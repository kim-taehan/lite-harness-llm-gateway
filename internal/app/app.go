// Package app 은 조립 지점이다. 설정을 읽고, 모듈의 라우트를 모아 권한 검사로 감싸 등록하고, 서버를 띄우고 내린다.
// 모든 모듈을 가져올 수 있는 유일한 패키지다.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"llm-gateway/internal/admin"
	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
	"llm-gateway/internal/limits"
)

// 카운터 저장소 종류(GATEWAY_COUNTER_STORE)
const (
	counterStoreMemory = "memory"
	counterStoreRedis  = "redis"
)

// Config 는 환경 변수에서 읽는 설정이다(설계 11.1)
type Config struct {
	Listen       string            // GATEWAY_LISTEN, 기본 :4000 (litellm 기본 포트)
	MasterKey    auth.MasterKey    // GATEWAY_MASTER_KEY, 필수
	DatabaseURL  string            // GATEWAY_DATABASE_URL, 필수
	DrainTimeout time.Duration     // 정상 종료 때 진행 중인 요청을 기다리는 시간
	Login        auth.LoginMethods // GATEWAY_LOGIN_METHODS
	SSO          auth.OIDCConfig   // 로그인 방식에 sso 가 있을 때만 쓴다
	JWT          auth.JWTConfig    // GATEWAY_JWT_ISSUER 가 있을 때만 켜진다. Keycloak 토큰으로 API 를 직접 부른다
	CORS         CORSConfig        // GATEWAY_CORS_ORIGINS, GATEWAY_CORS_ALLOW_CREDENTIALS
	CounterStore string            // GATEWAY_COUNTER_STORE: memory(기본, 인스턴스 1개) 또는 redis(여러 대)
	RedisURL     string            // GATEWAY_REDIS_URL: CounterStore 가 redis 일 때만
	// GATEWAY_TLS_CERT_FILE·GATEWAY_TLS_KEY_FILE: 둘 다 있으면 게이트웨이가 TLS 를 직접 종단한다(TLS 1.2 이상, 파일이 바뀌면 다시 읽는다).
	// 둘 다 비면 평문이고 앞단이 종단한다
	TLSCertFile, TLSKeyFile string
	// GATEWAY_AUDIT_RETENTION_DAYS: 감사 기록 보관 기간. 0 이면 지우지 않는다(영구 보관). 기본 365일
	AuditRetention time.Duration
	// GATEWAY_LIMITS_FAIL_CLOSED=true: Redis 장애 동안 한도가 걸린 요청을 거부한다. 기본은 메모리 카운터로 이어서 처리
	LimitsFailClosed bool
	// GATEWAY_TRUSTED_PROXIES: X-Forwarded-For 를 믿을 앞단 주소. 비면 쓰지 않는다
	TrustedProxies auth.TrustedProxies
	// GATEWAY_ALLOWED_IPS_DISABLE=true: IP 허용 목록을 무시한다(목록에서 자기 IP 를 빼고 저장해 잠겼을 때의 비상 해제)
	AllowedIPsDisabled bool
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
	methods, err := loadLoginMethods(getenv)
	if err != nil {
		return Config{}, err
	}
	counterStore, redisURL, err := loadCounterStore(getenv)
	if err != nil {
		return Config{}, err
	}
	failClosed := strings.EqualFold(strings.TrimSpace(getenv("GATEWAY_LIMITS_FAIL_CLOSED")), "true")
	if failClosed && counterStore != counterStoreRedis {
		return Config{}, errors.New("GATEWAY_LIMITS_FAIL_CLOSED=true 는 GATEWAY_COUNTER_STORE=redis 일 때만 뜻이 있다(메모리 카운터는 장애가 없다)")
	}
	auditRetention, err := loadAuditRetention(getenv)
	if err != nil {
		return Config{}, err
	}
	tlsCert, tlsKey, err := loadTLSFiles(getenv)
	if err != nil {
		return Config{}, err
	}
	sso := auth.OIDCConfig{Issuer: getenv("GATEWAY_SSO_ISSUER"), ClientID: getenv("GATEWAY_SSO_CLIENT_ID"),
		ClientSecret: getenv("GATEWAY_SSO_CLIENT_SECRET"), PublicURL: getenv("GATEWAY_PUBLIC_URL")}
	if sso.PublicURL == "" {
		scheme := "http" // 직접 종단하면 브라우저가 열 주소도 https 다
		if tlsCert != "" {
			scheme = "https"
		}
		sso.PublicURL = scheme + "://localhost" + listen
		if strings.HasPrefix(listen, "0.0.0.0:") || strings.HasPrefix(listen, "[::]:") {
			sso.PublicURL = scheme + "://localhost:" + listen[strings.LastIndex(listen, ":")+1:]
		}
	}
	proxies, err := auth.ParseTrustedProxies(getenv("GATEWAY_TRUSTED_PROXIES"))
	if err != nil {
		return Config{}, err
	}
	jwt, err := loadJWT(getenv)
	if err != nil {
		return Config{}, err
	}
	return Config{Listen: listen, JWT: jwt, MasterKey: masterKey, DatabaseURL: databaseURL, DrainTimeout: 60 * time.Second, Login: methods, SSO: sso,
		CORS: loadCORS(getenv), TrustedProxies: proxies, CounterStore: counterStore, RedisURL: redisURL,
		TLSCertFile: tlsCert, TLSKeyFile: tlsKey, AuditRetention: auditRetention, LimitsFailClosed: failClosed, AllowedIPsDisabled: strings.EqualFold(strings.TrimSpace(getenv("GATEWAY_ALLOWED_IPS_DISABLE")), "true")}, nil
}

