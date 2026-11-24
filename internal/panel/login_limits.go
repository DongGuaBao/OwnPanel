package panel

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type loginAttempts struct {
	count int
	until time.Time
}

type loginLimiter struct {
	mu          sync.Mutex
	entries     map[string]loginAttempts
	nextCleanup time.Time
}

const loginWindow = 15 * time.Minute
const maxLoginSources = 4096

func (l *loginLimiter) allow(host string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.nextCleanup) {
		for key, entry := range l.entries {
			if !now.Before(entry.until) {
				delete(l.entries, key)
			}
		}
		l.nextCleanup = now.Add(time.Minute)
	}
	entry, exists := l.entries[host]
	if !exists && len(l.entries) >= maxLoginSources {
		return false
	}
	if !now.Before(entry.until) {
		entry = loginAttempts{until: now.Add(loginWindow)}
	}
	if entry.count >= 10 {
		return false
	}
	entry.count++
	l.entries[host] = entry
	return true
}

func (l *loginLimiter) clear(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, host)
}

func sameRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http://"
	if r.TLS != nil {
		scheme = "https://"
	}
	return origin == scheme+r.Host
}

func loginSource(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
