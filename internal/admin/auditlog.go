package admin

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/auth"
	"llm-gateway/internal/core"
)

// 관리 변경 기록(설계 8.1 audit_log). litellm 은 이 기록이 유료 기능이라 비어 있지만 운영자에게는 누가 키·팀·사용자를 바꿨는지가 필요하다.
// 핸들러마다 고치지 않도록 변경 라우트를 audited 로 감싼다. 응답이 성공일 때만 남긴다

// litellm LitellmTableNames. 대시보드 감사 화면의 테이블 필터 값이다
const (
	auditKeyTable  = "LiteLLM_VerificationToken"
	auditTeamTable = "LiteLLM_TeamTable"
	auditUserTable = "LiteLLM_UserTable"
)

// auditSecretFields 는 기록에서 뺄 요청 필드다(키 원문·비밀번호)
var auditSecretFields = []string{"key", "new_key", "password", "api_key", "secret"}

// auditTargets 는 한 번의 요청이 바꾼 대상 id 들이다. req·resp 는 요청·응답 본문(JSON 객체)이다
type auditTargets func(r *http.Request, req, resp map[string]any) []string

// audited 는 next 가 성공하면(2xx) 대상마다 감사 기록 한 줄을 남긴다. 기록 실패는 요청을 실패시키지 않고 로그만 남긴다
func (h *Handlers) audited(action, table string, targets auditTargets, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
		if err != nil {
			next(w, r) // 본문이 너무 크면 핸들러가 자기 오류로 답한다
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		captured := &capturedResponse{ResponseWriter: w, status: http.StatusOK}
		next(captured, r)
		if captured.status >= 300 {
			return
		}
		var req, resp map[string]any
		_ = json.Unmarshal(data, &req)                   // 본문이 객체가 아니면 핸들러가 이미 거절했다
		_ = json.Unmarshal(captured.body.Bytes(), &resp) // 응답이 객체가 아니면(목록 등) 대상은 요청에서만 찾는다
		for _, id := range targets(r, req, resp) {
			if err := h.audit.record(r.Context(), principal(r.Context()), action, table, id, scrubbed(req)); err != nil {
				h.logger.Error("감사 기록 실패", "action", action, "table", table, "object_id", id, "err", err)
			}
		}
	}
}

// capturedResponse 는 응답 상태와 본문을 함께 담아 둔다. 감사 대상 id 를 응답에서도 찾기 위해서다
type capturedResponse struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capturedResponse) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturedResponse) Write(b []byte) (int, error) {
	if c.body.Len() < 1<<20 { // 대상 id 만 찾으면 되므로 앞부분이면 된다
		c.body.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func scrubbed(req map[string]any) map[string]any {
	if req == nil {
		return nil
	}
	clean := map[string]any{}
	for name, value := range req {
		if !slices.Contains(auditSecretFields, name) {
			clean[name] = value
		}
	}
	return clean
}

// idsFrom 은 요청 다음 응답에서 첫 번째로 값이 있는 필드(문자열 또는 문자열 목록)의 id 들이다
func idsFrom(fields ...string) auditTargets {
	return func(_ *http.Request, req, resp map[string]any) []string {
		for _, source := range []map[string]any{req, resp} {
			for _, field := range fields {
				if ids := auditStrings(source[field]); len(ids) > 0 {
					return ids
				}
			}
		}
		return nil
	}
}

// keyTargets 는 키 원문이나 해시를 저장 해시로 바꾼 id 들이다. 경로·요청의 key, keys 가 먼저고 없으면 응답의 해시다.
// 별명으로만 지정한 요청은 별명을 그대로 id 로 쓴다
func keyTargets(r *http.Request, req, resp map[string]any) []string {
	var ids []string
	for _, v := range append(auditStrings(r.PathValue("key")), append(auditStrings(req["key"]), auditStrings(req["keys"])...)...) {
		ids = append(ids, auth.TokenOf(v))
	}
	ids = append(ids, auditStrings(req["key_aliases"])...)
	if len(ids) == 0 {
		for _, v := range append(auditStrings(resp["token_id"]), auditStrings(resp["token"])...) {
			ids = append(ids, auth.TokenOf(v))
		}
	}
	slices.Sort(ids) // 생성 응답은 token_id 와 token 에 같은 해시를 싣는다. 한 번만 남긴다
	return slices.Compact(ids)
}

func auditStrings(value any) []string {
	switch v := value.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, auditStrings(item)...)
		}
		return out
	}
	return nil
}

