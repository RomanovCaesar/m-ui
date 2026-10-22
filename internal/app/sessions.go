package app

import (
	"crypto/subtle"
	"net/http"
	"time"
)

// Every sign-in gets its own random token. Previously all browsers shared one
// process-wide token: logging out only dropped the cookie, so a copied cookie kept
// working until the panel restarted or the password changed.
const (
	sessionCookieName = "mui_session"
	// maxPanelSessions bounds the table; the oldest session is dropped beyond it.
	maxPanelSessions = 64
)

// startSession issues a new token valid for d.
func (a *App) startSession(d time.Duration) (string, error) {
	var token string
	if err := randomToken(&token); err != nil {
		return "", err
	}
	now := time.Now()
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.sessions == nil {
		a.sessions = map[string]time.Time{}
	}
	for other, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, other)
		}
	}
	if len(a.sessions) >= maxPanelSessions {
		oldest, oldestExpiry := "", time.Time{}
		for other, expires := range a.sessions {
			if oldest == "" || expires.Before(oldestExpiry) {
				oldest, oldestExpiry = other, expires
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[token] = now.Add(d)
	return token, nil
}

// validSession reports whether token belongs to a live session. App.session is a
// fixed, non-expiring token that only tests set.
func (a *App) validSession(token string) bool {
	if token == "" {
		return false
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if a.session != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.session)) == 1 {
		return true
	}
	expires, ok := a.sessions[token]
	if !ok {
		return false
	}
	if !time.Now().Before(expires) {
		delete(a.sessions, token)
		return false
	}
	return true
}

// endSession invalidates one token (logout).
func (a *App) endSession(token string) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	delete(a.sessions, token)
	if token != "" && token == a.session {
		a.session = ""
	}
}

// endAllSessions signs every browser out (credential change).
func (a *App) endAllSessions() {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessions = nil
	a.session = ""
}

func sessionToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// requestIsHTTPS covers direct TLS and a TLS-terminating reverse proxy. A forged
// X-Forwarded-Proto can only make the client's own cookie stricter.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || subscriptionScheme(r) == "https"
}

func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: a.manager.panelPath(), HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func (a *App) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	a.setSessionCookie(w, r, "", -1)
}
