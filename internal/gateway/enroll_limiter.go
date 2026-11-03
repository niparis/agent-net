package gateway

import (
	"net"
	"strings"
	"sync"
	"time"
)

type enrollLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	window      time.Duration
	ipLimit     int
	globalLimit int

	ipAttempts       map[string][]time.Time
	ipLastLogged     map[string]time.Time
	globalAttempts   []time.Time
	globalLastLogged time.Time
}

func newEnrollLimiter(now func() time.Time) *enrollLimiter {
	if now == nil {
		now = time.Now
	}
	return &enrollLimiter{
		now:          now,
		window:       10 * time.Minute,
		ipLimit:      10,
		globalLimit:  60,
		ipAttempts:   make(map[string][]time.Time),
		ipLastLogged: make(map[string]time.Time),
	}
}

// Allow reports whether an enroll attempt from ip is allowed.
// If over the limit, it returns allowed=false.
// shouldLog is true only once per window for that limit.
func (l *enrollLimiter) Allow(remoteAddr string) (allowed bool, shouldLog bool) {
	ip := extractIP(remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-l.window)

	// Clean global
	var validGlobal []time.Time
	for _, t := range l.globalAttempts {
		if t.After(cutoff) {
			validGlobal = append(validGlobal, t)
		}
	}
	l.globalAttempts = validGlobal

	// Check global limit
	if len(l.globalAttempts) >= l.globalLimit {
		if l.globalLastLogged.IsZero() || now.Sub(l.globalLastLogged) >= l.window {
			l.globalLastLogged = now
			return false, true
		}
		return false, false
	}

	// Clean per-IP
	var validIP []time.Time
	for _, t := range l.ipAttempts[ip] {
		if t.After(cutoff) {
			validIP = append(validIP, t)
		}
	}
	if len(validIP) > 0 {
		l.ipAttempts[ip] = validIP
	} else {
		delete(l.ipAttempts, ip)
	}

	// Check IP limit
	if len(validIP) >= l.ipLimit {
		lastLogged := l.ipLastLogged[ip]
		if lastLogged.IsZero() || now.Sub(lastLogged) >= l.window {
			l.ipLastLogged[ip] = now
			return false, true
		}
		return false, false
	}

	// Allowed: record attempt
	l.globalAttempts = append(l.globalAttempts, now)
	l.ipAttempts[ip] = append(l.ipAttempts[ip], now)
	return true, false
}

func extractIP(addr string) string {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	return host
}
