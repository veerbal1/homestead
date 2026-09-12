package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound  = errors.New("store: link not found")
	ErrCodeTaken = errors.New("store: code already taken")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Save(ctx context.Context, code, url string) error {
	_, err := s.pool.Exec(ctx,
		"INSERT INTO links (code, url) VALUES ($1, $2)", code, url)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: save code=%s: %w", code, ErrCodeTaken)
		}
		return fmt.Errorf("store: save code=%s: %w", code, err)
	}
	return nil
}

func (s *Store) Resolve(ctx context.Context, code string) (string, error) {
	var url string
	err := s.pool.QueryRow(ctx,
		"SELECT url FROM links WHERE code = $1", code,
	).Scan(&url)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("store: resolve code=%s: %w", code, ErrNotFound)
		}
		return "", fmt.Errorf("store: resolve code=%s: %w", code, err)
	}
	return url, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
