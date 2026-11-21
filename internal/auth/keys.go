package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"llm-gateway/internal/core"
)

// Key 는 가상 키 행이다. Token 은 sha256 hex 이고 원문은 어디에도 없다
type Key struct {
	Token         string
	KeyName       *string
	KeyAlias      *string
	UserID        *string
	TeamID        *string
	Models        []string
	Metadata      map[string]any
	KeyType       string
	AllowedRoutes []string
	Blocked       *bool
	Expires       *time.Time
	CreatedBy     *string
	UpdatedBy     *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DeletedKey 는 지운 키의 사본이다
type DeletedKey struct {
	Key
	DeletedAt time.Time
	DeletedBy *string
}

type NewKey struct {
	Key           *string // 사용자 지정 원문. nil 이면 만든다
	KeyAlias      *string
	UserID        *string
	TeamID        *string
	Models        []string
	Metadata      map[string]any
	KeyType       string
	AllowedRoutes []string
	Blocked       *bool
	Expires       *time.Time
	CreatedBy     string
}

type KeyPatch struct {
	KeyAlias  core.Change[string]
	UserID    core.Change[string]
	TeamID    core.Change[string]
	Models    core.Change[[]string]
	Metadata  core.Change[map[string]any]
	Blocked   core.Change[bool]
	Expires   core.Change[time.Time]
	UpdatedBy string
}

// KeyQuery 는 /key/list 의 조건이다. Visible 이 있으면 비관리자 범위(OR)로 좁힌다
type KeyQuery struct {
	Visible        *KeyVisibility
	UserID         *string
	UserIDContains bool
	TeamID         *string
	KeyHash        *string
	KeyAlias       *string
	AliasContains  bool
	Search         *string
	Status         string // active · expired · revoked · "" (deleted 는 ListDeleted)
	SortBy         string // spend 는 M3 전까지 created_at 으로 대신한다
	SortOrder      SortOrder
	Page, PageSize int
	Now            time.Time
}

// KeyVisibility 는 비관리자가 볼 수 있는 키다(litellm _build_key_filter_conditions): 본인 키, 본인이 만든 키(팀 없음 또는 현재 소속 팀),
// admin 인 팀의 모든 키, 멤버인 팀의 user_id 없는 키(서비스 계정)
type KeyVisibility struct {
	UserID       string
	CreatedBy    bool
	MemberTeams  []string
	AdminTeams   []string
	IncludeTeams bool
}

var (
	ErrKeyNotFound   = errors.New("키가 없다")
	ErrKeyAliasTaken = errors.New("이미 있는 key_alias 다")
)

// InvalidCustomKeyError 는 사용자 지정 키 형식 오류다. Message 는 litellm 문구다
type InvalidCustomKeyError struct{ Message string }

func (e *InvalidCustomKeyError) Error() string { return e.Message }

// litellm MINIMUM_CUSTOM_KEY_LENGTH
const minimumCustomKeyLength = 16

// Keys 는 가상 키 관리다
type Keys struct {
	db    *core.DB
	cache *KeyCache
}

func NewKeys(db *core.DB, cache *KeyCache) *Keys { return &Keys{db: db, cache: cache} }

const keyColumns = "token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes, blocked, expires, created_by, updated_by, created_at, updated_at"

func scanKey(row pgx.Row) (Key, error) {
	var k Key
	err := row.Scan(&k.Token, &k.KeyName, &k.KeyAlias, &k.UserID, &k.TeamID, &k.Models, &k.Metadata, &k.KeyType,
		&k.AllowedRoutes, &k.Blocked, &k.Expires, &k.CreatedBy, &k.UpdatedBy, &k.CreatedAt, &k.UpdatedAt)
	return k, err
}

// Generate 는 키를 만들고 원문을 돌려준다. 원문은 이 응답에서만 한 번 나간다.
// litellm 처럼 같은 사용자 지정 원문으로 다시 만들면 오류 없이 기존 행을 돌려준다.
// 실패: *InvalidCustomKeyError, ErrKeyAliasTaken, ErrTeamNotFound
func (k *Keys) Generate(ctx context.Context, input NewKey) (string, Key, error) {
	plain := generateKey()
	if input.Key != nil {
		if err := validateCustomKey(*input.Key); err != nil {
			return "", Key{}, err
		}
		plain = *input.Key
	}
	token := hashKey(plain)
	var created Key
	err := k.db.InTx(ctx, func(ctx context.Context) error {
		if input.KeyAlias != nil {
			if err := k.checkAliasFree(ctx, *input.KeyAlias, ""); err != nil {
				return err
			}
		}
		if input.TeamID != nil {
			var exists bool
			if err := k.db.Q(ctx).QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM team WHERE team_id = $1)", *input.TeamID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrTeamNotFound
			}
		}
		keyType := input.KeyType
		if keyType == "" {
			keyType = "default"
		}
		_, err := k.db.Q(ctx).Exec(ctx, `
			INSERT INTO api_key (token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes, blocked, expires, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
			ON CONFLICT (token) DO NOTHING`,
			token, keyName(plain), input.KeyAlias, input.UserID, input.TeamID, orEmpty(input.Models), orEmptyMap(input.Metadata),
			keyType, orEmpty(input.AllowedRoutes), input.Blocked, input.Expires, input.CreatedBy)
		if err != nil {
			return err
		}
		created, err = k.Find(ctx, token)
		return err
	})
	return plain, created, err
}

