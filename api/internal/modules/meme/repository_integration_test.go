package meme

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateWithQuotaConcurrentPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL tidak diset")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("buat pool PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}

	userID := testUUID(t)
	email := "tkt109-" + userID + "@example.test"
	var planID string
	var maxMemes int32
	var durationDays int32
	if err := pool.QueryRow(ctx, `
		SELECT id, max_memes, duration_days
		FROM plans WHERE active = true AND max_memes > 0
		ORDER BY max_memes ASC LIMIT 1`).Scan(&planID, &maxMemes, &durationDays); err != nil {
		t.Fatalf("ambil plan quota: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email, name, password_hash)
		VALUES ($1, $2, 'TKT-109 fixture', 'not-a-login-password')`, userID, email)
	if err != nil {
		t.Fatalf("buat user fixture: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Errorf("cleanup user fixture: %v", err)
		}
	})

	_, err = pool.Exec(ctx, `
		INSERT INTO subscriptions (user_id, plan_id, status, starts_at, ends_at)
		VALUES ($1, $2, 'active', now(), now() + make_interval(days => $3))`,
		userID, planID, durationDays)
	if err != nil {
		t.Fatalf("buat subscription fixture: %v", err)
	}

	repo := NewPostgresRepository(pool)
	initial := int(maxMemes) - 2
	for i := 0; i < initial; i++ {
		if _, err := repo.CreateWithQuota(ctx, Meme{
			UserID: userID, Title: "TKT-109 initial " + strconv.Itoa(i), BaseImageURL: "base/fixture-" + strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("buat meme awal %d/%d: %v", i+1, initial, err)
		}
	}

	const concurrentRequests = 6
	results := make(chan error, concurrentRequests)
	var wg sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := repo.CreateWithQuota(ctx, Meme{
				UserID: userID, Title: "TKT-109 concurrent " + strconv.Itoa(i), BaseImageURL: "base/concurrent-" + strconv.Itoa(i),
			})
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var success, rejected int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrNotSubscribedGate):
			rejected++
		default:
			t.Fatalf("concurrent request menghasilkan error tak terduga: %v", err)
		}
	}
	if success != 2 || rejected != concurrentRequests-2 {
		t.Fatalf("hasil concurrency: success=%d rejected=%d, want success=2 rejected=%d", success, rejected, concurrentRequests-2)
	}

	var count int64
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memes WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("hitung meme fixture: %v", err)
	}
	if count != int64(maxMemes) {
		t.Fatalf("jumlah meme=%d, want quota=%d", count, maxMemes)
	}
}

func testUUID(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("buat random fixture ID: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:])
}

type allowAllMemeGate struct{}

func (allowAllMemeGate) CanCreate(context.Context, string) error { return nil }
func (allowAllMemeGate) CanUse(context.Context, string) error    { return nil }

func TestPrepareUploadPresignFailureCleansFixtureRecord(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL tidak diset")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("buat pool PostgreSQL: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	userID := testUUID(t)
	email := "tkt108-presign-" + userID + "@example.test"
	var planID string
	var durationDays int32
	if err := pool.QueryRow(ctx, `SELECT id, duration_days FROM plans WHERE active=true AND max_memes > 0 ORDER BY max_memes LIMIT 1`).Scan(&planID, &durationDays); err != nil {
		t.Fatalf("ambil plan fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id,email,name,password_hash) VALUES ($1,$2,'TKT-108 fixture','not-a-login-password')`, userID, email); err != nil {
		t.Fatalf("buat user fixture: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
			t.Errorf("cleanup user fixture: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions(user_id,plan_id,status,starts_at,ends_at) VALUES($1,$2,'active',now(),now()+make_interval(days=>$3))`, userID, planID, durationDays); err != nil {
		t.Fatalf("buat subscription fixture: %v", err)
	}
	repo := NewPostgresRepository(pool)
	service := &Service{
		repo: repo, bucket: "fixture", baseURL: "http://local.invalid",
		gate: allowAllMemeGate{},
		presignPut: func(context.Context, string, string) (string, error) {
			return "", errors.New("presign fixture failure")
		},
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memes WHERE user_id=$1`, userID).Scan(&before); err != nil {
		t.Fatalf("hitung sebelum: %v", err)
	}
	if _, err := service.PrepareUpload(ctx, userID, "TKT-108 presign failure", ".png", "image/png"); err == nil || !strings.Contains(err.Error(), "presign gagal") {
		t.Fatalf("PrepareUpload error = %v, want presign failure", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM memes WHERE user_id=$1`, userID).Scan(&after); err != nil {
		t.Fatalf("hitung sesudah: %v", err)
	}
	if after != before {
		t.Fatalf("fixture meme count after failed presign = %d, before = %d", after, before)
	}
}