// auditLog 는 audit_log 테이블이다(admin 소유)
type auditLog struct {
	db  *core.DB
	now func() time.Time
}

func (a *auditLog) record(ctx context.Context, p auth.Principal, action, table, objectID string, values map[string]any) error {
	var encoded any
	if values != nil {
		data, err := json.Marshal(values)
		if err != nil {
			return err
		}
		encoded = string(data)
	}
	_, err := a.db.Q(ctx).Exec(ctx, `
		INSERT INTO audit_log (id, updated_at, changed_by, changed_by_api_key, action, table_name, object_id, updated_values)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb)`,
		core.NewUUID(), a.now().UTC(), cmp.Or(p.UserID, "default_user_id"), p.KeyToken, action, table, objectID, encoded)
	return err
}

// auditFilter 는 GET /audit 의 조건이다. nil 은 거르지 않는다
type auditFilter struct {
	ChangedBy, ChangedByKey, Action, Table, ObjectID, Search, TeamID, KeyHash *string
	Start, End                                                                *time.Time
	SortBy                                                                    string
	Desc                                                                      bool
	Page, PageSize                                                            int
}

type auditEntry struct {
	ID, ChangedBy, ChangedByKey, Action, Table, ObjectID string
	UpdatedAt                                            time.Time
	Before, Values                                       json.RawMessage
}

// auditSortColumns 는 정렬에 쓸 수 있는 열이다. 모르는 열이면 updated_at 이다
var auditSortColumns = []string{"updated_at", "action", "table_name", "changed_by", "object_id"}

func (a *auditLog) page(ctx context.Context, f auditFilter) ([]auditEntry, int64, error) {
	var lq core.ListQuery
	for column, value := range map[string]*string{"changed_by": f.ChangedBy, "changed_by_api_key": f.ChangedByKey, "action": f.Action,
		"table_name": f.Table, "object_id": f.ObjectID} {
		if value != nil {
			lq.Where(column+" = ?", *value)
		}
	}
	if f.Start != nil {
		lq.Where("updated_at >= ?", *f.Start)
	}
	if f.End != nil {
		lq.Where("updated_at <= ?", *f.End)
	}
	if f.Search != nil {
		lq.Where("(id = ? OR changed_by = ? OR object_id = ? OR changed_by_api_key = ?)", *f.Search, *f.Search, *f.Search, *f.Search)
	}
	// litellm 은 before_value·updated_values 안의 team_id·token 을 찾는다. 우리는 대상 id 로도 찾는다
	for field, value := range map[string]*string{"team_id": f.TeamID, "token": f.KeyHash} {
		if value != nil {
			pattern := core.LikeContains(*value)
			lq.Where("(object_id = ? OR before_value->>'"+field+"' ILIKE ? OR updated_values->>'"+field+"' ILIKE ?)", *value, pattern, pattern)
		}
	}
	var total int64
	if err := a.db.Q(ctx).QueryRow(ctx, "SELECT count(*) FROM audit_log "+lq.Clause(), lq.Args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	sortBy := f.SortBy
	if !slices.Contains(auditSortColumns, sortBy) {
		sortBy = "updated_at"
	}
	dir := "ASC"
	if f.Desc {
		dir = "DESC"
	}
	rows, err := a.db.Q(ctx).Query(ctx, "SELECT "+auditColumns+" FROM audit_log "+lq.Clause()+" ORDER BY "+sortBy+" "+dir+", id"+lq.Page(f.Page, f.PageSize), lq.Args...)
	if err != nil {
		return nil, 0, err
	}
	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (auditEntry, error) { return scanAudit(row) })
	return entries, total, err
}

