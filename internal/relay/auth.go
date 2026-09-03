package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "tx_session"

// loginSession is an authenticated console session (in-memory only).
type loginSession struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type loginSessions struct {
	mu   sync.Mutex
	ttl  time.Duration
	now  func() time.Time
	sess map[string]*loginSession // token -> session
}

func newLoginSessions(ttl time.Duration, now func() time.Time) *loginSessions {
	return &loginSessions{ttl: ttl, now: now, sess: map[string]*loginSession{}}
}

// create returns the cookie token and the session.
func (l *loginSessions) create() (string, *loginSession) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("relay: crypto/rand: " + err.Error())
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	now := l.now()
	ls := &loginSession{ID: randomID("u-", 6), CreatedAt: now, ExpiresAt: now.Add(l.ttl)}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sess[tok] = ls
	// opportunistic purge
	for k, v := range l.sess {
		if now.After(v.ExpiresAt) {
			delete(l.sess, k)
		}
	}
	return tok, ls
}

func (l *loginSessions) get(tok string) *loginSession {
	if tok == "" {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ls, ok := l.sess[tok]
	if !ok {
		return nil
	}
	if l.now().After(ls.ExpiresAt) {
		delete(l.sess, tok)
		return nil
	}
	return ls
}

func (l *loginSessions) revoke(tok string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sess, tok)
}

// sessionFromRequest returns the login session for the request cookie, or nil.
func (s *Server) sessionFromRequest(r *http.Request) *loginSession {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.logins.get(c.Value)
}

func (s *Server) requireLogin(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.sessionFromRequest(r) == nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// passwordOK compares the password in constant time.
func (s *Server) passwordOK(pw string) bool {
	a := sha256.Sum256([]byte(pw))
	b := sha256.Sum256([]byte(s.cfg.AdminPassword))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// clientIP returns the caller IP, honouring X-Forwarded-For from a reverse proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// lockout counts failures per key and locks the key after N failures.
type lockout struct {
	mu    sync.Mutex
	after int
	for_  time.Duration
	now   func() time.Time
	ents  map[string]*lockEntry
}

type lockEntry struct {
	fails       int
	lastFail    time.Time
	lockedUntil time.Time
}

func newLockout(after int, d time.Duration, now func() time.Time) *lockout {
	return &lockout{after: after, for_: d, now: now, ents: map[string]*lockEntry{}}
}

// locked reports whether key is currently locked.
func (l *lockout) locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.ents[key]
	if !ok {
		return false
	}
	now := l.now()
	if now.Before(e.lockedUntil) {
		return true
	}
	if now.Sub(e.lastFail) > l.for_ {
		delete(l.ents, key)
	}
	return false
}

// fail records a failure; returns true when the key just got locked.
func (l *lockout) fail(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e, ok := l.ents[key]
	if !ok || now.Sub(e.lastFail) > l.for_ {
		e = &lockEntry{}
		l.ents[key] = e
	}
	e.fails++
	e.lastFail = now
	if e.fails >= l.after {
		e.lockedUntil = now.Add(l.for_)
		e.fails = 0
		return true
	}
	return false
}

// reset clears the counter for key (after success).
func (l *lockout) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ents, key)
}
