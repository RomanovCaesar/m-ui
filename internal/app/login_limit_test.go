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

// Each sign-in gets its own token; logging out kills that token on the server, so a
// copied cookie stops working, while other browsers stay signed in.
func TestSessionsArePerLoginAndLogoutRevokes(t *testing.T) {
	app := newLoginTestApp(t)
	login := func() (*http.Cookie, *httptest.ResponseRecorder) {
		request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"right-password"}`))
		request.RemoteAddr = "203.0.113.20:5000"
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, request)
		for _, cookie := range recorder.Result().Cookies() {
			if cookie.Name == sessionCookieName {
				return cookie, recorder
			}
		}
		t.Fatalf("no session cookie: %d %s", recorder.Code, recorder.Body.String())
		return nil, nil
	}
	first, _ := login()
	second, _ := login()
	if first.Value == second.Value {
		t.Fatal("two sign-ins share one token")
	}
	if first.Secure {
		t.Fatal("a plain-HTTP sign-in must not get a Secure cookie")
	}
	authed := func(cookie *http.Cookie) int {
		request := httptest.NewRequest(http.MethodGet, "/api/tools/uuid", nil)
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		app.routes().ServeHTTP(recorder, request)
		return recorder.Code
	}
	logout := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logout.AddCookie(first)
	app.routes().ServeHTTP(httptest.NewRecorder(), logout)
	if code := authed(first); code != http.StatusUnauthorized {
		t.Fatalf("logged-out token still works: %d", code)
	}
	if code := authed(second); code == http.StatusUnauthorized {
		t.Fatal("logging out one browser signed the other out too")
	}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"right-password"}`))
	request.RemoteAddr = "203.0.113.21:5000"
	request.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	app.routes().ServeHTTP(recorder, request)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("a sign-in over HTTPS must get a Secure cookie: %+v", cookies)
	}
}
