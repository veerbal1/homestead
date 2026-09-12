package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/veerbal1/homestead/internal/shortener"
)

type Code string
type URL string

type ShortenRequest struct {
	URL URL `json:"url"`
}

type JSONResponse struct {
	Code Code `json:"code"`
}

type Server struct {
	pool    *pgxpool.Pool
	version string
	apiKey  string
	logger  *slog.Logger
}

func New(pool *pgxpool.Pool, version string, apiKey string, logger *slog.Logger) *Server {
	return &Server{pool: pool, version: version, apiKey: apiKey, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /r/{code}", s.handleRedirect)
	mux.HandleFunc("POST /shorten", s.apiKeyAuth(s.handleShorten))
	mux.Handle("GET /metrics", promhttp.Handler())

	return s.requestID(s.metrics(mux))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", s.version)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	logger := s.loggerFor(r)

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		logger.Error("readyz: db ping failed", "error", err)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ready")
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	logger := s.loggerFor(r)

	codeStr := r.PathValue("code")
	if strings.TrimSpace(codeStr) == "" {
		logger.Warn("redirect: missing code", "status", http.StatusBadRequest)
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	var link string
	err := s.pool.QueryRow(r.Context(),
		"SELECT url FROM links WHERE code = $1", codeStr,
	).Scan(&link)

	if errors.Is(err, pgx.ErrNoRows) {
		logger.Warn("redirect: unknown code", "code", codeStr, "status", http.StatusNotFound)
		http.Error(w, "invalid code", http.StatusNotFound)
		return
	}

	if err != nil {
		logger.Error("redirect: select failed", "error", err, "code", codeStr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, link, http.StatusTemporaryRedirect)
}

func (s *Server) handleShorten(w http.ResponseWriter, r *http.Request) {
	logger := s.loggerFor(r)

	var requestBody ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		logger.Warn("shorten: bad json", "error", err, "status", http.StatusBadRequest)
		http.Error(w, "failed to decode json", http.StatusBadRequest)
		return
	}

	link, err := shortener.CleanLink(string(requestBody.URL))
	if err != nil {
		logger.Warn("shorten: bad url", "error", err, "status", http.StatusBadRequest)
		http.Error(w, "failed to get parse URL", http.StatusBadRequest)
		return
	}

	const maxTries = 5

	for i := 0; i < maxTries; i++ {
		code, err := shortener.GenerateSlug(6)
		if err != nil {
			logger.Error("shorten: generate slug failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		_, err = s.pool.Exec(r.Context(),
			"INSERT INTO links (code, url) VALUES ($1, $2)", code, link)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(JSONResponse{Code: Code(code)}); err != nil {
				logger.Error("shorten: encode response failed", "error", err, "code", code)
			}
			return
		}
		if isUniqueViolation(err) {
			logger.Debug("shorten: slug collision, retrying", "attempt", i+1, "code", code)
			continue
		}

		logger.Error("shorten: insert failed", "error", err, "code", code)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	logger.Error("shorten: slug collisions exhausted", "tries", maxTries)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) apiKeyAuth(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key != s.apiKey {
			s.loggerFor(r).Warn("unauthorized request", "status", http.StatusUnauthorized)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return false
}
