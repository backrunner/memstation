// Package memoryprocessingfixture supplies a deterministic encoder transport for
// contract tests and reliability drills. It does not measure extraction quality.
package memoryprocessingfixture

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"memstation/apps/server/internal/service/processing"
)

func Config() processing.Config {
	return processing.Config{Strategy: "generative", EntityRecallTimeoutMS: 200, Endpoint: "http://encoder-fixture.invalid", Token: "fixture-only-encoder-token", Model: "fixture-encoder", Revision: "fixture-revision", Splitter: "char", Threshold: .75, MaxSemanticBytes: 131072}
}

func New() (*processing.Service, func(), error) {
	return NewWithEntities(nil)
}

// NewWithEntities returns exact deterministic spans for synthetic contract data.
// It is never used as a quality-evaluation encoder.
func NewWithEntities(names []string) (*processing.Service, func(), error) {
	cfg := Config()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+cfg.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/extract" && r.URL.Path != "/entities" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/entities" {
			timeout, err := strconv.Atoi(r.Header.Get("X-Memstation-Timeout-Ms"))
			if err != nil || timeout < 1 || timeout > 5000 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		var request struct {
			Version  int      `json:"version"`
			Model    string   `json:"model"`
			Revision string   `json:"revision"`
			Splitter string   `json:"splitter"`
			Texts    []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Version != 1 || request.Model != cfg.Model || request.Revision != cfg.Revision || request.Splitter != cfg.Splitter {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		response := processing.Response{Version: 1, Model: cfg.Model, Revision: cfg.Revision, Splitter: cfg.Splitter, Observations: make([]processing.Observation, len(request.Texts))}
		for i := range response.Observations {
			response.Observations[i].Supported = true
			response.Observations[i].Spans = []processing.Span{}
			for _, name := range names {
				if name == "" {
					continue
				}
				if at := strings.Index(request.Texts[i], name); at >= 0 {
					start := len([]rune(request.Texts[i][:at]))
					response.Observations[i].Spans = append(response.Observations[i].Spans, processing.Span{Start: start, End: start + len([]rune(name)), Text: name, Label: "person", Confidence: 1})
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	cfg.Endpoint = server.URL
	service, err := processing.New(cfg, nil)
	if err != nil {
		server.Close()
		return nil, nil, err
	}
	return service, server.Close, nil
}
