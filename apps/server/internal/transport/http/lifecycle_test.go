package httptransport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMemoryRecoveryAndBrokerLoopGuards(t *testing.T) {
	reached := false
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
	guarded := memoryRecoveryBarrier(func(context.Context) error { return errors.New("deletion history missing") })(handler)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/memory/search", nil)
	w := httptest.NewRecorder()
	guarded.ServeHTTP(w, r)
	if w.Code != 503 || reached {
		t.Fatal("unverified restored data was served")
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("X-MemStation-Internal-Call", "memory-broker-v1")
	w = httptest.NewRecorder()
	memoryRecoveryBarrier(nil)(handler).ServeHTTP(w, r)
	if w.Code != 409 || reached {
		t.Fatal("broker loop was allowed")
	}
}