var errAuditNotFound = errors.New("감사 기록 없음")

func (a *auditLog) find(ctx context.Context, id string) (auditEntry, error) {
	entry, err := scanAudit(a.db.Q(ctx).QueryRow(ctx, "SELECT "+auditColumns+" FROM audit_log WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return auditEntry{}, errAuditNotFound
	}
	return entry, err
}

const auditColumns = "id, updated_at, changed_by, changed_by_api_key, action, table_name, object_id, before_value, updated_values"

func scanAudit(row pgx.Row) (auditEntry, error) {
	var e auditEntry
	err := row.Scan(&e.ID, &e.UpdatedAt, &e.ChangedBy, &e.ChangedByKey, &e.Action, &e.Table, &e.ObjectID, &e.Before, &e.Values)
	return e, err
}

func (e auditEntry) json() row {
	return row{"id": e.ID, "updated_at": e.UpdatedAt.UTC(), "changed_by": e.ChangedBy, "changed_by_api_key": e.ChangedByKey, "action": e.Action,
		"table_name": e.Table, "object_id": e.ObjectID, "before_value": nullableJSON(e.Before), "updated_values": nullableJSON(e.Values)}
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// listAudit 는 GET /audit 다(litellm enterprise audit_logging_endpoints). page_size 기본 10, 최대 100
func (h *Handlers) listAudit(w http.ResponseWriter, r *http.Request) {
	var errs validationError
	page := queryInt(r, "page", 1, 1, 0, &errs)
	size := queryInt(r, "page_size", 10, 1, 100, &errs)
	if len(errs) > 0 {
		errs.write(w)
		return
	}
	filter := auditFilter{ChangedBy: queryString(r, "changed_by"), ChangedByKey: queryString(r, "changed_by_api_key"), Action: queryString(r, "action"),
		Table: queryString(r, "table_name"), ObjectID: queryString(r, "object_id"), Search: queryString(r, "search"),
		TeamID: queryString(r, "object_team_id"), KeyHash: queryString(r, "object_key_hash"),
		SortBy: r.URL.Query().Get("sort_by"), Desc: r.URL.Query().Get("sort_order") != "asc", Page: page, PageSize: size}
	for param, target := range map[string]**time.Time{"start_date": &filter.Start, "end_date": &filter.End} {
		if raw := r.URL.Query().Get(param); raw != "" {
			parsed, ok := parseAuditTime(raw)
			if !ok {
				proxyError(400, "Invalid "+param+": "+raw).Write(w)
				return
			}
			*target = &parsed
		}
	}
	entries, total, err := h.audit.page(r.Context(), filter)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	logs := make([]row, len(entries))
	for i, e := range entries {
		logs[i] = e.json()
	}
	core.WriteJSON(w, http.StatusOK, row{"audit_logs": logs, "total": total, "page": page, "page_size": size, "total_pages": totalPages(total, size)})
}

// parseAuditTime 은 대시보드가 보내는 ISO 시각이나 "YYYY-MM-DD HH:MM:SS", 날짜만 받는다
func parseAuditTime(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", time.DateOnly} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// getAudit 는 GET /audit/{id} 다
func (h *Handlers) getAudit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entry, err := h.audit.find(r.Context(), id)
	switch {
	case errors.Is(err, errAuditNotFound):
		detailError(404, "Audit log with ID "+id+" not found").Write(w)
	case err != nil:
		h.internalError(w, r, err)
	default:
		core.WriteJSON(w, http.StatusOK, entry.json())
	}
}
