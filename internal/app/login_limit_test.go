package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func loginRequest(app *App, ip, username, password string) int {
	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	request.RemoteAddr = ip + ":40000"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	return recorder.Code
}

func newLoginTestApp(t *testing.T) *App {
	t.Helper()
	hash, err := hashPassword("right-password")
	if err != nil {
		t.Fatal(err)
	}
	state := defaultState()
	state.Settings.Username = "admin"
	state.Settings.Password = hash
	return &App{manager: &CoreManager{dataDir: t.TempDir(), state: state}, session: "s"}
}

func TestPanelLoginIsRateLimitedPerIP(t *testing.T) {
	app := newLoginTestApp(t)
	for i := 0; i < loginMaxFails; i++ {
		if code := loginRequest(app, "203.0.113.7", "admin", "guess"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, code)
		}
	}
	if code := loginRequest(app, "203.0.113.7", "admin", "right-password"); code != http.StatusTooManyRequests {
		t.Fatalf("blocked IP with the right password: status %d, want 429", code)
	}
	// Another address is not affected, and X-Forwarded-For is not trusted.
	if code := loginRequest(app, "198.51.100.9", "admin", "right-password"); code != http.StatusOK {
		t.Fatalf("other IP: status %d, want 200", code)
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	app := newLoginTestApp(t)
	for i := 0; i < loginMaxFails-1; i++ {
		loginRequest(app, "203.0.113.8", "admin", "guess")
	}
	if code := loginRequest(app, "203.0.113.8", "admin", "right-password"); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for i := 0; i < loginMaxFails-1; i++ {
		if code := loginRequest(app, "203.0.113.8", "admin", "guess"); code != http.StatusUnauthorized {
			t.Fatalf("failures were not reset after a successful login: status %d", code)
		}
	}
}

func TestLoginFailuresExpireAndStayBounded(t *testing.T) {
	var limiter loginLimiter
	for i := 0; i < loginMaxFails; i++ {
		limiter.fail("192.0.2.1")
	}
	if !limiter.blocked("192.0.2.1") {
		t.Fatal("not blocked after the maximum number of failures")
	}
	limiter.mu.Lock()
	for i := range limiter.fails["192.0.2.1"] {
		limiter.fails["192.0.2.1"][i] = time.Now().Add(-loginFailWindow - time.Second)
	}
	limiter.mu.Unlock()
	if limiter.blocked("192.0.2.1") {
		t.Fatal("failures older than the window still block")
	}
	for i := 0; i < loginMaxTracked+50; i++ {
		limiter.fail(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	limiter.mu.Lock()
	tracked := len(limiter.fails)
	limiter.mu.Unlock()
	if tracked > loginMaxTracked {
		t.Fatalf("limiter tracks %d addresses, more than %d", tracked, loginMaxTracked)
	}
}
