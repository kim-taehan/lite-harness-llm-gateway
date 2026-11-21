package admin

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"llm-gateway/internal/limits"
)

// 사용량 내보내기(N 4.5): GET /{entity}/daily/activity/export?export_type=…&format=csv|json.
// 열 순서는 litellm ExportRow 그대로다. CSV 는 수식 주입을 막으려고 위험한 첫 글자 앞에 ' 를 붙인다

var exportColumns = []string{"date", "entity_id", "entity_alias", "api_key", "key_alias", "user_id", "user_email", "model", "spend",
	"flat_cost", "prompt_tokens", "completion_tokens", "api_requests", "successful_requests", "failed_requests",
	"cache_read_input_tokens", "cache_creation_input_tokens"}

var exportTypes = []string{"daily", "daily_with_keys", "daily_with_models", "daily_with_users"}

func (h *Handlers) activityExport(entity string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start, end, ok := activityRange(w, r, true)
		if !ok {
			return
		}
		q := r.URL.Query()
		exportType, format := cmp.Or(q.Get("export_type"), "daily"), cmp.Or(q.Get("format"), "csv")
		if !slices.Contains(exportTypes, exportType) || (format != "csv" && format != "json") {
			validationError{{Type: "enum", Loc: []any{"query", "export_type"}, Msg: "Input should be 'daily', 'daily_with_keys', 'daily_with_models' or 'daily_with_users'", Input: exportType}}.write(w)
			return
		}
		f := limits.ActivityFilter{Start: start, End: end}
		aliases, ok := h.activityScope(w, r, entity, &f)
		if !ok {
			return
		}
		rows, err := h.usage.Activity(r.Context(), f)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		c, err := h.activityContext(r.Context(), rows)
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		records := exportRecords(rows, c, entity, exportType, aliases)
		var body []byte
		contentType := "application/json"
		if format == "json" {
			if body, err = json.Marshal(records); err != nil {
				h.internalError(w, r, err)
				return
			}
		} else {
			contentType = "text/csv"
			body = exportCSV(records)
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="`+entity+"-usage-"+start.Format("2006-01-02")+"-"+end.Format("2006-01-02")+"-"+exportType+"."+format+`"`)
		_, _ = w.Write(body) // 쓰기 실패는 클라이언트가 끊은 것이다
	}
}

// exportRecords 는 export_type 에 따라 묶은 행이다. 묶는 키 오름차순이다
func exportRecords(rows []limits.ActivityRow, c activityContext, entity, exportType string, aliases map[string]*string) []map[string]any {
	type group struct {
		key     []string
		sample  limits.ActivityRow
		metrics limits.Metrics
	}
	groups := map[string]*group{}
	for _, a := range rows {
		entityID := deref(a.UserID)
		if entity == entityTeam {
			entityID = deref(a.TeamID)
		}
		key := []string{a.Date, entityID}
		switch exportType {
		case "daily_with_keys":
			key = append(key, deref(a.KeyToken))
		case "daily_with_models":
			key = append(key, c.upstreamModel(a))
		case "daily_with_users":
			key = append(key, deref(a.UserID))
		}
		id := strings.Join(key, "\x00")
		if groups[id] == nil {
			groups[id] = &group{key: key, sample: a}
		}
		groups[id].metrics.Add(a.Metrics)
	}
	ids := make([]string, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		g := groups[id]
		m := g.metrics
		rec := map[string]any{"date": g.key[0], "entity_id": g.key[1], "entity_alias": nil, "api_key": nil, "key_alias": nil,
			"user_id": nil, "user_email": nil, "model": nil, "spend": m.Spend, "flat_cost": 0.0, "prompt_tokens": m.PromptTokens,
			"completion_tokens": m.CompletionTokens, "api_requests": m.SuccessfulRequests + m.FailedRequests,
			"successful_requests": m.SuccessfulRequests, "failed_requests": m.FailedRequests,
			"cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
		if entity == entityTeam {
			rec["entity_alias"] = aliases[g.key[1]]
		}
		switch exportType {
		case "daily_with_keys":
			rec["api_key"] = g.key[2]
			if meta, ok := c.keys[g.key[2]]; ok {
				rec["key_alias"], rec["user_id"], rec["user_email"] = meta["key_alias"], meta["user_id"], meta["user_email"]
			}
		case "daily_with_models":
			rec["model"] = g.key[2]
		case "daily_with_users":
			rec["user_id"] = g.key[2]
		}
		out = append(out, rec)
	}
	return out
}

func exportCSV(records []map[string]any) []byte {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	cw.UseCRLF = true
	_ = cw.Write(exportColumns) // bytes.Buffer 쓰기는 실패하지 않는다
	for _, rec := range records {
		line := make([]string, len(exportColumns))
		for i, col := range exportColumns {
			line[i] = csvCell(rec[col])
		}
		_ = cw.Write(line)
	}
	cw.Flush()
	return buf.Bytes()
}

func csvCell(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s = x
	case *string:
		if x == nil {
			return ""
		}
		s = *x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		b, _ := json.Marshal(x) // 위에서 다루지 않은 값은 없지만 혹시 오면 JSON 으로 적는다
		return string(b)
	}
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