// Find 는 해시로 키를 찾는다. 없으면 ErrKeyNotFound
func (k *Keys) Find(ctx context.Context, token string) (Key, error) {
	key, err := scanKey(k.db.Q(ctx).QueryRow(ctx, "SELECT "+keyColumns+" FROM api_key WHERE token = $1", token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrKeyNotFound
	}
	return key, err
}

// FindByAlias 는 alias 로 찾는다. alias 는 DB 에서 고유하다
func (k *Keys) FindByAlias(ctx context.Context, alias string) (Key, error) {
	key, err := scanKey(k.db.Q(ctx).QueryRow(ctx, "SELECT "+keyColumns+" FROM api_key WHERE key_alias = $1", alias))
	if errors.Is(err, pgx.ErrNoRows) {
		return Key{}, ErrKeyNotFound
	}
	return key, err
}

// FindAll 은 해시 목록 중 있는 키다
func (k *Keys) FindAll(ctx context.Context, tokens []string) ([]Key, error) {
	return k.list(ctx, "SELECT "+keyColumns+" FROM api_key WHERE token = ANY($1)", tokens)
}

// FindDeleted 는 지운 키의 가장 최근 사본이다. 없으면 ErrKeyNotFound
func (k *Keys) FindDeleted(ctx context.Context, token string) (DeletedKey, error) {
	var d DeletedKey
	err := k.db.Q(ctx).QueryRow(ctx, "SELECT "+keyColumns+", deleted_at, deleted_by FROM deleted_api_key WHERE token = $1 ORDER BY deleted_at DESC LIMIT 1", token).
		Scan(&d.Token, &d.KeyName, &d.KeyAlias, &d.UserID, &d.TeamID, &d.Models, &d.Metadata, &d.KeyType, &d.AllowedRoutes,
			&d.Blocked, &d.Expires, &d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt, &d.DeletedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return DeletedKey{}, ErrKeyNotFound
	}
	return d, err
}

// Update 는 보낸 필드만 바꾸고 캐시에서 지운다. 실패: ErrKeyNotFound, ErrKeyAliasTaken, ErrTeamNotFound
func (k *Keys) Update(ctx context.Context, token string, patch KeyPatch) (Key, error) {
	var updated Key
	err := k.db.InTx(ctx, func(ctx context.Context) error {
		if patch.KeyAlias.Value != nil {
			if err := k.checkAliasFree(ctx, *patch.KeyAlias.Value, token); err != nil {
				return err
			}
		}
		sets := []string{"updated_at = now()", "updated_by = $2"}
		args := []any{token, patch.UpdatedBy}
		set := func(column string, value any) {
			args = append(args, value)
			sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)))
		}
		if patch.KeyAlias.Set {
			set("key_alias", patch.KeyAlias.Value)
		}
		if patch.UserID.Set {
			set("user_id", patch.UserID.Value)
		}
		if patch.TeamID.Set {
			set("team_id", patch.TeamID.Value)
		}
		if patch.Models.Set {
			set("models", patch.Models.Or([]string{}))
		}
		if patch.Metadata.Set {
			set("metadata", orEmptyMap(patch.Metadata.Or(nil)))
		}
		if patch.Blocked.Set {
			set("blocked", patch.Blocked.Value)
		}
		if patch.Expires.Set {
			set("expires", patch.Expires.Value)
		}
		key, err := scanKey(k.db.Q(ctx).QueryRow(ctx, "UPDATE api_key SET "+strings.Join(sets, ", ")+" WHERE token = $1 RETURNING "+keyColumns, args...))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrKeyNotFound
		case isForeignKeyViolation(err):
			return ErrTeamNotFound
		case err != nil:
			return err
		}
		updated = key
		return nil
	})
	if err == nil {
		k.cache.forget(token)
	}
	return updated, err
}

