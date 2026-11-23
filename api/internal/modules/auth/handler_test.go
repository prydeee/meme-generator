package auth

import (
	"net/http/httptest"
	"testing"

	"github.com/nyxsr/meme-portal/api/internal/platform/cookie"
)

func TestClearAuthCookiesUsesAuthCookiePath(t *testing.T) {
	recorder := httptest.NewRecorder()
	clearAuthCookies(recorder)

	cleared := recorder.Result().Cookies()
	if len(cleared) != 2 {
		t.Fatalf("expected 2 cleared cookies, got %d", len(cleared))
	}

	seen := map[string]bool{}
	for _, c := range cleared {
		seen[c.Name] = true
		if c.Path != "/" {
			t.Errorf("cookie %q path = %q, want %q", c.Name, c.Path, "/")
		}
		if c.MaxAge != -1 {
			t.Errorf("cookie %q max-age = %d, want -1", c.Name, c.MaxAge)
		}
	}

	if !seen[cookie.AccessTokenName] || !seen[cookie.RefreshTokenName] {
		t.Fatalf("cleared cookies = %#v, want access and refresh cookies", seen)
	}
}
