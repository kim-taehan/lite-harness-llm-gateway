// Package app 은 조립 지점이다. 설정을 읽고, 모듈의 라우트를 모아 권한 검사로 감싸 등록하고, 서버를 띄우고 내린다.
// 모든 모듈을 가져올 수 있는 유일한 패키지다.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/internal/admin"
	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// version 과 loginMethods 는 빌드 때 build.sh 가 -ldflags 로 넣는다. 로그인 방식은 빌드 시점에 정하고 실행 중에는 바꿀 수 없다(설계 6장)
var (
	version      = "dev"
	loginMethods = "sso,password"
)

// Config 는 환경 변수에서 읽는 설정이다(설계 11.1)
type Config struct {
	Listen       string         // GATEWAY_LISTEN, 기본 :4000 (litellm 기본 포트)
	MasterKey    auth.MasterKey // GATEWAY_MASTER_KEY, 필수
	DatabaseURL  string         // GATEWAY_DATABASE_URL, 필수
	DrainTimeout time.Duration  // 정상 종료 때 진행 중인 요청을 기다리는 시간
	Login        auth.LoginMethods
	SSO          auth.OIDCConfig // SSO 가 들어간 빌드에서만 쓴다
}

// ErrNoDatabaseURL 은 DB 주소가 없을 때다. 신원·한도의 진실 원천이 PostgreSQL 이라 없이는 기동하지 않는다
var ErrNoDatabaseURL = errors.New("GATEWAY_DATABASE_URL 이 없다. PostgreSQL 없이는 기동하지 않는다")

func LoadConfig(getenv func(string) string) (Config, error) {
	masterKey, err := auth.NewMasterKey(getenv("GATEWAY_MASTER_KEY"))
	if err != nil {
		return Config{}, err
	}
	databaseURL := getenv("GATEWAY_DATABASE_URL")
	if databaseURL == "" {
		return Config{}, ErrNoDatabaseURL
	}
	listen := getenv("GATEWAY_LISTEN")
	if listen == "" {
		listen = ":4000"
	}
	methods, err := auth.ParseLoginMethods(loginMethods)
	if err != nil {
		return Config{}, fmt.Errorf("빌드에 새긴 로그인 방식이 틀렸다: %w", err)
	}
	sso := auth.OIDCConfig{Issuer: getenv("GATEWAY_SSO_ISSUER"), ClientID: getenv("GATEWAY_SSO_CLIENT_ID"),
		ClientSecret: getenv("GATEWAY_SSO_CLIENT_SECRET"), PublicURL: getenv("GATEWAY_PUBLIC_URL")}
	if sso.PublicURL == "" {
		sso.PublicURL = "http://localhost" + listen
		if strings.HasPrefix(listen, "0.0.0.0:") || strings.HasPrefix(listen, "[::]:") {
			sso.PublicURL = "http://localhost:" + listen[strings.LastIndex(listen, ":")+1:]
		}
	}
	return Config{Listen: listen, MasterKey: masterKey, DatabaseURL: databaseURL, DrainTimeout: 60 * time.Second, Login: methods, SSO: sso}, nil
}

// keyCacheTTL 은 litellm 기본(60초)과 같다. 다른 인스턴스에서 바꾼 키는 이 시간 안에 반영된다(설계 3.2)
const keyCacheTTL = 60 * time.Second

// modules 는 조립한 모듈이다. 한 번 만들어 권한 검사와 라우트가 함께 쓴다
type modules struct {
	authenticator *auth.Authenticator
	routes        []core.Route
}

// identity 는 auth 의 저장소들이다. 로그인(SSO 설정 읽기에 ctx 가 필요하다)과 나머지 조립이 함께 쓴다
type identity struct {
	cache *auth.KeyCache
	users *auth.Users
	teams *auth.Teams
}

func newIdentity(db *core.DB) identity {
	cache := auth.NewKeyCache(keyCacheTTL, time.Now)
	users := auth.NewUsers(db, cache)
	return identity{cache: cache, users: users, teams: auth.NewTeams(db, users, cache)}
}

// newLogin 은 빌드에 들어간 로그인 방식으로 로그인을 만든다. SSO 가 들어갔으면 Keycloak 설정을 읽는다
func newLogin(ctx context.Context, cfg Config, id identity, logger *slog.Logger) (*auth.Login, error) {
	return auth.NewLogin(ctx, cfg.Login, cfg.MasterKey, cfg.SSO, id.users, id.teams, &http.Client{Timeout: 10 * time.Second, Transport: noProxyTransport()}, time.Now, logger)
}