// Delete 는 키를 사본으로 옮긴 뒤 지운다. 지운 해시를 돌려준다
func (k *Keys) Delete(ctx context.Context, tokens []string, deletedBy string) ([]string, error) {
	var deleted []string
	err := k.db.InTx(ctx, func(ctx context.Context) error {
		var err error
		deleted, err = archiveAndDelete(ctx, k.db.Q(ctx), "token = ANY($1)", deletedBy, tokens)
		return err
	})
	if err == nil {
		k.cache.forget(deleted...)
	}
	return deleted, err
}

// archiveAndDelete 는 조건에 맞는 키를 deleted_api_key 로 옮기고 지운다. 팀 삭제·팀원 제거에서도 쓴다
func archiveAndDelete(ctx context.Context, q core.Querier, where string, deletedBy string, args ...any) ([]string, error) {
	params := append([]any{}, args...)
	params = append(params, deletedBy)
	byArg := fmt.Sprintf("$%d", len(params))
	rows, err := q.Query(ctx, `
		WITH gone AS (DELETE FROM api_key WHERE `+where+` RETURNING *)
		INSERT INTO deleted_api_key (id, token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes,
		                             blocked, expires, created_by, updated_by, created_at, updated_at, deleted_by)
		SELECT gen_random_uuid()::text, token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes,
		       blocked, expires, created_by, updated_by, created_at, updated_at, `+byArg+` FROM gone
		RETURNING token`, params...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Regenerate 는 키의 원문을 새로 만든다(J 3.9). 같은 행의 token 을 새 해시로 바꾸므로 사용자·팀·설정은 이어진다.
// litellm 처럼 옛 행은 deleted_api_key 에 사본으로 남긴다. 옛 원문은 바로 쓸 수 없게 된다. 실패: ErrKeyNotFound, *InvalidCustomKeyError
func (k *Keys) Regenerate(ctx context.Context, token string, newKey *string, by string) (string, Key, error) {
	plain := generateKey()
	if newKey != nil {
		if err := validateCustomKey(*newKey); err != nil {
			return "", Key{}, err
		}
		plain = *newKey
	}
	var regenerated Key
	err := k.db.InTx(ctx, func(ctx context.Context) error {
		tag, err := k.db.Q(ctx).Exec(ctx, `
			INSERT INTO deleted_api_key (id, token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes,
			                             blocked, expires, created_by, updated_by, created_at, updated_at, deleted_by)
			SELECT gen_random_uuid()::text, token, key_name, key_alias, user_id, team_id, models, metadata, key_type, allowed_routes,
			       blocked, expires, created_by, updated_by, created_at, updated_at, $2 FROM api_key WHERE token = $1`, token, by)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrKeyNotFound
		}
		regenerated, err = scanKey(k.db.Q(ctx).QueryRow(ctx, `
			UPDATE api_key SET token = $2, key_name = $3, updated_by = $4, updated_at = now() WHERE token = $1 RETURNING `+keyColumns,
			token, hashKey(plain), keyName(plain), by))
		return err
	})
	if err == nil {
		k.cache.forget(token)
	}
	return plain, regenerated, err
}

// SetBlocked 는 차단을 바꾸고 캐시에서 지운다
func (k *Keys) SetBlocked(ctx context.Context, token string, blocked bool, by string) (Key, error) {
	return k.Update(ctx, token, KeyPatch{Blocked: core.To(blocked), UpdatedBy: by})
}

// Page 는 /key/list 의 한 쪽이다
func (k *Keys) Page(ctx context.Context, q KeyQuery) (core.Page[Key], error) {
	lq := keyFilter(q)
	var total int64
	if err := k.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM api_key "+lq.Clause(), lq.Args...).Scan(&total); err != nil {
		return core.Page[Key]{}, err
	}
	sql := "SELECT " + keyColumns + " FROM api_key " + lq.Clause() +
		keyOrder(q.SortBy, q.SortOrder) + lq.Page(q.Page, q.PageSize)
	items, err := k.list(ctx, sql, lq.Args...)
	return core.Page[Key]{Items: items, Total: total}, err
}

