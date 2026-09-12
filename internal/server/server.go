package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

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
}

func New(pool *pgxpool.Pool, version string, apiKey string) *Server {
	return &Server{pool: pool, version: version, apiKey: apiKey}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /version", s.handleVersion)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /r/{code}", s.handleRedirect)
	mux.HandleFunc("POST /shorten", s.apiKeyAuth(s.handleShorten))

	return mux
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "%s", s.version)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		log.Printf("readyz: db ping failed: %v", err)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ready")
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	codeStr := r.PathValue("code")
	if strings.TrimSpace(codeStr) == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	var link string
	err := s.pool.QueryRow(r.Context(),
		"SELECT url FROM links WHERE code = $1", codeStr,
	).Scan(&link)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "invalid code", http.StatusNotFound)
		return
	}

	if err != nil {
		log.Printf("select code=%s: %v", codeStr, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, link, http.StatusTemporaryRedirect)
}

func (s *Server) handleShorten(w http.ResponseWriter, r *http.Request) {
	var requestBody ShortenRequest
	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, "failed to decode json", http.StatusBadRequest)
		return
	}

	link, err := shortener.CleanLink(string(requestBody.URL))
	if err != nil {
		http.Error(w, "failed to get parse URL", http.StatusBadRequest)
		return
	}

	const maxTries = 5

	for i := 0; i < maxTries; i++ {
		code, err := shortener.GenerateSlug(6)
		if err != nil {
			log.Printf("generate slug: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		_, err = s.pool.Exec(r.Context(),
			"INSERT INTO links (code, url) VALUES ($1, $2)", code, link)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(JSONResponse{Code: Code(code)}); err != nil {
				log.Printf("encode response: %v", err)
			}
			return
		}
		if isUniqueViolation(err) {
			continue
		}

		log.Printf("insert code=%s: %v", code, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (s *Server) apiKeyAuth(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key != s.apiKey {
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
