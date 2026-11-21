package inference

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"llm-gateway/internal/core"
)

// 공개 모델 허브(M 5장, litellm public_v1/model_hub.py). 관리자가 "Make Public" 으로 고른 모델 그룹만 보인다.
// 쿼리는 litellm 목록 규칙 그대로다: page, page_size, sort(쉼표, - 는 내림차순), q(검색), filter[mode|providers|features][in]

// ModelHubSetting 은 공개 그룹과 링크를 담는 core.Settings 이름이다(admin 이 쓴다)
const ModelHubSetting = "model_hub"

const maxHubPageSize = 100 // litellm 목록 경로의 한 쪽 상한

// publicRows 는 공개 그룹 가운데 쓸 수 있는 배포가 있는 것을 거른 행이다
func (h *Handlers) publicRows(r *http.Request) ([]map[string]any, error) {
	stored, err := h.settings.Get(r.Context(), ModelHubSetting)
	if err != nil {
		return nil, err
	}
	all, err := h.deployments.Active(r.Context())
	if err != nil {
		return nil, err
	}
	byName := map[string][]Deployment{}
	for _, d := range all {
		byName[d.ModelName] = append(byName[d.ModelName], d)
	}
	q := r.URL.Query()
	search := strings.ToLower(q.Get("q"))
	rows := []map[string]any{}
	for _, name := range stringList(stored["public_model_groups"]) {
		group, ok := byName[name]
		if !ok || (search != "" && !strings.Contains(strings.ToLower(name), search)) {
			continue
		}
		row := groupRow(name, group)
		if matchesIn(q.Get("filter[mode][in]"), []string{row["mode"].(string)}) &&
			matchesIn(q.Get("filter[providers][in]"), row["providers"].([]string)) &&
			matchesIn(q.Get("filter[features][in]"), features(row)) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (h *Handlers) publicModelHub(w http.ResponseWriter, r *http.Request) {
	rows, err := h.publicRows(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	q := r.URL.Query()
	sortRows(rows, cmp.Or(q.Get("sort"), "model_group"))
	page, size := pageParam(q.Get("page"), 1), min(pageParam(q.Get("page_size"), 50), maxHubPageSize)
	total := len(rows)
	start := min((page-1)*size, total)
	core.WriteJSON(w, http.StatusOK, map[string]any{
		"data":  rows[start:min(start+size, total)],
		"links": map[string]any{"self": r.URL.RequestURI(), "first": r.URL.RequestURI(), "last": r.URL.RequestURI(), "next": nil, "prev": nil},
		"meta":  map[string]any{"page": page, "page_size": size, "total_count": total, "total_pages": (total + size - 1) / size},
	})
}

// publicModelHubFacet 은 필터 드롭다운 값(제공자·모드·기능)이다. 목록과 같은 필터를 받는다
func (h *Handlers) publicModelHubFacet(w http.ResponseWriter, r *http.Request) {
	facet := r.PathValue("facet")
	if facet != "providers" && facet != "modes" && facet != "features" {
		core.WriteRouteNotFound(w)
		return
	}
	rows, err := h.publicRows(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	values := []string{}
	for _, row := range rows {
		var vs []string
		switch facet {
		case "providers":
			vs = row["providers"].([]string)
		case "modes":
			vs = []string{row["mode"].(string)}
		case "features":
			vs = features(row)
		}
		for _, v := range vs {
			if !slices.Contains(values, v) {
				values = append(values, v)
			}
		}
	}
	slices.Sort(values)
	size := min(pageParam(r.URL.Query().Get("page_size"), 50), maxHubPageSize)
	core.WriteJSON(w, http.StatusOK, map[string]any{
		"data":  values[:min(size, len(values))],
		"links": map[string]any{"self": r.URL.RequestURI(), "next": nil, "prev": nil},
		"meta":  map[string]any{"has_more": len(values) > size, "page": 1, "page_size": size},
	})
}

// features 는 참인 supports_* 플래그 이름(접두어 뗀 것)이다. litellm 은 이것 하나로 기능 필터를 건다
func features(row map[string]any) []string {
	var names []string
	for k, v := range row {
		if name, ok := strings.CutPrefix(k, "supports_"); ok && v == true {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func matchesIn(filter string, values []string) bool {
	if filter == "" {
		return true
	}
	for _, want := range strings.Split(filter, ",") {
		if slices.Contains(values, want) {
			return true
		}
	}
	return false
}

// sortRows 는 sort 의 첫 필드로 정렬한다(화면은 한 열씩만 정렬한다). 값이 없는 행은 앞에 둔다
func sortRows(rows []map[string]any, sort string) {
	field, _, _ := strings.Cut(sort, ",")
	desc := strings.HasPrefix(field, "-")
	field = strings.TrimPrefix(field, "-")
	slices.SortStableFunc(rows, func(a, b map[string]any) int {
		c := compareCell(a[field], b[field])
		if desc {
			return -c
		}
		return c
	})
}

func compareCell(a, b any) int {
	key := func(v any) (float64, string, bool) {
		switch x := v.(type) {
		case string:
			return 0, x, true
		case []string:
			return 0, strings.Join(x, ","), true
		case *int64:
			if x != nil {
				return float64(*x), "", true
			}
		case *float64:
			if x != nil {
				return *x, "", true
			}
		}
		return 0, "", false
	}
	an, as, aok := key(a)
	bn, bs, bok := key(b)
	if aok != bok {
		if !aok {
			return -1
		}
		return 1
	}
	return cmp.Or(cmp.Compare(an, bn), cmp.Compare(as, bs))
}

func stringList(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func pageParam(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}
