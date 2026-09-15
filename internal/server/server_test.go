package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/local/gopad/internal/metrics"
)

func TestHealthz_ReturnsOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	New().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestMetrics_ReturnsPrometheusText(t *testing.T) {
	registry := metrics.New()
	registry.IncOperations()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	New(registry).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "gopad_operations_total 1") {
		t.Fatalf("metrics body does not contain operation counter: %s", body)
	}
}
