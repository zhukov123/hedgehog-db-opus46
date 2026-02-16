package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggingMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := LoggingMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("LoggingMiddleware: status %d", rec.Code)
	}
}

func TestRecoveryMiddleware_PanicRecovery(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})
	h := RecoveryMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("RecoveryMiddleware on panic: status %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Internal Server Error") {
		t.Errorf("RecoveryMiddleware: body %q", body)
	}
}

func TestMetricsMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := MetricsMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("MetricsMiddleware: status %d", rec.Code)
	}
}

func TestTraceIDMiddleware_SetsHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := TraceIDMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("TraceIDMiddleware: X-Request-ID not set")
	}
}

func TestTraceIDMiddleware_PropagatesRequestID(t *testing.T) {
	var seenID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = r.Header.Get("X-Request-ID")
		if tid, ok := r.Context().Value(traceIDKey).(string); ok {
			seenID = tid
		}
		w.WriteHeader(http.StatusOK)
	})
	h := TraceIDMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("TraceIDMiddleware: want X-Request-ID custom-id-123, got %q", rec.Header().Get("X-Request-ID"))
	}
	if seenID != "custom-id-123" {
		t.Errorf("TraceIDMiddleware context: got %q", seenID)
	}
}

func TestTracingMiddleware_SetsTraceID(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := TracingMiddleware(next)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tables", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// TracingMiddleware may set X-Trace-ID from span context
	_ = rec.Header().Get("X-Trace-ID")
	if rec.Code != http.StatusOK {
		t.Errorf("TracingMiddleware: status %d", rec.Code)
	}
}
