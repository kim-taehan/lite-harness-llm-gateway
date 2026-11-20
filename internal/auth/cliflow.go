package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CLI 로그인 흐름 저장소(K 3장). litellm 은 캐시(메모리 또는 Redis)에만 두고 TTL 600초다. 지금은 메모리라 인스턴스 1대 전용이다.
// 비밀 값(poll_secret, user_code, browser_complete_token)은 해시만 둔다
const (
	cliFlowTTL       = 600 * time.Second
	cliStartLimit    = 30 // IP 마다 60초에
	cliStartWindow   = 60 * time.Second
	userCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // I·O·0·1 없음
)

var loginIDPattern = regexp.MustCompile(`^cli-[A-Za-z0-9_-]{12,124}$`)

// cliSession 은 로그인을 마친 사용자다. 폴링이 이것으로 키를 만든다
type cliSession struct {
	UserID string
	Teams  []teamDetail
}

type teamDetail struct {
	TeamID     string   `json:"team_id"`
	TeamAlias  *string  `json:"team_alias"`
	TeamModels []string `json:"team_models"`
	// litellm 은 팀 모델 별칭을 함께 준다. 우리는 별칭이 없다
	TeamModelAliases map[string]string `json:"team_model_aliases"`
}

type cliFlow struct {
	pollSecretHash   string
	userCodeHash     string
	browserTokenHash string
	pkceVerifier     string
	session          *cliSession
	codeVerified     bool
	expires          time.Time
}

type cliFlows struct {
	now    func() time.Time
	mu     sync.Mutex
	flows  map[string]*cliFlow
	starts map[string][]time.Time
}

func newCLIFlows(now func() time.Time) *cliFlows {
	return &cliFlows{now: now, flows: map[string]*cliFlow{}, starts: map[string][]time.Time{}}
}

// start 는 새 흐름이다. ip 가 60초에 30번을 넘기면 ok=false
func (f *cliFlows) start(ipHash string) (loginID, pollSecret, userCode string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	recent := f.starts[ipHash][:0]
	for _, t := range f.starts[ipHash] {
		if now.Sub(t) < cliStartWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= cliStartLimit {
		f.starts[ipHash] = recent
		return "", "", "", false
	}
	f.starts[ipHash] = append(recent, now)
	f.sweep(now)
	loginID = "cli-" + randomURLSafe(24)
	pollSecret = randomURLSafe(32)
	userCode = newUserCode()
	f.flows[loginID] = &cliFlow{pollSecretHash: sha256Hex(pollSecret), userCodeHash: sha256Hex(normalizeUserCode(userCode)), expires: now.Add(cliFlowTTL)}
	return loginID, pollSecret, userCode, true
}

// with 은 살아 있는 흐름을 잠근 채 넘긴다. 바꾸면 TTL 을 다시 센다(litellm 도 다시 쓸 때마다 600초)
func (f *cliFlows) with(loginID string, fn func(*cliFlow) (touch bool)) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	flow, ok := f.flows[loginID]
	if !ok || !f.now().Before(flow.expires) {
		delete(f.flows, loginID)
		return false
	}
	if fn(flow) {
		flow.expires = f.now().Add(cliFlowTTL)
	}
	return true
}

func (f *cliFlows) remove(loginID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.flows, loginID)
}

func (f *cliFlows) sweep(now time.Time) {
	for id, flow := range f.flows {
		if !now.Before(flow.expires) {
			delete(f.flows, id)
		}
	}
}

func newUserCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	var sb strings.Builder
	for i, v := range b {
		if i == 4 {
			sb.WriteByte('-')
		}
		sb.WriteByte(userCodeAlphabet[int(v)%len(userCodeAlphabet)])
	}
	return sb.String()
}

// normalizeUserCode 는 litellm 처럼 대문자로 바꾸고 영숫자만 남긴다(AB-cd → ABCD)
func normalizeUserCode(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
