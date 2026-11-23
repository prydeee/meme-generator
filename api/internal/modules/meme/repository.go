package meme

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Create(ctx context.Context, m Meme) (Meme, error) {
	const q = `
		INSERT INTO memes (user_id, title, base_url, top_text, bottom_text, font_size)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, title, base_url, result_url, top_text, bottom_text, font_size, created_at`
	var out Meme
	err := r.pool.QueryRow(ctx, q,
		m.UserID, m.Title, m.BaseImageURL, m.TopText, m.BottomText, m.FontSize,
	).Scan(&out.ID, &out.UserID, &out.Title, &out.BaseImageURL, &out.ResultURL,
		&out.TopText, &out.BottomText, &out.FontSize, &out.CreatedAt)
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: create gagal: %w", err)
	}
	return out, nil
}

// CreateWithQuota serializes quota checking and meme creation per user.
// The advisory transaction lock is held until commit or rollback.
func (r *Repository) CreateWithQuota(ctx context.Context, m Meme) (Meme, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: begin quota transaction gagal: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, m.UserID); err != nil {
		return Meme{}, fmt.Errorf("meme repository: quota lock gagal: %w", err)
	}

	var maxMemes, durationDays int32
	err = tx.QueryRow(ctx, `
		SELECT p.max_memes, p.duration_days
		FROM subscriptions s
		JOIN plans p ON p.id = s.plan_id
		WHERE s.user_id = $1 AND s.status = 'active' AND s.ends_at > now()
		ORDER BY s.ends_at DESC LIMIT 1`, m.UserID).Scan(&maxMemes, &durationDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return Meme{}, ErrNotSubscribedGate
	}
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: active subscription lookup gagal: %w", err)
	}

	var used int64
	err = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM memes
		WHERE user_id = $1
		  AND created_at >= now() - make_interval(days => $2)`, m.UserID, durationDays).Scan(&used)
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: quota count gagal: %w", err)
	}
	if !quotaAvailable(used, maxMemes) {
		return Meme{}, ErrNotSubscribedGate
	}

	const q = `
		INSERT INTO memes (user_id, title, base_url, top_text, bottom_text, font_size)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, title, base_url, result_url, top_text, bottom_text, font_size, created_at`
	var out Meme
	err = tx.QueryRow(ctx, q,
		m.UserID, m.Title, m.BaseImageURL, m.TopText, m.BottomText, m.FontSize,
	).Scan(&out.ID, &out.UserID, &out.Title, &out.BaseImageURL, &out.ResultURL,
		&out.TopText, &out.BottomText, &out.FontSize, &out.CreatedAt)
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: create dengan quota gagal: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Meme{}, fmt.Errorf("meme repository: commit quota transaction gagal: %w", err)
	}
	return out, nil
}

func quotaAvailable(used int64, max int32) bool {
	return max <= 0 || used < int64(max)
}

func (r *Repository) GetByID(ctx context.Context, id, userID string) (Meme, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id, user_id, title, base_url, result_url, top_text, bottom_text, font_size, created_at
		 FROM memes WHERE id = $1 AND user_id = $2`, id, userID)
	var m Meme
	err := row.Scan(&m.ID, &m.UserID, &m.Title, &m.BaseImageURL, &m.ResultURL, &m.TopText, &m.BottomText, &m.FontSize, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Meme{}, ErrNotFound
	}
	return m, err
}

func (r *Repository) SetResultURL(ctx context.Context, id, userID, resultURL string) (Meme, error) {
	const q = `
		UPDATE memes SET result_url = $2
		WHERE id = $1 AND user_id = $3
		RETURNING id, user_id, title, base_url, result_url, top_text, bottom_text, font_size, created_at`
	var out Meme
	err := r.pool.QueryRow(ctx, q, id, resultURL, userID).Scan(
		&out.ID, &out.UserID, &out.Title, &out.BaseImageURL, &out.ResultURL,
		&out.TopText, &out.BottomText, &out.FontSize, &out.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Meme{}, ErrNotFound
	}
	if err != nil {
		return Meme{}, fmt.Errorf("meme repository: set result gagal: %w", err)
	}
	return out, nil
}

func (r *Repository) ListByUser(ctx context.Context, userID string, limit, offset int32) ([]Meme, int64, error) {
	var total int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM memes WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("meme repository: count gagal: %w", err)
	}
	const q = `
		SELECT id, user_id, title, base_url, result_url, top_text, bottom_text, font_size, created_at
		FROM memes WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.pool.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("meme repository: list gagal: %w", err)
	}
	defer rows.Close()
	var items []Meme
	for rows.Next() {
		var m Meme
		if err := rows.Scan(&m.ID, &m.UserID, &m.Title, &m.BaseImageURL, &m.ResultURL,
			&m.TopText, &m.BottomText, &m.FontSize, &m.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, m)
	}
	return items, total, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id, userID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM memes WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("meme repository: delete gagal: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
