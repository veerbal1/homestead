package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

var httpRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{Name: "http_requests_total", Help: "Total number of HTTP requests by route and status code."},
	[]string{"route", "status"},
)
var httpRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request latency in seconds by route."},
	[]string{"route"},
)

func newRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *Server) loggerFor(r *http.Request) *slog.Logger {
	return s.logger.With("request_id", requestIDFromContext(r.Context()))
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if len(id) == 0 || len(id) > 64 {
			var err error
			id, err = newRequestID()
			if err != nil {
				s.logger.Error("request_id: generate failed", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}

		start := time.Now()
		next.ServeHTTP(rec, r)
		duration := time.Since(start)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		httpRequestDuration.WithLabelValues(route).Observe(duration.Seconds())
		httpRequestsTotal.WithLabelValues(route, strconv.Itoa(rec.status)).Inc()
	})
}
