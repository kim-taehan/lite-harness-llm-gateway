package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 게이트웨이가 직접 TLS 를 종단하는 선택 기능(GATEWAY_TLS_CERT_FILE·KEY_FILE)

// writeSelfSigned 는 127.0.0.1 용 자체 서명 인증서·키 파일을 쓴다. serial 로 인증서를 구별한다
func writeSelfSigned(t *testing.T, certFile, keyFile string, serial int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "gateway.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// servedSerial 은 서버가 이번 핸드셰이크에서 내놓은 인증서의 일련번호다. 버전 범위를 정해 접속하고, 접속 못 하면 오류다
func servedSerial(addr string, minVersion, maxVersion uint16) (int64, error) {
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: minVersion, MaxVersion: maxVersion}) //nolint:gosec // 시험: 어떤 인증서를 내놓는지만 본다
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return conn.ConnectionState().PeerCertificates[0].SerialNumber.Int64(), nil
}

func TestLoadConfig_TLSFilesComeInPairs(t *testing.T) {
	if cfg, err := LoadConfig(envWith(nil)); err != nil || cfg.TLSCertFile != "" || !strings.HasPrefix(cfg.SSO.PublicURL, "http://") {
		t.Errorf("기본 = %q %q, %v (평문 + http 주소여야 한다)", cfg.TLSCertFile, cfg.SSO.PublicURL, err)
	}
	cfg, err := LoadConfig(envWith(map[string]string{"GATEWAY_TLS_CERT_FILE": "/c.pem", "GATEWAY_TLS_KEY_FILE": "/k.pem"}))
	if err != nil || cfg.TLSCertFile != "/c.pem" || cfg.TLSKeyFile != "/k.pem" || !strings.HasPrefix(cfg.SSO.PublicURL, "https://") {
		t.Errorf("둘 다 = %q %q %q, %v (직접 종단하면 기본 공개 주소도 https)", cfg.TLSCertFile, cfg.TLSKeyFile, cfg.SSO.PublicURL, err)
	}
	for name, env := range map[string]map[string]string{
		"인증서만": {"GATEWAY_TLS_CERT_FILE": "/c.pem"},
		"키만":   {"GATEWAY_TLS_KEY_FILE": "/k.pem"},
	} {
		if _, err := LoadConfig(envWith(env)); err == nil {
			t.Errorf("%s 정했는데 설정이 만들어졌다(평문으로 뜨면 안 된다)", name)
		}
	}
}

