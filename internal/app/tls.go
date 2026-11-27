package app

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// 게이트웨이가 직접 TLS 를 종단하는 선택 기능이다(GATEWAY_TLS_CERT_FILE, GATEWAY_TLS_KEY_FILE). 둘 다 비면 평문으로 받고,
// 앞단(nginx 등)에서 종단하는 배치를 쓴다. 폐쇄망이라 사내 CA 로 서명한 인증서를 파일로 넣는 것을 가정한다

// certReloadInterval 은 인증서 파일이 바뀌었는지 보는 최소 간격이다. 핸드셰이크마다 파일을 보지 않으려는 값이다
const certReloadInterval = 10 * time.Second

// loadTLSFiles 는 환경 변수 두 개를 읽는다. 하나만 있으면 오류다(조용히 평문으로 뜨면 암호화한 줄 아는 운영자가 속는다)
func loadTLSFiles(getenv func(string) string) (certFile, keyFile string, err error) {
	certFile, keyFile = getenv("GATEWAY_TLS_CERT_FILE"), getenv("GATEWAY_TLS_KEY_FILE")
	if (certFile == "") != (keyFile == "") {
		return "", "", errors.New("GATEWAY_TLS_CERT_FILE 과 GATEWAY_TLS_KEY_FILE 은 함께 정해야 한다(하나만 있으면 평문으로 뜨므로 기동하지 않는다)")
	}
	return certFile, keyFile, nil
}

// certReloader 는 인증서·키 파일을 읽어 두고, 파일이 바뀌면(갱신) 다음 핸드셰이크부터 새 것을 쓴다. 재기동이 필요 없다.
// 다시 읽다 실패하면(파일을 쓰는 중이거나 짝이 안 맞음) 이전 인증서를 계속 쓰고 로그만 남긴다
type certReloader struct {
	certFile, keyFile string
	logger            *slog.Logger
	now               func() time.Time

	mu       sync.Mutex
	cert     *tls.Certificate
	modified [2]time.Time // 마지막으로 읽은 인증서·키 파일의 수정 시각
	checked  time.Time
}

// newCertReloader 는 기동 때 한 번 읽어 본다. 읽지 못하면 오류이고, 호출하는 쪽이 기동을 멈춘다
func newCertReloader(certFile, keyFile string, logger *slog.Logger, now func() time.Time) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile, logger: logger, now: now}
	if err := r.load(); err != nil {
		return nil, fmt.Errorf("TLS 인증서를 읽을 수 없다: %w", err)
	}
	r.checked = now()
	return r, nil
}

func (r *certReloader) load() error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return err
	}
	r.cert = &cert
	r.modified = [2]time.Time{modTime(r.certFile), modTime(r.keyFile)}
	return nil
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// GetCertificate 는 tls.Config.GetCertificate 다
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now := r.now(); now.Sub(r.checked) >= certReloadInterval {
		r.checked = now
		if modified := [2]time.Time{modTime(r.certFile), modTime(r.keyFile)}; modified != r.modified {
			if err := r.load(); err != nil {
				r.modified = modified // 같은 깨진 파일을 간격마다 다시 읽지 않는다. 파일이 또 바뀌면 다시 시도한다
				r.logger.Error("TLS 인증서를 다시 읽지 못했다. 이전 인증서를 계속 쓴다", "err", err)
			} else {
				r.logger.Info("TLS 인증서를 다시 읽었다")
			}
		}
	}
	return r.cert, nil
}

// tlsConfig 는 TLS 1.2 이상만 받는 설정이다(1.0·1.1 은 폐기됐다). 암호 묶음은 Go 기본값을 쓴다(1.3 은 고정, 1.2 는 안전한 것만)
func (r *certReloader) tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: r.GetCertificate}
}

// withSecurityHeaders 는 TLS 를 직접 종단할 때 HSTS 를 건다(브라우저가 이 주소를 평문으로 다시 열지 않게). 앞단이 종단하면 앞단이 정한다.
// includeSubDomains 는 넣지 않는다: 같은 도메인의 다른 서비스까지 묶으면 사내에서 사고가 난다
func withSecurityHeaders(cfg Config, next http.Handler) http.Handler {
	if cfg.TLSCertFile == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		next.ServeHTTP(w, r)
	})
}
