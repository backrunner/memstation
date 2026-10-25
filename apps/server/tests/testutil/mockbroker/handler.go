// Package mockbroker implements a deterministic contract fixture. It is not an
// AI model, a quality benchmark, or a production semantic fallback.
package mockbroker

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"memstation/apps/server/internal/service/memoryllm"
	"memstation/apps/server/internal/testutil/memoryllmfixture"
)

type Handler struct {
	Credential string
	mu         sync.Mutex
	Calls      map[string]map[string]any
	FailNext   bool
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Credential == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.Credential)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	role, workspace, call := r.Header.Get("X-MemStation-Role"), r.Header.Get("X-MemStation-Workspace"), r.Header.Get("X-MemStation-Call-Id")
	if r.Header.Get("X-MemStation-Internal-Call") != "memory-broker-v1" || workspace == "" || !strings.Contains("|summary|analysis|embedding|rerank|", "|"+role+"|") || role == "" {
		http.Error(w, "scope denied", http.StatusForbidden)
		return
	}
	w.Header().Set("X-MemStation-Broker-Version", "1")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/memory/calls/") {
		h.mu.Lock()
		result := h.Calls[strings.TrimPrefix(r.URL.Path, "/v1/memory/calls/")]
		h.mu.Unlock()
		if result == nil || result["workspace_id"] != workspace || result["role"] != role {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if r.Method != "POST" || call == "" {
		http.Error(w, "invalid request", 400)
		return
	}
	var in struct {
		Model      string `json:"model"`
		Dimensions int    `json:"dimensions"`
		Messages   []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Documents []struct {
			ID string `json:"id"`
		} `json:"documents"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&in) != nil || in.Model != "memory."+role {
		http.Error(w, "alias denied", http.StatusForbidden)
		return
	}
	h.mu.Lock()
	fail := h.FailNext
	h.FailNext = false
	h.mu.Unlock()
	if fail {
		http.Error(w, "simulated retryable failure", http.StatusServiceUnavailable)
		return
	}
	version := "fixture-text-v1"
	var result map[string]any
	switch r.URL.Path {
	case "/v1/chat/completions":
		if role != "summary" && role != "analysis" || len(in.Messages) < 2 {
			http.Error(w, "role denied", http.StatusForbidden)
			return
		}
		var input map[string]any
		if json.Unmarshal([]byte(in.Messages[len(in.Messages)-1].Content), &input) != nil {
			http.Error(w, "invalid input", 400)
			return
		}
		task := "analysis.extract"
		if role == "summary" {
			task = "summary.generate"
			if input["chunk_summaries"] != nil {
				task = "summary.merge"
			}
		} else if input["allowed_keys"] != nil {
			task = "keymemory.reconcile"
		}
		var raw []byte
		var err error
		if input["memory_type"] != nil && input["reference_time"] != nil {
			raw, err = json.Marshal(map[string]any{"importance": 1, "decay_factor": 1, "activity_at": input["reference_time"], "key_memory_candidate": false, "reason": "Explicit deterministic score fixture"})
		} else {
			raw, err = (memoryllmfixture.Generator{}).Generate(r.Context(), memoryllm.Request{Task: task, Input: input})
		}
		if err != nil {
			http.Error(w, "fixture generation failed", 500)
			return
		}
		result = map[string]any{"id": "response-" + call, "model": version, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(raw)}}}}
	case "/v1/embeddings":
		if role != "embedding" || in.Dimensions != 3 || r.Header.Get("X-MemStation-Embedding-Fingerprint") != "fixture-embedding-v1:3" {
			http.Error(w, "index mismatch", http.StatusConflict)
			return
		}
		version = "fixture-embedding-v1"
		w.Header().Set("X-MemStation-Embedding-Fingerprint", "fixture-embedding-v1:3")
		result = map[string]any{"model": version, "data": []any{map[string]any{"index": 0, "embedding": []float32{1, 0, 0}}}}
	case "/v1/memory/rerank/v1":
		if role != "rerank" {
			http.Error(w, "role denied", http.StatusForbidden)
			return
		}
		results := []any{}
		for i, d := range in.Documents {
			results = append(results, map[string]any{"document_id": d.ID, "score": 1 / float64(i+1)})
		}
		result = map[string]any{"version": 1, "results": results}
	default:
		http.NotFound(w, r)
		return
	}
	result["usage"] = map[string]int{"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14}
	w.Header().Set("X-MemStation-Model-Version", version)
	w.Header().Set("X-MemStation-Call-Id", call)
	w.Header().Set("X-MemStation-Request-Id", "fixture-"+call)
	w.Header().Set("X-MemStation-Usage-Id", "usage-"+call)
	w.Header().Set("X-MemStation-Reported-Cost", "0")
	w.Header().Set("X-MemStation-Currency", "USD")
	h.mu.Lock()
	if h.Calls == nil {
		h.Calls = map[string]map[string]any{}
	}
	h.Calls[call] = map[string]any{"call_id": call, "workspace_id": workspace, "role": role, "status": "complete", "usage_id": "usage-" + call, "request_id": "fixture-" + call, "model_version": version, "input_tokens": 10, "output_tokens": 4, "reported_cost": "0", "currency": "USD"}
	h.mu.Unlock()
	_ = json.NewEncoder(w).Encode(result)
}
