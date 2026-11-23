package subscription

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCheckoutSameKeyConcurrentHTTP exercises the running local API, rather
// than calling the repository directly. A closed start channel is the barrier
// release for all request goroutines.
func TestCheckoutSameKeyConcurrentHTTP(t *testing.T) {
	base := os.Getenv("TEST_API_URL")
	dsn := os.Getenv("TEST_DATABASE_URL")
	if base == "" || dsn == "" {
		t.Skip("TEST_API_URL dan TEST_DATABASE_URL harus diset")
	}
	base = strings.TrimRight(base, "/")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("tkt105-http-%d@example.test", time.Now().UnixNano())
	password := "LocalTest-Only-12345"
	postJSON := func(path string, body any, headers map[string]string) (*http.Response, error) {
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return http.DefaultClient.Do(req)
	}
	register, err := postJSON("/api/v1/auth/register", map[string]string{"email": email, "password": password, "name": "TKT105 HTTP"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer register.Body.Close()
	var registerBody struct {
		Data struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(register.Body).Decode(&registerBody); err != nil {
		t.Fatal(err)
	}
	userID := registerBody.Data.User.ID
	if userID == "" {
		t.Fatal("register did not return fixture user ID")
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) }()
	login, err := postJSON("/api/v1/auth/login", map[string]string{"email": email, "password": password}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	var cookies []string
	for _, c := range login.Cookies() {
		cookies = append(cookies, c.Name+"="+c.Value)
	}
	if len(cookies) < 2 {
		t.Fatalf("login returned %d auth cookies", len(cookies))
	}
	cookieHeader := strings.Join(cookies, "; ")
	const requests = 8
	start := make(chan struct{})
	results := make(chan struct {
		status    int
		reference string
	}, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := postJSON("/api/v1/subscription/checkout", map[string]string{"planCode": "premium_1d"}, map[string]string{"Cookie": cookieHeader, "Idempotency-Key": "tkt105-concurrent-same-key"})
			if err != nil {
				results <- struct {
					status    int
					reference string
				}{status: 0}
				return
			}
			defer res.Body.Close()
			var body struct {
				Data struct {
					Payment struct {
						Reference string `json:"reference"`
					} `json:"payment"`
				} `json:"data"`
			}
			_ = json.NewDecoder(res.Body).Decode(&body)
			results <- struct {
				status    int
				reference string
			}{status: res.StatusCode, reference: body.Data.Payment.Reference}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	refs := map[string]bool{}
	success := 0
	for result := range results {
		if result.status == http.StatusCreated {
			success++
			refs[result.reference] = true
		}
	}
	if success != requests || len(refs) != 1 {
		t.Fatalf("concurrent same-key checkout: success=%d unique references=%d, want %d and 1", success, len(refs), requests)
	}
	var paymentCount, keyCount, referenceCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*), COUNT(DISTINCT p.idempotency_key), COUNT(DISTINCT p.reference) FROM payments p JOIN subscriptions s ON s.id=p.subscription_id WHERE s.user_id=$1`, userID).Scan(&paymentCount, &keyCount, &referenceCount); err != nil {
		t.Fatal(err)
	}
	if paymentCount != 1 || keyCount != 1 || referenceCount != 1 {
		t.Fatalf("database checkout rows=%d keys=%d references=%d, want 1/1/1", paymentCount, keyCount, referenceCount)
	}
}
