package admin

import (
	"net/http"
	"slices"

	"llm-gateway/internal/core"
	"llm-gateway/internal/inference"
)

// 모델 액세스 그룹 조회(GET /access_group/list, /access_group/{access_group}/info). 그룹은 배포의 model_info.access_groups 에서
// 나오므로 따로 저장하지 않고, 만들고 고치는 일은 모델 편집 화면이 한다. 그룹 공유 예산은 만들지 않아 budget·spend 는 늘 null 이다

func accessGroupRow(name string, deployments []inference.Deployment, members []string) row {
	count := 0
	for _, d := range deployments {
		if slices.Contains(members, d.ModelName) && slices.ContainsFunc(accessGroupNames(d), func(g string) bool { return g == name }) {
			count++
		}
	}
	return row{"access_group": name, "model_names": members, "deployment_count": count, "spend": nil, "budget": nil}
}

func accessGroupNames(d inference.Deployment) []string {
	raw, _ := d.ExtraInfo["access_groups"].([]any)
	names := make([]string, 0, len(raw))
	for _, n := range raw {
		if s, ok := n.(string); ok {
			names = append(names, s)
		}
	}
	return names
}

func (h *Handlers) listAccessGroups(w http.ResponseWriter, r *http.Request) {
	all, err := h.deployments.All(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	groups := inference.AccessGroupsOf(all)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	slices.Sort(names)
	rows := make([]row, 0, len(names))
	for _, name := range names {
		rows = append(rows, accessGroupRow(name, all, groups[name]))
	}
	core.WriteJSON(w, http.StatusOK, row{"access_groups": rows})
}

func (h *Handlers) accessGroupInfo(w http.ResponseWriter, r *http.Request) {
	all, err := h.deployments.All(r.Context())
	if err != nil {
		h.internalError(w, r, err)
		return
	}
	name := r.PathValue("access_group")
	members, found := inference.AccessGroupsOf(all)[name]
	if !found {
		core.WriteJSON(w, http.StatusNotFound, row{"detail": row{"error": "Access group '" + name + "' not found"}})
		return
	}
	core.WriteJSON(w, http.StatusOK, accessGroupRow(name, all, members))
}
