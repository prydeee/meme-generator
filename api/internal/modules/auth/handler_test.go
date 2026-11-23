package auth

import (
	"net/http"
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

func TestSetAuthCookiesProtectsTokensFromJavaScript(t *testing.T) {
	recorder := httptest.NewRecorder()
	setAuthCookies(recorder, AuthResponse{Token: TokenPayload{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresIn:    3600,
	}})

	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected 2 auth cookies, got %d", len(cookies))
	}
	for _, c := range cookies {
		if !c.HttpOnly {
			t.Errorf("cookie %q is not HttpOnly", c.Name)
		}
		if c.Path != "/" {
			t.Errorf("cookie %q path = %q, want /", c.Name, c.Path)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie %q SameSite = %v, want Lax", c.Name, c.SameSite)
		}
	}
	if access := cookies[0]; access.Name == cookie.AccessTokenName && access.MaxAge != 3600 {
		t.Errorf("access token max-age = %d, want 3600", access.MaxAge)
	}
}
