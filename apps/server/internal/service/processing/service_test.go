package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractionRetainsHTTPStatusWithoutResponseContent(t *testing.T) {
	for _, status := range []int{400, 401, 429, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("private source and secret-token"))
		}))
		s := &Service{config: Config{Endpoint: server.URL}, client: server.Client()}
		_, err := s.Extract(context.Background(), "owner", []string{"source"})
		server.Close()
		var rejection *ExtractorHTTPError
		if !errors.As(err, &rejection) || rejection.StatusCode != status {
			t.Fatalf("status %d lost: %v", status, err)
		}
		if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret-token") {
			t.Fatal("upstream body leaked")
		}
	}
}

func TestExtractionRejectsUngroundedOffsetsAndIdentity(t *testing.T) {
	for _, invalid := range []string{"", "span", "identity", "extra"} {
		t.Run(invalid, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer local-secret" {
					t.Error("missing extractor auth")
				}
				response := Response{Version: 1, Model: "model", Revision: "weights-v1", Splitter: "char", Observations: []Observation{{Spans: []Span{{Start: 1, End: 3, Text: "小林", Label: "person", Confidence: .9}}, NeedsSemantic: .8, Supported: true}}}
				if invalid == "span" {
					response.Observations[0].Spans[0].Start = 0
				}
				if invalid == "identity" {
					response.Revision = "other"
				}
				if invalid == "extra" {
					_, _ = w.Write([]byte(`{"version":1,"model":"model","revision":"weights-v1","splitter":"char","observations":[],"unexpected":true}`))
					return
				}
				_ = json.NewEncoder(w).Encode(response)
			}))
			defer server.Close()
			s, err := New(Config{EntityRecallTimeoutMS: 200, Strategy: "hybrid", Endpoint: server.URL, Token: "local-secret", Model: "model", Revision: "weights-v1", Splitter: "char", Threshold: .75, MaxSemanticBytes: 4096}, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := s.Extract(context.Background(), "owner", []string{"😀小林和小林"})
			if invalid == "" {
				if err != nil || len(out) != 1 {
					t.Fatalf("valid extraction: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid extraction accepted")
			}
		})
	}
}

func TestPromotionDoesNotEquateNoEntitiesWithNoMemory(t *testing.T) {
	s := &Service{config: Config{EntityRecallTimeoutMS: 200, Strategy: "hybrid", Threshold: .75}}
	if ok, _ := s.Promote("scope", []Observation{{Supported: true, NeedsSemantic: .9}}); !ok {
		t.Fatal("entity-free semantic signal discarded")
	}
	if ok, _ := s.Promote("scope", []Observation{{Supported: false}}); !ok {
		t.Fatal("unsupported input discarded")
	}
	s.config.AuditRate = 1
	if ok, reason := s.Promote("scope", []Observation{{Supported: true}}); !ok || reason != "audit_sample" {
		t.Fatal("audit not sampled")
	}
	s.config.Strategy = "lightweight"
	if ok, reason := s.Promote("scope", []Observation{{Supported: false}}); ok || reason != "not_requested" {
		t.Fatal("lightweight silently invoked semantic path")
	}
}

func TestExtractSplitsLongSourcesAndKeepsWindowOrder(t *testing.T) {
	batches := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		batches = append(batches, len(request.Texts))
		observations := make([]Observation, 0, len(request.Texts))
		for _, text := range request.Texts {
			observations = append(observations, Observation{Spans: []Span{{Start: 0, End: len(text), Text: text, Label: "name", Confidence: 1}}, Supported: true})
		}
		_ = json.NewEncoder(w).Encode(Response{Version: 1, Model: "m", Revision: "r", Splitter: "char", Observations: observations})
	}))
	defer server.Close()
	service, err := New(Config{Strategy: "generative", Endpoint: server.URL, Token: "t", Model: "m", Revision: "r", Splitter: "char", Threshold: .75, AuditRate: .1, MaxSemanticBytes: 1024, EntityRecallTimeoutMS: 200}, nil)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, extractBatchWindows*20+3)
	for i := range texts {
		texts[i] = fmt.Sprintf("w%d", i)
	}
	out, err := service.Extract(context.Background(), "", texts)
	if err != nil || len(out) != len(texts) {
		t.Fatal(len(out), err)
	}
	for i, observation := range out {
		if observation.Spans[0].Text != texts[i] {
			t.Fatal("window order changed", i)
		}
	}
	if len(batches) != 21 || batches[0] != extractBatchWindows || batches[20] != 3 {
		t.Fatal(batches)
	}
}