// loadJWT 는 GATEWAY_JWT_* 를 읽는다. GATEWAY_JWT_ISSUER 가 없으면 JWT 직접 인증은 꺼진다.
// 그룹 매핑(GATEWAY_JWT_GROUP_MAP)은 {"그룹": {"team_id": "...", "admin": true}} 모양의 JSON 이고, 모르는 필드·깨진 JSON 은 기동을 멈춘다
// (조용히 건너뛰면 의도한 권한과 다르게 동작한다)
func loadJWT(getenv func(string) string) (auth.JWTConfig, error) {
	cfg := auth.JWTConfig{Issuer: strings.TrimSpace(getenv("GATEWAY_JWT_ISSUER")), Audience: strings.TrimSpace(getenv("GATEWAY_JWT_AUDIENCE")),
		JWKSURL: strings.TrimSpace(getenv("GATEWAY_JWT_JWKS_URL")), GroupsClaim: strings.TrimSpace(getenv("GATEWAY_JWT_GROUPS_CLAIM"))}
	if raw := strings.TrimSpace(getenv("GATEWAY_JWT_GROUP_MAP")); raw != "" {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg.Groups); err != nil {
			return auth.JWTConfig{}, fmt.Errorf("GATEWAY_JWT_GROUP_MAP: %w", err)
		}
	}
	if !cfg.Enabled() && (cfg.Audience != "" || cfg.JWKSURL != "" || len(cfg.Groups) > 0) {
		return auth.JWTConfig{}, errors.New("GATEWAY_JWT_ISSUER 없이 다른 GATEWAY_JWT_* 만 있다. JWT 인증을 켜려면 발급자를 정한다")
	}
	if err := cfg.Validate(); err != nil {
		return auth.JWTConfig{}, fmt.Errorf("GATEWAY_JWT_*: %w", err)
	}
	return cfg, nil
}

// defaultAuditRetentionDays 는 감사 기록의 기본 보관 기간이다. 사용량 기록(90일)보다 길게 둔다: 점검·사고 조사는 몇 달 뒤에 오기도 한다
const defaultAuditRetentionDays = 365

