package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hedgehog-db/hedgehog/internal/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type traceIDKeyType struct{}

var traceIDKey = traceIDKeyType{}

// LoggingMiddleware logs each request, including trace ID when present.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		tid, _ := r.Context().Value(traceIDKey).(string)
		if tid != "" {
			log.Printf("[%s] %s %s %d %s", tid[:8], r.Method, r.URL.Path, sw.status, time.Since(start))
		} else {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start))
		}
	})
}

// RecoveryMiddleware recovers from panics.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC: %v\n%s", err, debug.Stack())
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware adds CORS headers.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// --- TraceIDMiddleware ---

// TraceIDMiddleware reads or generates a request ID and puts it in context + response header.
func TraceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get("X-Request-ID")
		if tid == "" {
			tid = r.Header.Get("X-Trace-ID")
		}
		if tid == "" {
			tid = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", tid)
		ctx := context.WithValue(r.Context(), traceIDKey, tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// --- MetricsMiddleware ---

// MetricsMiddleware records Prometheus HTTP request count and duration.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		duration := time.Since(start).Seconds()

		op, tbl := classifyRequest(r.Method, r.URL.Path)
		status := statusClass(sw.status)

		metrics.HTTPRequestsTotal.WithLabelValues(r.Method, op, status, tbl).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(r.Method, op, status, tbl).Observe(duration)
	})
}

// classifyRequest derives operation name and table from method+path.
func classifyRequest(method, path string) (operation, tableName string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// /api/v1/tables
	// /api/v1/tables/{name}
	// /api/v1/tables/{name}/items
	// /api/v1/tables/{name}/items/{key}
	// /api/v1/tables/{name}/count
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "tables" {
		if len(parts) == 3 {
			switch method {
			case "POST":
				return "create_table", ""
			default:
				return "list_tables", ""
			}
		}
		tbl := parts[3]
		if len(parts) == 4 {
			return "delete_table", tbl
		}
		if len(parts) >= 5 {
			switch parts[4] {
			case "count":
				return "table_count", tbl
			case "items":
				if len(parts) == 5 {
					return "scan_items", tbl
				}
				// /api/v1/tables/{name}/items/{key}
				switch method {
				case "GET":
					return "get_item", tbl
				case "PUT":
					return "put_item", tbl
				case "DELETE":
					return "delete_item", tbl
				}
			}
		}
	}
	return "other", ""
}

func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return fmt.Sprintf("%d", code)
	}
}

// --- TracingMiddleware (OpenTelemetry) ---

var tracer = otel.Tracer("hedgehogdb")

// TracingMiddleware starts an OTel span for each request and propagates trace context.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prop := otel.GetTextMapPropagator()
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		op, _ := classifyRequest(r.Method, r.URL.Path)
		ctx, span := tracer.Start(ctx, "http."+op,
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", r.URL.Path),
			),
		)
		defer span.End()

		// Set trace ID on response header so clients can correlate
		sc := span.SpanContext()
		if sc.HasTraceID() {
			w.Header().Set("X-Trace-ID", sc.TraceID().String())
		}

		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.status_code", sw.status))
	})
}