func TestCertReloader_ServesTLS12AndUpAndPicksUpRenewedFilesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	writeSelfSigned(t, certFile, keyFile, 1)

	now := time.Now()
	logs := &strings.Builder{}
	reloader, err := newCertReloader(certFile, keyFile, slog.New(slog.NewTextHandler(logs, nil)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	// 운영(ServeTLS, Certificates 비어 있음)과 같게 우리 설정만으로 리스너를 만든다. httptest.StartTLS 는 자기 인증서를 끼워 넣는다
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }),
		ReadHeaderTimeout: time.Second, ErrorLog: slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil).WithGroup("tls"), slog.LevelError)}
	go func() { _ = server.Serve(tls.NewListener(plain, reloader.tlsConfig())) }() // 시험이 끝나면 Close 로 멈춘다
	t.Cleanup(func() { _ = server.Close() })
	addr := plain.Addr().String()

	if serial, err := servedSerial(addr, tls.VersionTLS12, tls.VersionTLS13); err != nil || serial != 1 {
		t.Fatalf("처음 인증서 = %d, %v", serial, err)
	}
	if _, err := servedSerial(addr, tls.VersionTLS13, tls.VersionTLS13); err != nil {
		t.Errorf("TLS 1.3 이 안 된다: %v", err)
	}
	if _, err := servedSerial(addr, tls.VersionTLS10, tls.VersionTLS11); err == nil {
		t.Error("TLS 1.0·1.1 로 접속이 됐다(1.2 이상만 받아야 한다)")
	}

	// 갱신: 같은 경로에 새 인증서를 쓰면 확인 간격이 지난 뒤 첫 핸드셰이크부터 새 것이다. 재기동이 필요 없다
	writeSelfSigned(t, certFile, keyFile, 2)
	later := time.Now().Add(time.Minute)
	for _, f := range []string{certFile, keyFile} {
		if err := os.Chtimes(f, later, later); err != nil {
			t.Fatal(err)
		}
	}
	if serial, _ := servedSerial(addr, tls.VersionTLS12, tls.VersionTLS13); serial != 1 {
		t.Errorf("확인 간격 전에 인증서가 바뀌었다: %d", serial)
	}
	now = now.Add(certReloadInterval + time.Second)
	if serial, err := servedSerial(addr, tls.VersionTLS12, tls.VersionTLS13); err != nil || serial != 2 {
		t.Fatalf("갱신한 인증서 = %d, %v", serial, err)
	}

	// 깨진 파일: 이전 인증서를 계속 쓰고 로그만 남긴다(서비스를 끊지 않는다)
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := later.Add(time.Minute)
	if err := os.Chtimes(certFile, broken, broken); err != nil {
		t.Fatal(err)
	}
	now = now.Add(certReloadInterval + time.Second)
	if serial, err := servedSerial(addr, tls.VersionTLS12, tls.VersionTLS13); err != nil || serial != 2 {
		t.Errorf("깨진 파일 뒤 인증서 = %d, %v (이전 것을 계속 써야 한다)", serial, err)
	}
	if !strings.Contains(logs.String(), "다시 읽지 못했다") {
		t.Errorf("재읽기 실패 로그가 없다: %s", logs.String())
	}

	// 기동 때 읽을 수 없으면 오류다
	if _, err := newCertReloader(filepath.Join(dir, "missing.crt"), keyFile, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now); err == nil {
		t.Error("없는 인증서 파일인데 기동할 수 있다")
	}
}

func TestSecurityHeaders_HSTSOnlyWhenTheGatewayTerminatesTLS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	plain := httptest.NewRecorder()
	withSecurityHeaders(Config{}, next).ServeHTTP(plain, httptest.NewRequest("GET", "/", nil))
	if got := plain.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("평문인데 HSTS 가 있다: %q", got)
	}
	secured := httptest.NewRecorder()
	withSecurityHeaders(Config{TLSCertFile: "/c.pem"}, next).ServeHTTP(secured, httptest.NewRequest("GET", "/", nil))
	if got := secured.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Errorf("HSTS = %q, max-age=31536000 이어야 한다(includeSubDomains 는 넣지 않는다)", got)
	}
}

// 앞단이 TLS 를 종단하면 게이트웨이는 평문으로 받는다. 신뢰하는 앞단이 https 라고 알려 준 요청에만 보안 쿠키를 심는다
func TestDashboardCookie_SecureWhenATrustedProxyReportsHTTPS(t *testing.T) {
	login := func(h http.Handler, headers map[string]string) string {
		b := browser{t: t, h: h}
		rec := b.doJSON("POST", "/v2/login", `{"username":"admin","password":`+jsonString(testMasterKey)+`}`, headers)
		if rec.Code != 200 {
			t.Fatalf("로그인 = %d", rec.Code)
		}
		return rec.Header().Get("Set-Cookie")
	}
	trusting := gatewayBehindProxy(t, testPeerAddr)
	if cookie := login(trusting, map[string]string{"X-Forwarded-Proto": "https"}); !strings.Contains(cookie, "Secure") {
		t.Errorf("신뢰하는 앞단이 https 라고 했는데 쿠키가 Secure 가 아니다: %q", cookie)
	}
	if cookie := login(trusting, nil); strings.Contains(cookie, "Secure") {
		t.Errorf("https 표시가 없는데 Secure 쿠키다(평문 연결에서는 브라우저가 못 쓴다): %q", cookie)
	}
	untrusting := gatewayBehindProxy(t, "")
	if cookie := login(untrusting, map[string]string{"X-Forwarded-Proto": "https"}); strings.Contains(cookie, "Secure") {
		t.Errorf("신뢰하지 않는 상대의 X-Forwarded-Proto 를 믿었다: %q", cookie)
	}
}
