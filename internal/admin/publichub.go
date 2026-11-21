package admin

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// 공개 모델 허브(M 5장, litellm public_v1/model_hub.py). 관리자가 "Make Public" 으로 고른 모델 그룹만 보인다.
// 쿼리는 litellm 목록 규칙 그대로다: page, page_size, sort(쉼표, - 는 내림차순), q(검색), filter[mode|providers|features][in].
// 공개할 그룹과 링크는 core.Settings 에 두므로(admin/modelhub.go) 목록도 같은 곳에서 읽는다. 모델 그룹 행 자체는 inference 가 만든다

const maxHubPageSize = 100 // litellm 목록 경로의 한 쪽 상한

// publicHubRows 는 공개 그룹 가운데 쓸 수 있는 배포가 있는 것을 거른 행이다
func (h *Handlers) publicHubRows(r *http.Request) ([]row, error) {
	stored, err := h.settings.Get(r.Context(), modelHubSetting)
	if err != nil {
		return nil, err
	}
	all, err := h.deployments.Active(r.Context())
	if err != nil {
		return nil, err
	}
	byName := map[string][]inference.Deployment{}
	for _, d := range all {
		byName[d.ModelName] = append(byName[d.ModelName], d)
	}
	q := r.URL.Query()
	search := strings.ToLower(q.Get("q"))
	rows := []row{}
	for _, name := range stringsOf(stored["public_model_groups"]) {
		group, ok := byName[name]
		if !ok || (search != "" && !strings.Contains(strings.ToLower(name), search)) {
			continue
		}
		hubRow := inference.ModelGroupRow(name, group)
		if matchesIn(q.Get("filter[mode][in]"), []string{hubRow["mode"].(string)}) &&
			matchesIn(q.Get("filter[providers][in]"), hubRow["providers"].([]string)) &&
			matchesIn(q.Get("filter[features][in]"), hubFeatures(hubRow)) {
			rows = append(rows, hubRow)
		}
	}
	return rows, nil
}

func (h *Handlers) publicModelHub(w http.ResponseWriter, r *http.Request) {
	rows, err := h.publicHubRows(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	q := r.URL.Query()
	sortHubRows(rows, cmp.Or(q.Get("sort"), "model_group"))
	page, size := positiveInt(q.Get("page"), 1), min(positiveInt(q.Get("page_size"), 50), maxHubPageSize)
	total := len(rows)
	core.WriteJSON(w, http.StatusOK, row{
		"data":  pageOf(rows, (page-1)*size, size),
		"links": row{"self": r.URL.RequestURI(), "first": r.URL.RequestURI(), "last": r.URL.RequestURI(), "next": nil, "prev": nil},
		"meta":  row{"page": page, "page_size": size, "total_count": total, "total_pages": (total + size - 1) / size},
	})
}

// publicModelHubFacet 은 필터 드롭다운 값(제공자·모드·기능)이다. 목록과 같은 필터를 받는다
func (h *Handlers) publicModelHubFacet(w http.ResponseWriter, r *http.Request) {
	facet := r.PathValue("facet")
	if facet != "providers" && facet != "modes" && facet != "features" {
		core.WriteRouteNotFound(w)
		return
	}
	rows, err := h.publicHubRows(r)
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	values := []string{}
	for _, hubRow := range rows {
		var vs []string
		switch facet {
		case "providers":
			vs = hubRow["providers"].([]string)
		case "modes":
			vs = []string{hubRow["mode"].(string)}
		case "features":
			vs = hubFeatures(hubRow)
		}
		for _, v := range vs {
			if !slices.Contains(values, v) {
				values = append(values, v)
			}
		}
	}
	slices.Sort(values)
	size := min(positiveInt(r.URL.Query().Get("page_size"), 50), maxHubPageSize)
	core.WriteJSON(w, http.StatusOK, row{
		"data":  values[:min(size, len(values))],
		"links": row{"self": r.URL.RequestURI(), "next": nil, "prev": nil},
		"meta":  row{"has_more": len(values) > size, "page": 1, "page_size": size},
	})
}

// hubFeatures 는 참인 supports_* 플래그 이름(접두어 뗀 것)이다. litellm 은 이것 하나로 기능 필터를 건다
func hubFeatures(hubRow row) []string {
	var names []string
	for k, v := range hubRow {
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

// sortHubRows 는 sort 의 첫 필드로 정렬한다(화면은 한 열씩만 정렬한다). 값이 없는 행은 앞에 둔다
func sortHubRows(rows []row, sort string) {
	field, _, _ := strings.Cut(sort, ",")
	desc := strings.HasPrefix(field, "-")
	field = strings.TrimPrefix(field, "-")
	slices.SortStableFunc(rows, func(a, b row) int {
		c := compareHubCell(a[field], b[field])
		if desc {
			return -c
		}
		return c
	})
}

func compareHubCell(a, b any) int {
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

// stringsOf 는 JSON 으로 저장한 문자열 배열을 읽는다
func stringsOf(v any) []string {
	items, _ := v.([]any)
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
