// Package inference 는 추론 경로다: /v1/* 라우트, 모델 배포(model_deployment), 배포 선택, 업스트림(Ollama 등) 중계.
package inference

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"llm-gateway/internal/core"
)

// Deployment 는 모델 배포 하나다. ModelName 은 클라이언트가 부르는 이름이고 같은 이름이 여럿이면 Weight 로 나눈다
type Deployment struct {
	ID                 string
	ModelName          string
	Provider           string
	BaseURL            string
	UpstreamModel      string
	Weight             int
	MaxInputTokens     *int64
	MaxOutputTokens    *int64
	SupportsReasoning  bool
	InputCostPerToken  *float64
	OutputCostPerToken *float64
	// Active 가 거짓이면 일시정지다(litellm model_info.blocked). 라우팅에서 빠진다
	Active bool
	// 대시보드가 넣는 그 밖의 litellm_params·model_info 키. 우리는 해석하지 않고 그대로 돌려준다
	ExtraParams map[string]any
	ExtraInfo   map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewDeployment 는 등록할 배포다. ID 가 nil 이면 만든다
type NewDeployment struct {
	ID                 *string
	ModelName          string
	Provider           string
	BaseURL            string
	UpstreamModel      string
	Weight             int
	MaxInputTokens     *int64
	MaxOutputTokens    *int64
	SupportsReasoning  bool
	InputCostPerToken  *float64
	OutputCostPerToken *float64
	ExtraParams        map[string]any
	ExtraInfo          map[string]any
}

var (
	ErrDeploymentNotFound = errors.New("배포가 없다")
	ErrDeploymentIDTaken  = errors.New("이미 있는 배포 id 다")
)

// Deployments 는 모델 배포 저장소다. 배포 목록은 자주 바뀌지 않지만 지금은 요청마다 읽는다(캐시는 사용량이 생기면 붙인다)
type Deployments struct {
	db *core.DB
}

func NewDeployments(db *core.DB) *Deployments { return &Deployments{db: db} }

const deploymentColumns = "id, model_name, provider, base_url, upstream_model, weight, max_input_tokens, max_output_tokens, supports_reasoning, " +
	"input_cost_per_token, output_cost_per_token, active, extra_params, extra_info, created_at, updated_at"

func scanDeployment(row pgx.Row) (Deployment, error) {
	var d Deployment
	err := row.Scan(&d.ID, &d.ModelName, &d.Provider, &d.BaseURL, &d.UpstreamModel, &d.Weight, &d.MaxInputTokens, &d.MaxOutputTokens,
		&d.SupportsReasoning, &d.InputCostPerToken, &d.OutputCostPerToken, &d.Active, &d.ExtraParams, &d.ExtraInfo, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

func (s *Deployments) Create(ctx context.Context, n NewDeployment) (Deployment, error) {
	id := newDeploymentID()
	if n.ID != nil && *n.ID != "" {
		id = *n.ID
	}
	if n.Weight <= 0 {
		n.Weight = 1
	}
	d, err := scanDeployment(s.db.Q(ctx).QueryRow(ctx, `
		INSERT INTO model_deployment (id, model_name, provider, base_url, upstream_model, weight, max_input_tokens, max_output_tokens,
		                              supports_reasoning, input_cost_per_token, output_cost_per_token, extra_params, extra_info)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO NOTHING RETURNING `+deploymentColumns,
		id, n.ModelName, n.Provider, n.BaseURL, n.UpstreamModel, n.Weight, n.MaxInputTokens, n.MaxOutputTokens,
		n.SupportsReasoning, n.InputCostPerToken, n.OutputCostPerToken, orEmptyMap(n.ExtraParams), orEmptyMap(n.ExtraInfo)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrDeploymentIDTaken
	}
	return d, err
}

// Replace 는 관리 화면에서 고친 배포를 통째로 저장한다(id·만든 시각은 그대로)
func (s *Deployments) Replace(ctx context.Context, d Deployment) (Deployment, error) {
	if d.Weight <= 0 {
		d.Weight = 1
	}
	updated, err := scanDeployment(s.db.Q(ctx).QueryRow(ctx, `
		UPDATE model_deployment SET model_name = $2, provider = $3, base_url = $4, upstream_model = $5, weight = $6, max_input_tokens = $7,
		       max_output_tokens = $8, supports_reasoning = $9, input_cost_per_token = $10, output_cost_per_token = $11, active = $12,
		       extra_params = $13, extra_info = $14, updated_at = now()
		WHERE id = $1 RETURNING `+deploymentColumns,
		d.ID, d.ModelName, d.Provider, d.BaseURL, d.UpstreamModel, d.Weight, d.MaxInputTokens, d.MaxOutputTokens, d.SupportsReasoning,
		d.InputCostPerToken, d.OutputCostPerToken, d.Active, orEmptyMap(d.ExtraParams), orEmptyMap(d.ExtraInfo)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrDeploymentNotFound
	}
	return updated, err
}

// Active 는 쓸 수 있는(일시정지하지 않은) 배포 전부다(모델 이름 순)
func (s *Deployments) Active(ctx context.Context) ([]Deployment, error) {
	return s.list(ctx, "WHERE active")
}

// All 은 일시정지한 것까지 모든 배포다(관리 화면)
func (s *Deployments) All(ctx context.Context) ([]Deployment, error) {
	return s.list(ctx, "")
}

func (s *Deployments) Find(ctx context.Context, id string) (Deployment, error) {
	d, err := scanDeployment(s.db.Q(ctx).QueryRow(ctx, "SELECT "+deploymentColumns+" FROM model_deployment WHERE id = $1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrDeploymentNotFound
	}
	return d, err
}

func (s *Deployments) list(ctx context.Context, where string) ([]Deployment, error) {
	rows, err := s.db.Q(ctx).Query(ctx, "SELECT "+deploymentColumns+" FROM model_deployment "+where+" ORDER BY model_name, created_at, id")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Deployment, error) { return scanDeployment(row) })
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (s *Deployments) Delete(ctx context.Context, id string) error {
	tag, err := s.db.Q(ctx).Exec(ctx, "DELETE FROM model_deployment WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrDeploymentNotFound
	}
	return err
}

// pick 은 같은 모델 이름의 배포 가운데 weight 비율로 하나를 고른다(litellm 기본 simple-shuffle 과 같다)
func pick(candidates []Deployment) Deployment {
	total := 0
	for _, d := range candidates {
		total += d.Weight
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(total)))
	r := int(n.Int64())
	for _, d := range candidates {
		if r < d.Weight {
			return d
		}
		r -= d.Weight
	}
	return candidates[len(candidates)-1]
}

// endpoint 는 업스트림 OpenAI 호환 주소다(path 예: chat/completions). api_base 가 /v1 로 끝나면(OpenAI 호환 서버의 흔한 모양) 그 뒤에,
// 아니면 /v1 을 붙인다. Ollama 도 /v1 로 보낸다(설계 5장: 네이티브 /api/chat 은 쓰지 않는다)
func (d Deployment) endpoint(path string) string {
	base := strings.TrimRight(d.BaseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/" + path
	}
	return base + "/v1/" + path
}

func newDeploymentID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