// PageDeleted 는 /key/list?status=deleted 다
func (k *Keys) PageDeleted(ctx context.Context, q KeyQuery) (core.Page[DeletedKey], error) {
	q.Status = ""
	lq := keyFilter(q)
	var total int64
	if err := k.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM deleted_api_key "+lq.Clause(), lq.Args...).Scan(&total); err != nil {
		return core.Page[DeletedKey]{}, err
	}
	rows, err := k.db.Q(ctx).Query(ctx, "SELECT "+keyColumns+", deleted_at, deleted_by FROM deleted_api_key "+lq.Clause()+
		" ORDER BY deleted_at DESC, token"+lq.Page(q.Page, q.PageSize), lq.Args...)
	if err != nil {
		return core.Page[DeletedKey]{}, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeletedKey, error) {
		var d DeletedKey
		err := row.Scan(&d.Token, &d.KeyName, &d.KeyAlias, &d.UserID, &d.TeamID, &d.Models, &d.Metadata, &d.KeyType, &d.AllowedRoutes,
			&d.Blocked, &d.Expires, &d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt, &d.DeletedBy)
		return d, err
	})
	return core.Page[DeletedKey]{Items: items, Total: total}, err
}

// Aliases 는 /key/aliases 다. alias 오름차순, 빈 alias 는 뺀다
func (k *Keys) Aliases(ctx context.Context, visible *KeyVisibility, search, teamID *string, page, size int) (core.Page[string], error) {
	var lq core.ListQuery
	lq.Where("key_alias IS NOT NULL AND key_alias <> ''")
	lq.Where("(team_id IS NULL OR team_id <> ?)", dashboardTeamID)
	if visible != nil {
		lq.Where("(user_id = ? OR team_id = ANY(?))", visible.UserID, visible.MemberTeams)
	}
	if search != nil {
		lq.Where("key_alias ILIKE ?", core.LikeContains(*search))
	}
	if teamID != nil {
		lq.Where("team_id = ?", *teamID)
	}
	var total int64
	if err := k.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM api_key "+lq.Clause(), lq.Args...).Scan(&total); err != nil {
		return core.Page[string]{}, err
	}
	rows, err := k.db.Q(ctx).Query(ctx, "SELECT key_alias FROM api_key "+lq.Clause()+" ORDER BY key_alias"+lq.Page(page, size), lq.Args...)
	if err != nil {
		return core.Page[string]{}, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return core.Page[string]{Items: items, Total: total}, err
}

