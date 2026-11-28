package inference

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"llm-gateway/internal/core"
)

// GuardrailRecord 는 저장된 가드레일 한 줄이다. Params 는 대시보드가 보낸 litellm_params 를 그대로 가진다
type GuardrailRecord struct {
	ID        string
	Name      string
	Params    map[string]any
	Info      map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	ErrGuardrailNotFound  = errors.New("가드레일이 없다")
	ErrGuardrailNameTaken = errors.New("이미 있는 가드레일 이름이다")
)

// guardrailCacheTTL 은 다른 인스턴스가 바꾼 가드레일이 반영되기까지의 최대 시간이다. 이 인스턴스에서 바꾼 것은 바로 반영된다
const guardrailCacheTTL = 5 * time.Second

// Guardrails 는 가드레일 저장소다. 요청마다 쓰이므로 검사 규칙으로 만든 결과를 잠깐 기억한다
type Guardrails struct {
	db      *core.DB
	now     func() time.Time
	onBlock func(guardrail, mode string)

	mu      sync.Mutex
	cached  []*Guardrail
	expires time.Time
}

func NewGuardrails(db *core.DB, now func() time.Time) *Guardrails {
	return &Guardrails{db: db, now: now}
}

// OnBlock 은 가드레일이 요청이나 응답을 막을 때마다 부를 함수를 정한다(지표용). 요청을 받기 전에 한 번 정한다
func (s *Guardrails) OnBlock(f func(guardrail, mode string)) { s.onBlock = f }

func (s *Guardrails) blocked(guardrail, mode string) {
	if s.onBlock != nil {
		s.onBlock(guardrail, mode)
	}
}

const guardrailColumns = "guardrail_id, guardrail_name, litellm_params, guardrail_info, created_at, updated_at"

func scanGuardrail(row pgx.Row) (GuardrailRecord, error) {
	var g GuardrailRecord
	err := row.Scan(&g.ID, &g.Name, &g.Params, &g.Info, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func (s *Guardrails) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = nil
}

// Create 는 설정을 검사한 뒤 저장한다. 설정이 잘못되면 ConfigError, 이름이 겹치면 ErrGuardrailNameTaken 이다
func (s *Guardrails) Create(ctx context.Context, name string, params, info map[string]any) (GuardrailRecord, error) {
	id := core.NewUUID()
	if _, err := ParseGuardrail(id, name, params); err != nil {
		return GuardrailRecord{}, err
	}
	g, err := scanGuardrail(s.db.Q(ctx).QueryRow(ctx,
		"INSERT INTO guardrail (guardrail_id, guardrail_name, litellm_params, guardrail_info) VALUES ($1, $2, $3, $4) RETURNING "+guardrailColumns,
		id, name, params, orEmptyMap(info)))
	if isUniqueViolation(err) {
		return GuardrailRecord{}, ErrGuardrailNameTaken
	}
	s.forget()
	return g, err
}

// Replace 는 이름·설정·정보를 통째로 바꾼다(id·만든 시각은 그대로). 설정은 저장 전에 검사한다
func (s *Guardrails) Replace(ctx context.Context, id, name string, params, info map[string]any) (GuardrailRecord, error) {
	if _, err := ParseGuardrail(id, name, params); err != nil {
		return GuardrailRecord{}, err
	}
	g, err := scanGuardrail(s.db.Q(ctx).QueryRow(ctx, `
		UPDATE guardrail SET guardrail_name = $2, litellm_params = $3, guardrail_info = $4, updated_at = now()
		WHERE guardrail_id = $1 RETURNING `+guardrailColumns, id, name, params, orEmptyMap(info)))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return GuardrailRecord{}, ErrGuardrailNotFound
	case isUniqueViolation(err):
		return GuardrailRecord{}, ErrGuardrailNameTaken
	}
	s.forget()
	return g, err
}

func (s *Guardrails) Find(ctx context.Context, id string) (GuardrailRecord, error) {
	g, err := scanGuardrail(s.db.Q(ctx).QueryRow(ctx, "SELECT "+guardrailColumns+" FROM guardrail WHERE guardrail_id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return GuardrailRecord{}, ErrGuardrailNotFound
	}
	return g, err
}

func (s *Guardrails) List(ctx context.Context) ([]GuardrailRecord, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+guardrailColumns+" FROM guardrail ORDER BY guardrail_name")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (GuardrailRecord, error) { return scanGuardrail(row) })
}

func (s *Guardrails) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM guardrail WHERE guardrail_id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrGuardrailNotFound
	}
	s.forget()
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// GuardrailChoice 는 요청 하나가 어떤 가드레일을 원하는지다. 호출자의 키·팀 설정과 요청 본문에서 모은다
type GuardrailChoice struct {
	Requested     []string // 이름으로 지정한 가드레일(키·팀에 붙였거나 요청이 더한 것)
	DisableGlobal bool     // 모든 요청에 거는(default_on) 가드레일을 이 호출자에게는 끈다
	OptedOut      []string // 이 이름의 default_on 가드레일만 끈다
}

// For 는 이 요청에 적용할 가드레일이다(이름순). default_on 인 것은 끄지 않았다면 모두, 나머지는 이름이 지정됐을 때만이다.
// 저장된 설정을 규칙으로 만들지 못하면 오류다: 검사를 건너뛰고 통과시키지 않는다
func (s *Guardrails) For(ctx context.Context, c GuardrailChoice) ([]*Guardrail, error) {
	all, err := s.compiled(ctx)
	if err != nil || len(all) == 0 {
		return nil, err
	}
	var chosen []*Guardrail
	for _, g := range all {
		global := g.DefaultOn && !c.DisableGlobal && !slices.Contains(c.OptedOut, g.Name)
		if global || slices.Contains(c.Requested, g.Name) {
			chosen = append(chosen, g)
		}
	}
	return chosen, nil
}

func (s *Guardrails) compiled(ctx context.Context) ([]*Guardrail, error) {
	s.mu.Lock()
	if s.cached != nil && s.now().Before(s.expires) {
		defer s.mu.Unlock()
		return s.cached, nil
	}
	s.mu.Unlock()
	records, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	built := make([]*Guardrail, 0, len(records))
	for _, r := range records {
		g, err := ParseGuardrail(r.ID, r.Name, r.Params)
		if err != nil {
			return nil, errors.New("가드레일 " + r.Name + " 의 저장된 설정이 잘못됐다: " + err.Error())
		}
		built = append(built, g)
	}
	s.mu.Lock()
	s.cached, s.expires = built, s.now().Add(guardrailCacheTTL)
	s.mu.Unlock()
	return built, nil
}
