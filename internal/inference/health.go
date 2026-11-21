package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"llm-gateway/internal/core"
)

// 모델 헬스 체크(M 5장 "Health Status" 탭, 모델 추가의 "Test Connect"). litellm 처럼 업스트림에 아주 짧은 요청을 실제로 보낸다

// probeTimeout 은 헬스 요청 하나의 시간 제한이다. 로컬 모델은 처음 불릴 때 적재하느라 느리다
const probeTimeout = 60 * time.Second

// probe 는 배포에 1 토큰짜리 요청을 보낸다. mode 가 embedding 이면 임베딩으로 본다
func (h *Handlers) probe(ctx context.Context, d Deployment, mode string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	path, body := "chat/completions", map[string]any{"model": d.UpstreamModel, "max_tokens": 1,
		"messages": []map[string]string{{"role": "user", "content": "ping"}}}
	if mode == "embedding" {
		path, body = "embeddings", map[string]any{"model": d.UpstreamModel, "input": "ping"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := h.send(ctx, d, path, payload)
	if err != nil {
		return fmt.Errorf("%s 에 닿지 않는다: %w", d.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) // 오류 설명용이라 못 읽어도 상태만으로 충분하다
		return fmt.Errorf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// health 는 GET /health 다. model_id 가 있으면 그 배포만, 없으면 쓸 수 있는 배포 전부를 본다
func (h *Handlers) health(w http.ResponseWriter, r *http.Request) {
	var targets []Deployment
	if id := r.URL.Query().Get("model_id"); id != "" {
		d, err := h.deployments.Find(r.Context(), id)
		if errors.Is(err, ErrDeploymentNotFound) {
			core.ProxyError{Status: 404, Type: "None", Param: "model_id", Message: "Model id = " + id + " not found on litellm proxy"}.Write(w)
			return
		}
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		targets = []Deployment{d}
	} else {
		all, err := h.deployments.Active(r.Context())
		if err != nil {
			h.internalError(w, r, err)
			return
		}
		targets = all
	}
	healthy, unhealthy := []map[string]any{}, []map[string]any{}
	for _, d := range targets {
		mode, _ := d.ExtraInfo["mode"].(string)
		endpoint := map[string]any{"model": d.Provider + "/" + d.UpstreamModel, "api_base": d.BaseURL, "model_id": d.ID}
		if err := h.probe(r.Context(), d, mode); err != nil {
			endpoint["error"] = err.Error()
			unhealthy = append(unhealthy, endpoint)
			continue
		}
		healthy = append(healthy, endpoint)
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"healthy_endpoints": healthy, "unhealthy_endpoints": unhealthy,
		"healthy_count": len(healthy), "unhealthy_count": len(unhealthy)})
}

// testConnection 은 POST /health/test_connection 이다. 저장하기 전의 모델 설정으로 업스트림을 불러 본다.
// 대시보드는 실패도 200 의 {"status":"error","result":{"error"}} 로 읽는다
func (h *Handlers) testConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LitellmParams struct {
			Model   string `json:"model"`
			APIBase string `json:"api_base"`
		} `json:"litellm_params"`
		Mode string `json:"mode"`
	}
	fail := func(message string) {
		core.WriteJSON(w, http.StatusOK, map[string]any{"status": "error", "message": message, "result": map[string]any{"error": message}})
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		fail("Invalid JSON body: " + err.Error())
		return
	}
	provider, upstream, found := strings.Cut(req.LitellmParams.Model, "/")
	if !found || upstream == "" || req.LitellmParams.APIBase == "" {
		fail("litellm_params.model must look like '<provider>/<model>' and api_base is required")
		return
	}
	d := Deployment{Provider: provider, UpstreamModel: upstream, BaseURL: req.LitellmParams.APIBase}
	if err := h.probe(r.Context(), d, req.Mode); err != nil {
		fail(err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"status": "success", "result": map[string]any{}})
}