func keyFilter(q KeyQuery) *core.ListQuery {
	lq := &core.ListQuery{}
	// 대시보드 세션 키(team_id=litellm-dashboard)는 목록에서 늘 뺀다
	lq.Where("(team_id IS NULL OR team_id <> ?)", dashboardTeamID)
	if v := q.Visible; v != nil {
		conditions := []string{"user_id = ?"}
		args := []any{v.UserID}
		if v.CreatedBy {
			conditions = append(conditions, "(created_by = ? AND (team_id IS NULL OR team_id = ANY(?)))")
			args = append(args, v.UserID, v.MemberTeams)
		}
		if v.IncludeTeams {
			conditions = append(conditions, "team_id = ANY(?)", "(user_id IS NULL AND team_id = ANY(?))")
			args = append(args, v.AdminTeams, v.MemberTeams)
		}
		lq.Where("("+strings.Join(conditions, " OR ")+")", args...)
	}
	if q.UserID != nil {
		if q.UserIDContains {
			lq.Where("user_id ILIKE ?", core.LikeContains(*q.UserID))
		} else {
			lq.Where("user_id = ?", *q.UserID)
		}
	}
	if q.TeamID != nil {
		lq.Where("team_id = ?", *q.TeamID)
	}
	if q.KeyHash != nil {
		lq.Where("token = ?", *q.KeyHash)
	}
	if q.KeyAlias != nil {
		if q.AliasContains {
			lq.Where("key_alias ILIKE ?", core.LikeContains(*q.KeyAlias))
		} else {
			lq.Where("key_alias = ?", *q.KeyAlias)
		}
	}
	if q.Search != nil {
		lq.Where("(token = ? OR key_alias ILIKE ?)", *q.Search, core.LikeContains(*q.Search))
	}
	switch q.Status {
	case "revoked":
		lq.Where("blocked IS TRUE")
	case "expired":
		lq.Where("blocked IS NOT TRUE AND expires < ?", q.Now)
	case "active":
		lq.Where("blocked IS NOT TRUE AND (expires IS NULL OR expires >= ?)", q.Now)
	}
	return lq
}

// keyOrder 는 litellm /key/list 정렬이다. 기본은 created_at 최신순. spend·max_budget 은 limits 값이라 M3 전까지 created_at 으로 대신한다
func keyOrder(sortBy string, order SortOrder) string {
	dir := "DESC"
	if order == SortAsc {
		dir = "ASC"
	}
	switch sortBy {
	case "created_at", "updated_at", "token", "key_alias":
		return " ORDER BY " + sortBy + " " + dir + ", token " + dir
	case "spend", "max_budget":
		return " ORDER BY created_at " + dir + ", token " + dir
	default:
		return " ORDER BY created_at DESC, token DESC"
	}
}

func (k *Keys) checkAliasFree(ctx context.Context, alias, exceptToken string) error {
	var taken bool
	err := k.db.Q(ctx).QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM api_key WHERE key_alias = $1 AND token <> $2)", alias, exceptToken).Scan(&taken)
	if err == nil && taken {
		return ErrKeyAliasTaken
	}
	return err
}

func (k *Keys) list(ctx context.Context, sql string, args ...any) ([]Key, error) {
	rows, err := k.db.Q(ctx).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Key, error) { return scanKey(row) })
}

// generateKey 는 litellm 과 같은 모양이다: "sk-" + secrets.token_urlsafe(16) (base64url 22자, 전체 25자)
func generateKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "sk-" + base64.RawURLEncoding.EncodeToString(b)
}

func validateCustomKey(key string) error {
	if !strings.HasPrefix(key, "sk-") {
		return &InvalidCustomKeyError{Message: "Invalid key format. LiteLLM Virtual Key must start with 'sk-'. Received: " + maskKey(key)}
	}
	if len(key) < minimumCustomKeyLength {
		return &InvalidCustomKeyError{Message: "Invalid key format. LiteLLM Virtual Key must be at least 16 characters long."}
	}
	return nil
}

// keyName 은 litellm 의 표시용 이름이다: "sk-..." + 원문 끝 4자(16자 미만이면 "sk-...")
func keyName(plain string) string {
	if len(plain) < 16 {
		return "sk-..."
	}
	return "sk-..." + plain[len(plain)-4:]
}

// maskKey 는 오류 메시지에 넣을 때 앞 4자와 뒤 4자만 남긴다(litellm 과 같다)
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
