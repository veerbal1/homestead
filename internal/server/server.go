package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/veerbal1/homestead/internal/config"
	"github.com/veerbal1/homestead/internal/shortener"
	"github.com/veerbal1/homestead/internal/store"
)

type ShortenRequest struct {
	URL string `json:"url"`
}

type JSONResponse struct {
	Code string `json:"code"`
}

const (
	cachePrefix = "link:"
	cacheTTL    = 24 * time.Hour
)

type Server struct {
	store  *store.Store
	rdb    *redis.Client
	cfg    config.Config
	logger *slog.Logger
}

func New(store *store.Store, rdb *redis.Client, cfg config.Config, logger *slog.Logger) *Server {
	return &Server{store: store, rdb: rdb, cfg: cfg, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /r/{code}", s.handleRedirect)
	mux.Handle("POST /shorten", s.apiKeyAuth(http.HandlerFunc(s.handleShorten)))
	mux.Handle("GET /metrics", promhttp.Handler())

	return s.requestID(s.metrics(mux))
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", s.cfg.Version)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	logger := s.loggerFor(r)

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ReadyzTimeout)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
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

	cacheKey := cachePrefix + codeStr

	link, err := s.rdb.Get(r.Context(), cacheKey).Result()
	if err == nil {
		cacheRequestsTotal.WithLabelValues("hit").Inc()
		http.Redirect(w, r, link, http.StatusTemporaryRedirect)
		return
	}
	if errors.Is(err, redis.Nil) {
		cacheRequestsTotal.WithLabelValues("miss").Inc()
	} else {
		cacheRequestsTotal.WithLabelValues("error").Inc()
		logger.Warn("redirect: cache get failed, falling back to db", "error", err, "code", codeStr)
	}

	link, err = s.store.Resolve(r.Context(), codeStr)

	if errors.Is(err, store.ErrNotFound) {
		logger.Warn("redirect: unknown code", "code", codeStr, "status", http.StatusNotFound)
		http.Error(w, "invalid code", http.StatusNotFound)
		return
	}

	if err != nil {
		logger.Error("redirect: select failed", "error", err, "code", codeStr)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.rdb.Set(r.Context(), cacheKey, link, cacheTTL).Err(); err != nil {
		logger.Warn("redirect: cache set failed", "error", err, "code", codeStr)
	}

	http.Redirect(w, r, link, http.StatusTemporaryRedirect)
}

func (s *Server) handleShorten(w http.ResponseWriter, r *http.Request) {
	logger := s.loggerFor(r)

	var requestBody ShortenRequest
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			logger.Warn("shorten: body too large", "limit", s.cfg.MaxRequestBodyBytes, "status", http.StatusRequestEntityTooLarge)
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		logger.Warn("shorten: bad json", "error", err, "status", http.StatusBadRequest)
		http.Error(w, "failed to decode json", http.StatusBadRequest)
		return
	}

	link, err := shortener.CleanLink(requestBody.URL)
	if err != nil {
		logger.Warn("shorten: bad url", "error", err, "status", http.StatusBadRequest)
		http.Error(w, "failed to get parse URL", http.StatusBadRequest)
		return
	}

	for i := 0; i < s.cfg.MaxSlugTries; i++ {
		code, err := shortener.GenerateSlug(s.cfg.SlugLength)
		if err != nil {
			logger.Error("shorten: generate slug failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		err = s.store.Save(r.Context(), code, link)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(JSONResponse{Code: code}); err != nil {
				logger.Error("shorten: encode response failed", "error", err, "code", code)
			}
			return
		}
		if errors.Is(err, store.ErrCodeTaken) {
			logger.Debug("shorten: slug collision, retrying", "attempt", i+1, "code", code)
			continue
		}

		logger.Error("shorten: insert failed", "error", err, "code", code)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	logger.Error("shorten: slug collisions exhausted", "tries", s.cfg.MaxSlugTries)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) apiKeyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(key), []byte(s.cfg.APIKey)) != 1 {
			s.loggerFor(r).Warn("unauthorized request", "status", http.StatusUnauthorized)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
