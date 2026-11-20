package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	"llm-gateway/internal/core"
)

// hashKey 는 저장·조회에 쓰는 키 해시다. 원문은 저장하지 않는다(설계 6장). litellm 의 hash_token 과 같은 sha256 hex 다
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// TokenOf 는 관리 API 가 받은 키 값을 저장 해시로 바꾼다. litellm hash_token_if_needed 처럼
// sk- 로 시작하면 원문으로 보고 해시하고, 아니면 이미 해시로 본다(대시보드는 해시를 보낸다)
func TokenOf(keyOrHash string) string {
	if len(keyOrHash) >= 3 && keyOrHash[:3] == "sk-" {
		return hashKey(keyOrHash)
	}
	return keyOrHash
}

// keyCache 는 검증한 키를 잠깐 기억해 요청마다 DB 를 읽지 않게 한다.
// 키를 바꾸면(차단·삭제·수정) 그 자리에서 지우고, 다른 인스턴스에서 바뀐 것은 TTL 이 지나면 반영된다(설계 3.2).
// 없는 키는 기억하지 않는다. 아무 문자열이나 보내 캐시를 채우는 것을 막기 위해서다
type keyCache[V any] struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]cacheEntry[V]
}

type cacheEntry[V any] struct {
	value   V
	expires time.Time
}

func newKeyCache[V any](ttl time.Duration, now func() time.Time) *keyCache[V] {
	return &keyCache[V]{ttl: ttl, now: now, entries: map[string]cacheEntry[V]{}}
}

func (c *keyCache[V]) get(hash string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[hash]
	if !ok || !c.now().Before(entry.expires) {
		delete(c.entries, hash)
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (c *keyCache[V]) put(hash string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[hash] = cacheEntry[V]{value: value, expires: c.now().Add(c.ttl)}
}

func (c *keyCache[V]) remove(hashes ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, h := range hashes {
		delete(c.entries, h)
	}
}

// removeWhere 는 조건에 맞는 항목을 지운다(예: 차단한 팀의 키 전부)
func (c *keyCache[V]) removeWhere(match func(V) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for h, entry := range c.entries {
		if match(entry.value) {
			delete(c.entries, h)
		}
	}
}

// resolvedKey 는 검증에 필요한 것을 한 번에 읽어 둔 키다. 만료는 시각이 지나면 바뀌므로 캐시하지 않고 매번 본다
type resolvedKey struct {
	key         Key
	role        core.Role // 키 소유자의 역할. 사용자 행이 없으면 빈 문자열(litellm 의 role=None)
	teamBlocked bool
	teamModels  []string
}

// KeyCache 는 키 검증 결과 캐시다. 키·팀·사용자를 바꾸는 쪽이 그 자리에서 지운다
type KeyCache struct {
	entries *keyCache[resolvedKey]
}

// NewKeyCache 의 TTL 은 litellm 기본(DEFAULT_MANAGEMENT_OBJECT_IN_MEMORY_CACHE_TTL 60초)과 같게 쓴다
func NewKeyCache(ttl time.Duration, now func() time.Time) *KeyCache {
	return &KeyCache{entries: newKeyCache[resolvedKey](ttl, now)}
}

func (c *KeyCache) forget(tokens ...string) { c.entries.remove(tokens...) }

// forgetTeam 은 팀을 막거나 지울 때 그 팀 키를 모두 지운다
func (c *KeyCache) forgetTeam(teamIDs ...string) {
	c.entries.removeWhere(func(r resolvedKey) bool {
		return r.key.TeamID != nil && slices.Contains(teamIDs, *r.key.TeamID)
	})
}

// forgetUser 는 사용자 역할이 바뀌거나 지울 때 그 사용자 키를 모두 지운다
func (c *KeyCache) forgetUser(userIDs ...string) {
	c.entries.removeWhere(func(r resolvedKey) bool {
		return r.key.UserID != nil && slices.Contains(userIDs, *r.key.UserID)
	})
}
