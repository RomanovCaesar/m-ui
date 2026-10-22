package app

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// Failed sign-ins are limited per source IP, shared by the panel login and the
// client-device authorization page so one cannot be used to get around the other.
// Only the TCP peer address counts: X-Forwarded-For is set by the client and would
// let an attacker pick a fresh "IP" for every guess. Behind a reverse proxy every
// visitor shares the proxy's address; a successful sign-in clears it again.
const (
	loginFailWindow  = 10 * time.Minute
	loginMaxFails    = 5
	loginMaxTracked  = 4096
	loginFailMessage = "登录失败次数过多，请稍后再试"
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// recentLocked drops failures older than the window and returns what is left.
func (l *loginLimiter) recentLocked(ip string, now time.Time) []time.Time {
	recent := l.fails[ip][:0]
	for _, at := range l.fails[ip] {
		if now.Sub(at) < loginFailWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = recent
	return recent
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fails == nil {
		return false
	}
	return len(l.recentLocked(ip, time.Now())) >= loginMaxFails
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.fails == nil {
		l.fails = map[string][]time.Time{}
	}
	if _, tracked := l.fails[ip]; !tracked && len(l.fails) >= loginMaxTracked {
		// Keep memory bounded under a spray from many addresses: forget expired
		// entries first, and only then the oldest remaining one.
		for other := range l.fails {
			l.recentLocked(other, now)
		}
		if len(l.fails) >= loginMaxTracked {
			// Timestamps can tie at the clock's resolution, so the first entry is
			// always a candidate; otherwise nothing might be evicted at all.
			oldestIP, oldest := "", time.Time{}
			for other, times := range l.fails {
				if last := times[len(times)-1]; oldestIP == "" || last.Before(oldest) {
					oldestIP, oldest = other, last
				}
			}
			delete(l.fails, oldestIP)
		}
	}
	l.fails[ip] = append(l.recentLocked(ip, now), now)
}

func (l *loginLimiter) succeed(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
