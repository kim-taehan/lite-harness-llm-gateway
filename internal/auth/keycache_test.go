package auth

import (
	"testing"
	"time"
)

func TestHashKey_IsSha256HexLikeLitellm(t *testing.T) {
	// printf sk-1234 | shasum -a 256
	const want = "88dc28d0f030c55ed4ab77ed8faf098196cb1c05df778539800c9f1243fe6b4b"
	if got := hashKey("sk-1234"); got != want {
		t.Errorf("hashKey = %s, 기대 %s", got, want)
	}
}

func TestKeyCache_ExpiresAfterTTLAndRemoves(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := newKeyCache[string](30*time.Second, func() time.Time { return now })

	c.put("h1", "team-a")
	c.put("h2", "team-b")
	if v, ok := c.get("h1"); !ok || v != "team-a" {
		t.Fatalf("방금 넣은 값: %q %v", v, ok)
	}
	c.removeWhere(func(v string) bool { return v == "team-b" })
	if _, ok := c.get("h2"); ok {
		t.Error("removeWhere 가 지우지 않았다")
	}
	now = now.Add(30 * time.Second)
	if _, ok := c.get("h1"); ok {
		t.Error("TTL 이 지났는데 남았다")
	}
}