func assemble(cfg Config, lifecycle *core.Lifecycle, db *core.DB, id identity, login *auth.Login, usage *limits.UsageRecorder, logger *slog.Logger) modules {
	cache, users, teams := id.cache, id.users, id.teams
	keys := auth.NewKeys(db, cache)
	policies := limits.NewPolicies(db, time.Now)
	deployments := inference.NewDeployments(db)
	return modules{
		authenticator: auth.NewAuthenticator(cfg.MasterKey, keys, users, teams, cache, time.Now),
		// 모듈이 내놓은 라우트 전부다. 새 모듈은 여기에 한 줄을 더한다
		routes: slices.Concat(
			core.HealthRoutes(lifecycle, db),
			login.Routes(),
			inference.NewHandlers(deployments, upstreamClient(), usage, limits.NewGate(policies, limits.NewMemoryCounters(time.Now), time.Now), time.Now, logger).Routes(),
			admin.NewHandlers(db, teams, users, keys, policies, deployments, limits.NewUsageQuery(db), time.Now, logger).Routes(),
		),
	}
}

// upstreamClient 는 업스트림(Ollama 등) 호출용이다. 연결 5초, 응답 헤더 300초(로컬 모델은 첫 토큰이 느리다. 설계 4장).
// 전체 시간 제한(Client.Timeout)은 두지 않는다. 스트림은 수 분 이어지고, 클라이언트가 끊으면 요청 ctx 로 취소된다
func upstreamClient() *http.Client {
	transport := noProxyTransport()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 300 * time.Second
	return &http.Client{Transport: transport}
}

// noProxyTransport 는 환경 변수 프록시를 타지 않는다(폐쇄망: 호출 대상은 내부 주소뿐이다)
func noProxyTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return transport
}

// usageOptions 는 설계 11.1 기본값이다(100건 또는 1초, 채널 1만)
var usageOptions = limits.UsageOptions{BatchSize: 100, FlushInterval: time.Second, QueueCapacity: 10_000}

// Handler 는 모듈을 조립하고, 모든 라우트를 권한 검사로 감싸 등록한 핸들러다. usage 의 Run 은 부르는 쪽이 띄운다
func Handler(cfg Config, lifecycle *core.Lifecycle, db *core.DB, id identity, login *auth.Login, usage *limits.UsageRecorder, logger *slog.Logger) http.Handler {
	m := assemble(cfg, lifecycle, db, id, login, usage, logger)
	mux := http.NewServeMux()
	for _, route := range m.routes {
		mux.Handle(route.Pattern(), m.authenticator.Guard(route))
	}
	// 선언되지 않은 경로는 litellm(FastAPI) 기본 404 와 같은 본문으로 답한다
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { core.WriteRouteNotFound(w) })
	return mux
}

// Run 은 서버를 띄우고 종료 신호를 받으면 정상 종료한다. 종료 코드를 돌려준다
func Run(stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, logger); err != nil {
		logger.Error("서버 종료", "err", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg Config, logger *slog.Logger) error {
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("DB 연결 설정: %w", err)
	}
	defer pool.Close()
	// 기동 시 마이그레이션. 여러 대가 동시에 떠도 advisory lock 으로 한 대만 적용한다(설계 2장)
	if err := core.Migrate(ctx, pool, logger); err != nil {
		return fmt.Errorf("마이그레이션: %w", err)
	}
	db := core.NewDB(pool)
	// 사용량 배치 쓰기. 종료 때 서버를 먼저 멈추고 채널을 비운 다음에야 DB 를 닫는다(설계 3.3 정상 종료 3·4번)
	usage := limits.NewUsageRecorder(db, logger, usageOptions)
	background, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	defer stopBackground()
	go usage.Run(background)
	defer usage.Close()

	id := newIdentity(db)
	login, err := newLogin(ctx, cfg, id, logger)
	if err != nil {
		return fmt.Errorf("로그인 설정: %w", err)
	}
	lifecycle := &core.Lifecycle{}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg, lifecycle, db, id, login, usage, logger),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 은 두지 않는다. LLM 스트림은 수 분 이어질 수 있다
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	logger.Info("기동", "version", version, "listen", listener.Addr().String(), "login", cfg.Login.String())

	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// 설계 3.3 정상 종료: liveness 를 먼저 내리고, 진행 중인 요청을 기다린 뒤 닫는다
	lifecycle.BeginShutdown()
	logger.Info("종료 시작", "drain_timeout", cfg.DrainTimeout.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.DrainTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("종료")
	return nil
}
