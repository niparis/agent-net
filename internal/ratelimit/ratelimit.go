// Package ratelimit provides in-memory limits for the public listener.
package ratelimit

import (
	"container/list"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate permits Requests operations per Per. Address limits use token buckets
// that refill over Per. App limits apply to each rolling period of length Per.
type Rate struct {
	Requests int
	Per      time.Duration
}

// Config controls address and app limits. Non-positive fields use defaults.
// The default address rates are 120 MCP, 30 OAuth, and 60 page requests per
// minute. App defaults are 60 calls per minute and 30 messages per hour.
// Ten failures in ten minutes ban an address for an hour. Up to 10,000
// addresses and 10,000 apps are tracked; idle entries expire after at least
// one hour. See New for the memory and eviction behavior.
type Config struct {
	MCP, OAuth, Page      Rate
	AppCalls, AppMessages Rate
	FailureLimit          int
	FailureWindow         time.Duration
	BanDuration           time.Duration
	MaxAddresses, MaxApps int
	IdleTTL               time.Duration
}

type bucket struct {
	tokens      float64
	updated     time.Time
	initialized bool
}

type address struct {
	key         string
	seen        time.Time
	buckets     [3]bucket
	failures    []time.Time
	bannedUntil time.Time
}

type app struct {
	key     string
	seen    time.Time
	windows [2][]time.Time
}

// Limiter is safe for concurrent use. Construct it with New.
type Limiter struct {
	mu          sync.Mutex
	cfg         Config
	now         func() time.Time
	lastNow     time.Time
	hasNow      bool
	addressIdle time.Duration
	appIdle     time.Duration
	addresses   map[string]*list.Element
	addressLRU  list.List
	apps        map[string]*list.Element
	appLRU      list.List
}

// New creates a limiter. If now is nil, it uses time.Now.
//
// Address and app entries are each capped and evicted in least recently seen
// order. Eviction also forgets any ban or limit state in that entry. The app
// methods must therefore be called only with approved app IDs. Idle entries
// are removed on subsequent operations, without a background goroutine. Their
// retention is at least IdleTTL and long enough to preserve bans, failure
// windows, and bucket refill periods. In-memory state is lost on restart.
func New(cfg Config, now func() time.Time) *Limiter {
	cfg.MCP = defaultRate(cfg.MCP, Rate{120, time.Minute})
	cfg.OAuth = defaultRate(cfg.OAuth, Rate{30, time.Minute})
	cfg.Page = defaultRate(cfg.Page, Rate{60, time.Minute})
	cfg.AppCalls = defaultRate(cfg.AppCalls, Rate{60, time.Minute})
	cfg.AppMessages = defaultRate(cfg.AppMessages, Rate{30, time.Hour})
	if cfg.FailureLimit <= 0 {
		cfg.FailureLimit = 10
	}
	if cfg.FailureWindow <= 0 {
		cfg.FailureWindow = 10 * time.Minute
	}
	if cfg.BanDuration <= 0 {
		cfg.BanDuration = time.Hour
	}
	if cfg.MaxAddresses <= 0 {
		cfg.MaxAddresses = 10_000
	}
	if cfg.MaxApps <= 0 {
		cfg.MaxApps = 10_000
	}
	if cfg.IdleTTL <= 0 {
		cfg.IdleTTL = time.Hour
	}
	if now == nil {
		now = time.Now
	}
	return &Limiter{
		cfg:         cfg,
		now:         now,
		addressIdle: max(cfg.IdleTTL, cfg.FailureWindow, cfg.BanDuration, cfg.MCP.Per, cfg.OAuth.Per, cfg.Page.Per),
		appIdle:     max(cfg.IdleTTL, cfg.AppCalls.Per, cfg.AppMessages.Per),
		addresses:   make(map[string]*list.Element),
		apps:        make(map[string]*list.Element),
	}
}

func defaultRate(rate, fallback Rate) Rate {
	if rate.Requests <= 0 {
		rate.Requests = fallback.Requests
	}
	if rate.Per <= 0 {
		rate.Per = fallback.Per
	}
	return rate
}

// current is called under mu. Clamping a clock that goes backwards keeps the
// LRU ordered and prevents a clock adjustment from refilling buckets twice.
func (l *Limiter) current() time.Time {
	now := l.now()
	if l.hasNow && now.Before(l.lastNow) {
		return l.lastNow
	}
	l.lastNow = now
	l.hasNow = true
	return now
}

func (b *bucket) allow(rate Rate, now time.Time) (bool, time.Duration) {
	capacity := float64(rate.Requests)
	if !b.initialized {
		b.tokens = capacity
		b.initialized = true
	} else {
		elapsed := now.Sub(b.updated)
		b.tokens = min(capacity, b.tokens+float64(elapsed)*capacity/float64(rate.Per))
	}
	b.updated = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	nanos := math.Ceil((1 - b.tokens) * float64(rate.Per) / capacity)
	if nanos >= float64(math.MaxInt64) {
		return false, time.Duration(math.MaxInt64)
	}
	return false, max(time.Duration(nanos), time.Nanosecond)
}

func allowWindow(events *[]time.Time, rate Rate, now time.Time) (bool, time.Duration) {
	cutoff := now.Add(-rate.Per)
	first := 0
	for first < len(*events) && !(*events)[first].After(cutoff) {
		first++
	}
	if first == len(*events) {
		*events = (*events)[:0]
	} else {
		*events = (*events)[first:]
	}
	if len(*events) >= rate.Requests {
		return false, (*events)[0].Add(rate.Per).Sub(now)
	}
	*events = append(*events, now)
	return true, 0
}

func (l *Limiter) pruneAddresses(now time.Time) {
	for e := l.addressLRU.Back(); e != nil; e = l.addressLRU.Back() {
		a := e.Value.(*address)
		if now.Sub(a.seen) < l.addressIdle {
			break
		}
		delete(l.addresses, a.key)
		l.addressLRU.Remove(e)
	}
}

func (l *Limiter) touchAddress(e *list.Element, now time.Time) *address {
	a := e.Value.(*address)
	a.seen = now
	l.addressLRU.MoveToFront(e)
	return a
}

func (l *Limiter) getAddress(key string, now time.Time) *address {
	l.pruneAddresses(now)
	if e := l.addresses[key]; e != nil {
		return l.touchAddress(e, now)
	}
	if len(l.addresses) >= l.cfg.MaxAddresses {
		e := l.addressLRU.Back()
		delete(l.addresses, e.Value.(*address).key)
		l.addressLRU.Remove(e)
	}
	a := &address{key: key, seen: now}
	l.addresses[key] = l.addressLRU.PushFront(a)
	return a
}

func (l *Limiter) pruneApps(now time.Time) {
	for e := l.appLRU.Back(); e != nil; e = l.appLRU.Back() {
		a := e.Value.(*app)
		if now.Sub(a.seen) < l.appIdle {
			break
		}
		delete(l.apps, a.key)
		l.appLRU.Remove(e)
	}
}

func (l *Limiter) getApp(key string, now time.Time) *app {
	l.pruneApps(now)
	if e := l.apps[key]; e != nil {
		a := e.Value.(*app)
		a.seen = now
		l.appLRU.MoveToFront(e)
		return a
	}
	if len(l.apps) >= l.cfg.MaxApps {
		e := l.appLRU.Back()
		delete(l.apps, e.Value.(*app).key)
		l.appLRU.Remove(e)
	}
	a := &app{key: key, seen: now}
	l.apps[key] = l.appLRU.PushFront(a)
	return a
}

// Allow checks the ban and the address bucket for class "mcp", "oauth", or
// "page". An unknown class is refused without creating state. A refused
// request returns the time until a token is available or the ban expires.
func (l *Limiter) Allow(addr, class string) (bool, time.Duration) {
	var index int
	var rate Rate
	switch class {
	case "mcp":
		index, rate = 0, l.cfg.MCP
	case "oauth":
		index, rate = 1, l.cfg.OAuth
	case "page":
		index, rate = 2, l.cfg.Page
	default:
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.current()
	a := l.getAddress(addr, now)
	if now.Before(a.bannedUntil) {
		return false, a.bannedUntil.Sub(now)
	}
	return a.buckets[index].allow(rate, now)
}

// Fail records a bad token, registration, or code. It reports whether the
// address is banned, including when it was already banned. Failures during a
// ban do not extend it. Failures at least FailureWindow old are discarded.
func (l *Limiter) Fail(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.current()
	a := l.getAddress(addr, now)
	if now.Before(a.bannedUntil) {
		return true
	}
	cutoff := now.Add(-l.cfg.FailureWindow)
	first := 0
	for first < len(a.failures) && !a.failures[first].After(cutoff) {
		first++
	}
	a.failures = append(a.failures[first:], now)
	if len(a.failures) >= l.cfg.FailureLimit {
		a.failures = nil
		a.bannedUntil = now.Add(l.cfg.BanDuration)
		return true
	}
	return false
}

// Banned reports whether addr is currently banned. Looking up an unseen
// address does not create an entry.
func (l *Limiter) Banned(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.addresses[addr]
	if e == nil {
		return false
	}
	now := l.current()
	a := l.touchAddress(e, now)
	return now.Before(a.bannedUntil)
}

// AllowApp checks the rolling window for an approved app ID and kind "call"
// or "message". An unknown kind is refused without creating state.
func (l *Limiter) AllowApp(appID, kind string) (bool, time.Duration) {
	var index int
	var rate Rate
	switch kind {
	case "call":
		index, rate = 0, l.cfg.AppCalls
	case "message":
		index, rate = 1, l.cfg.AppMessages
	default:
		return false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.current()
	return allowWindow(&l.getApp(appID, now).windows[index], rate, now)
}

// Middleware applies the address ban and class limit before calling next.
// Refusals use HTTP 429 and Retry-After rounded up to whole seconds.
func Middleware(l *Limiter, class string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.Allow(ClientAddr(r), class); !ok {
			seconds := int64(1)
			if retry > 0 {
				seconds = int64((retry-1)/time.Second) + 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientAddr uses the last entry of the last X-Forwarded-For header only for a
// loopback peer. The tunnel appends the client address; earlier entries may be
// supplied by the client and spoofed. Headers from other peers are ignored.
// If the last entry is not an IP address, the remote host is returned.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Unmap().IsLoopback() {
		values := r.Header.Values("X-Forwarded-For")
		if len(values) > 0 {
			forwarded := values[len(values)-1]
			if i := strings.LastIndexByte(forwarded, ','); i >= 0 {
				forwarded = forwarded[i+1:]
			}
			forwarded = strings.TrimSpace(forwarded)
			if _, err := netip.ParseAddr(forwarded); err == nil {
				return forwarded
			}
		}
	}
	return host
}
