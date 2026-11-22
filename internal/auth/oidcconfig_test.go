package auth

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestOIDCConfig_NeverPrintsClientSecret(t *testing.T) {
	cfg := OIDCConfig{Issuer: "http://kc/realms/x", ClientID: "gw", ClientSecret: "s3cret-value", PublicURL: "http://gw"}

	if out := fmt.Sprintf("%v %+v %s", cfg, cfg, cfg); strings.Contains(out, "s3cret-value") {
		t.Errorf("fmt 출력에 비밀이 나갔다: %s", out)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("설정", "sso", cfg)
	if strings.Contains(buf.String(), "s3cret-value") || !strings.Contains(buf.String(), "client_id") {
		t.Errorf("slog 출력 = %s", buf.String())
	}
}
