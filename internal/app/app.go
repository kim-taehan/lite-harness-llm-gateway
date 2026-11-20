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
	"syscall"
	"time"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// version 은 빌드 때 build.sh 가 -ldflags 로 넣는다
var version = "dev"

// Config 는 환경 변수에서 읽는 설정이다(설계 11.1)
type Config struct {
	Listen       string         // GATEWAY_LISTEN, 기본 :4000 (litellm 기본 포트)
	MasterKey    auth.MasterKey // GATEWAY_MASTER_KEY, 필수
	DrainTimeout time.Duration  // 정상 종료 때 진행 중인 요청을 기다리는 시간
}

func LoadConfig(getenv func(string) string) (Config, error) {
	masterKey, err := auth.NewMasterKey(getenv("GATEWAY_MASTER_KEY"))
	if err != nil {
		return Config{}, err
	}
	listen := getenv("GATEWAY_LISTEN")
	if listen == "" {
		listen = ":4000"
	}
	return Config{Listen: listen, MasterKey: masterKey, DrainTimeout: 60 * time.Second}, nil
}

// Handler 는 모든 모듈의 라우트를 권한 검사로 감싸 등록한 핸들러다
func Handler(cfg Config, lifecycle *core.Lifecycle) http.Handler {
	authenticator := auth.NewAuthenticator(cfg.MasterKey)
	mux := http.NewServeMux()
	for _, route := range routes(lifecycle) {
		mux.Handle(route.Pattern(), authenticator.Guard(route))
	}
	// 선언되지 않은 경로는 litellm(FastAPI) 기본 404 와 같은 본문으로 답한다
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { core.WriteRouteNotFound(w) })
	return mux
}

// routes 는 모듈이 내놓은 라우트 전부다. 새 모듈은 여기에 한 줄을 더한다
func routes(lifecycle *core.Lifecycle) []core.Route {
	return core.HealthRoutes(lifecycle)
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
	lifecycle := &core.Lifecycle{}
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg, lifecycle),
		ReadHeaderTimeout: 10 * time.Second,
		// WriteTimeout 은 두지 않는다. LLM 스트림은 수 분 이어질 수 있다
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	logger.Info("기동", "version", version, "listen", listener.Addr().String())

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