// loadAuditRetention 은 GATEWAY_AUDIT_RETENTION_DAYS 를 읽는다. 비우면 365일, 0 이면 영구 보관, 양의 정수면 그 일수다.
// 음수나 숫자가 아닌 값은 오타일 가능성이 커서 기동을 멈춘다(조용히 기본값으로 돌리면 의도와 다른 기간으로 지워진다)
func loadAuditRetention(getenv func(string) string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv("GATEWAY_AUDIT_RETENTION_DAYS"))
	if raw == "" {
		return defaultAuditRetentionDays * 24 * time.Hour, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < 0 {
		return 0, fmt.Errorf("GATEWAY_AUDIT_RETENTION_DAYS %q 는 0 이상의 정수여야 한다(0 은 영구 보관)", raw)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// loadLoginMethods 는 GATEWAY_LOGIN_METHODS(password, sso, sso,password)를 읽는다. password 는 litellm 자체 로그인(아이디·비밀번호),
// sso 는 Keycloak 이다. 비어 있으면 비밀번호는 늘 켜고 SSO 는 GATEWAY_SSO_ISSUER 가 있을 때만 켠다.
// 환경 변수로 받으므로 프로세스를 띄울 때 정해지고, 관리 API 로는 바꿀 수 없다(설계 6장)
func loadLoginMethods(getenv func(string) string) (auth.LoginMethods, error) {
	value := strings.TrimSpace(getenv("GATEWAY_LOGIN_METHODS"))
	if value == "" {
		return auth.LoginMethods{Password: true, SSO: getenv("GATEWAY_SSO_ISSUER") != ""}, nil
	}
	methods, err := auth.ParseLoginMethods(value)
	if err != nil {
		return auth.LoginMethods{}, fmt.Errorf("GATEWAY_LOGIN_METHODS: %w", err)
	}
	return methods, nil
}

// loadCounterStore 는 GATEWAY_COUNTER_STORE(memory, redis)와 redis 일 때의 GATEWAY_REDIS_URL 을 읽는다.
// Redis 없이 여러 대를 띄우면 한도가 대수만큼 늘어나므로 여러 대일 때는 redis 로 둔다(설계 7장)
func loadCounterStore(getenv func(string) string) (store, redisURL string, err error) {
	switch store = strings.TrimSpace(getenv("GATEWAY_COUNTER_STORE")); store {
	case "", counterStoreMemory:
		return counterStoreMemory, "", nil
	case counterStoreRedis:
		if redisURL = getenv("GATEWAY_REDIS_URL"); redisURL == "" {
			return "", "", errors.New("GATEWAY_COUNTER_STORE=redis 인데 GATEWAY_REDIS_URL 이 없다")
		}
		return counterStoreRedis, redisURL, nil
	default:
		return "", "", fmt.Errorf("GATEWAY_COUNTER_STORE %q 를 모른다(memory, redis 만 된다)", store)
	}
}

// keyCacheTTL 은 litellm 기본(60초)과 같다. 다른 인스턴스에서 바꾼 키는 이 시간 안에 반영된다(설계 3.2)
const keyCacheTTL = 60 * time.Second

// modules 는 조립한 모듈이다. 한 번 만들어 권한 검사와 라우트가 함께 쓴다
type modules struct {
	authenticator *auth.Authenticator
	ipAllow       *auth.IPAllowlist // 모든 요청 앞에서 목록 밖 IP 를 막고, 관리 API 가 같은 인스턴스로 목록을 고친다
	routes        []core.Route
	guardrails    *inference.Guardrails // 막은 횟수를 지표로 세려고 조립 뒤에 부르는 쪽이 관찰자를 건다
}

// identity 는 auth 의 저장소들이다. 로그인(SSO 설정 읽기에 ctx 가 필요하다)과 나머지 조립이 함께 쓴다
type identity struct {
	db    *core.DB // 로그인 사건을 감사 기록에 남기는 데 쓴다
	cache *auth.KeyCache
	users *auth.Users
	teams *auth.Teams
}

func newIdentity(db *core.DB) identity {
	cache := auth.NewKeyCache(keyCacheTTL, time.Now)
	users := auth.NewUsers(db, cache)
	return identity{db: db, cache: cache, users: users, teams: auth.NewTeams(db, users, cache)}
}

// newLogin 은 설정한 로그인 방식으로 로그인을 만든다. SSO 를 골랐으면 Keycloak 설정을 읽는다
func newLogin(ctx context.Context, cfg Config, id identity, logger *slog.Logger) (*auth.Login, error) {
	return auth.NewLogin(ctx, auth.LoginDeps{Methods: cfg.Login, Master: cfg.MasterKey, OIDC: cfg.SSO, Users: id.users, Teams: id.teams,
		Client: &http.Client{Timeout: 10 * time.Second, Transport: noProxyTransport()}, Proxies: cfg.TrustedProxies, Events: admin.NewAuthAudit(id.db, time.Now, cfg.MasterKey.AuditChainKey(), logger), Now: time.Now, Logger: logger})
}

// parts 는 조립에 필요한, 이미 만들어 둔 부품이다
type parts struct {
	lifecycle *core.Lifecycle
	db        *core.DB
	id        identity
	login     *auth.Login
	usage     *limits.UsageRecorder
	counters  limits.CounterStore // nil 이면 메모리 카운터(인스턴스 1개)
	logger    *slog.Logger
}

// openCounters 는 설정대로 카운터 저장소를 연다. 돌려주는 close 는 종료 때 부른다
func openCounters(ctx context.Context, cfg Config, logger *slog.Logger) (limits.CounterStore, func(), error) {
	if cfg.CounterStore != counterStoreRedis {
		return limits.NewMemoryCounters(time.Now), func() {}, nil
	}
	redisCounters, err := limits.OpenRedisCounters(ctx, cfg.RedisURL, time.Now, logger)
	if err != nil {
		return nil, nil, err
	}
	redisCounters.FailClosed = cfg.LimitsFailClosed
	return redisCounters, func() {
		if err := redisCounters.Close(); err != nil {
			logger.Warn("Redis 연결 닫기 실패", "err", err)
		}
	}, nil
}

// assemble 의 usage 는 추론 경로가 사용량을 넘기는 곳이다. 운영에서는 지표를 세는 래퍼를 거쳐 p.usage 로 간다
func assemble(cfg Config, p parts, usage usageSink) modules {
	db, logger := p.db, p.logger
	cache, users, teams := p.id.cache, p.id.users, p.id.teams
	keys := auth.NewKeys(db, cache)
	policies := limits.NewPolicies(db, time.Now)
	counters := p.counters
	if counters == nil {
		counters = limits.NewMemoryCounters(time.Now)
	}
	deployments := inference.NewDeployments(db, cfg.MasterKey.UpstreamKeyBox())
	guardrails := inference.NewGuardrails(db, time.Now)
	settings := core.NewSettings(db)
	ipAllow := auth.NewIPAllowlist(settings, cfg.TrustedProxies, time.Now, cfg.AllowedIPsDisabled, logger)
	authenticator := auth.NewAuthenticator(cfg.MasterKey, keys, users, teams, cache, time.Now)
	if cfg.JWT.Enabled() {
		authenticator.EnableJWT(cfg.JWT, &http.Client{Timeout: 5 * time.Second, Transport: noProxyTransport()}, logger)
	}
	authenticator.RecordDenials(admin.NewAuthAudit(db, time.Now, cfg.MasterKey.AuditChainKey(), logger), cfg.TrustedProxies)
	return modules{
		authenticator: authenticator,
		ipAllow:       ipAllow,
		guardrails:    guardrails,
		// 모듈이 내놓은 라우트 전부다. 새 모듈은 여기에 한 줄을 더한다
		routes: slices.Concat(
			core.HealthRoutes(p.lifecycle, db),
			p.login.Routes(),
			inference.NewHandlers(deployments, upstreamClient(), usage, limits.NewGate(policies, counters, time.Now), guardrails, time.Now, logger).Routes(),
			admin.NewHandlers(admin.Deps{DB: db, Teams: teams, Users: users, Keys: keys, Projects: auth.NewProjects(db, cache), Organizations: auth.NewOrganizations(db, cache), Policies: policies, Deployments: deployments, Guardrails: guardrails,
				Usage: limits.NewUsageQuery(db), Settings: settings, IPAllow: ipAllow, AuditChainKey: cfg.MasterKey.AuditChainKey(), Now: time.Now, Logger: logger}).Routes(),
		),
	}
}

// upstreamClient 는 업스트림(Ollama 등) 호출용이다. 연결 5초, 응답 헤더 300초(로컬 모델은 첫 토큰이 느리다. 설계 4장).
// 전체 시간 제한(Client.Timeout)은 두지 않는다. 스트림은 수 분 이어지고, 클라이언트가 끊으면 요청 ctx 로 취소된다
func upstreamClient() *http.Client {
	transport := noProxyTransport()
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 300 * time.Second
	// 유휴 연결을 충분히 보관한다. Go 기본은 호스트당 2개라 동시 요청이 그보다 많으면 응답마다 연결을 닫고 새로 맺는다.
	// 모델 서버는 보통 한 곳이라 초당 수백 건에서 닫힌 연결이 TIME_WAIT 로 쌓여 임시 포트가 바닥나고
	// "cannot assign requested address" 로 업스트림 호출이 503 이 된다(부하 시험 2026-11-27)
	transport.MaxIdleConns = 2048
	transport.MaxIdleConnsPerHost = 1024
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

// requestLogRetention 은 설계 11.1 기본값(90일)이다. 환경 변수로 바꾸는 건 필요해질 때 더한다
const requestLogRetention = 90 * 24 * time.Hour

// buildHandler 는 모듈을 조립하고, 모든 라우트를 권한 검사로 감싸 등록한 핸들러다. usage 의 Run 은 부르는 쪽이 띄운다
func buildHandler(cfg Config, p parts) http.Handler {
	metrics := newMetrics(p.usage)
	metrics.trackSoftBudgets(limits.NewPolicies(p.db, time.Now), p.logger)
	m := assemble(cfg, p, metrics.observing(p.usage))
	m.guardrails.OnBlock(metrics.guardrailBlocked)
	mux := http.NewServeMux()
	for _, route := range append(m.routes, metrics.route()) {
		mux.Handle(route.Pattern(), m.authenticator.Guard(route))
	}
	// 선언되지 않은 경로는 litellm(FastAPI) 기본 404 와 같은 본문으로 답한다
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { core.WriteRouteNotFound(w) })
	// 허용 목록은 지표 안쪽이라 막힌 요청(403)도 센다
	return withSecurityHeaders(cfg, withCORS(cfg.CORS, metrics.instrument(m.ipAllow.Middleware(mux))))
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
	counters, closeCounters, err := openCounters(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("카운터 저장소: %w", err)
	}
	defer closeCounters()
	background, stopBackground := context.WithCancel(context.WithoutCancel(ctx))
	// 백그라운드 job 은 루트 ctx 아래에서 띄우고, 종료 때 취소한 뒤 끝나길 기다린다. 그래야 DB 를 닫기 전에 멈춘다(설계 3.3)
	var jobs sync.WaitGroup
	defer func() {
		stopBackground()
		jobs.Wait()
	}()
	go usage.Run(background)
	jobs.Go(func() { limits.NewPolicies(db, time.Now).RunBudgetResets(background, logger) })
	jobs.Go(func() { limits.NewLogRetention(db, time.Now, requestLogRetention).Run(background, logger) })
	if cfg.AuditRetention > 0 {
		jobs.Go(func() {
			admin.NewAuditRetention(db, time.Now, cfg.MasterKey.AuditChainKey(), cfg.AuditRetention).Run(background, logger)
		})
	} else {
		logger.Warn("감사 기록을 영구 보관한다(GATEWAY_AUDIT_RETENTION_DAYS=0). 테이블이 계속 커진다")
	}
	defer usage.Close()

	id := newIdentity(db)
	login, err := newLogin(ctx, cfg, id, logger)
	if err != nil {
		return fmt.Errorf("로그인 설정: %w", err)
	}
	lifecycle := &core.Lifecycle{}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           buildHandler(cfg, parts{lifecycle: lifecycle, db: db, id: id, login: login, usage: usage, counters: counters, logger: logger}),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 은 두지 않는다. LLM 스트림은 수 분 이어질 수 있다
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	logger.Info("기동", "version", core.Version, "listen", listener.Addr().String(), "login", cfg.Login.String(), "jwt", cfg.JWT.Enabled(), "tls", cfg.TLSCertFile != "")
	if cfg.AllowedIPsDisabled {
		logger.Warn("IP 허용 목록이 비상 해제돼 있다(GATEWAY_ALLOWED_IPS_DISABLE). 목록을 고친 뒤 이 설정을 지우고 다시 띄운다")
	}

	serve := func() error { return server.Serve(listener) }
	if cfg.TLSCertFile != "" {
		certs, err := newCertReloader(cfg.TLSCertFile, cfg.TLSKeyFile, logger, time.Now)
		if err != nil {
			_ = listener.Close() // 기동을 멈추는 중이라 닫기 오류는 더 알릴 것이 없다
			return err
		}
		if !strings.HasPrefix(cfg.SSO.PublicURL, "https://") {
			logger.Warn("TLS 를 직접 종단하는데 GATEWAY_PUBLIC_URL 이 https 가 아니다. 로그인 뒤 이동 주소와 SSO 되돌림 주소가 평문으로 간다", "public_url", cfg.SSO.PublicURL)
		}
		server.TLSConfig = certs.tlsConfig()
		serve = func() error { return server.ServeTLS(listener, "", "") }
	} else {
		logger.Warn("TLS 없이 평문으로 받는다. 앞단(nginx 등)에서 TLS 를 종단하지 않으면 비밀번호·키가 평문으로 오간다(GATEWAY_TLS_CERT_FILE·GATEWAY_TLS_KEY_FILE)")
	}
	served := make(chan error, 1)
	go func() { served <- serve() }()

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
