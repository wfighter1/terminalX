package relay

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestNormalizePairCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a7k39qzp", "A7K39QZP"},
		{"A7K3-9QZP", "A7K39QZP"},
		{"  a7k3 9qzp\n", "A7K39QZP"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizePairCode(tc.in); got != tc.want {
			t.Errorf("normalizePairCode(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestPairCodeAndTokens(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := newPairCode()
		if len(c) != 8 || normalizePairCode(c) != c {
			t.Fatalf("bad code %q", c)
		}
		for _, r := range c {
			if !strings.ContainsRune(pairAlphabet, r) {
				t.Fatalf("code %q uses %q outside the alphabet", c, r)
			}
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes are not random enough: %d unique of 200", len(seen))
	}
	tok, hash := newDeviceToken()
	if tok == "" || hash != hashToken(tok) || len(hash) != 64 {
		t.Fatalf("token %q hash %q", tok, hash)
	}
	if fp := fingerprint("material"); len(fp) != 9 || fp[4] != '-' || fp != fingerprint("material") {
		t.Fatalf("fingerprint %q", fp)
	}
	if fingerprint("a") == fingerprint("b") {
		t.Fatalf("fingerprints collide")
	}
}

func TestLockout(t *testing.T) {
	clock := newFakeClock()
	l := newLockout(3, 10*time.Minute, clock.Now)

	steps := []struct {
		name    string
		advance time.Duration
		fail    bool // record a failure, else just query
		wantHit bool // fail() returned "just locked"
		locked  bool // locked() afterwards
	}{
		{"first fail", 0, true, false, false},
		{"second fail", time.Minute, true, false, false},
		{"third fail locks", time.Minute, true, true, true},
		{"still locked", 5 * time.Minute, false, false, true},
		{"lock expired", 5*time.Minute + time.Second, false, false, false},
		{"counter restarted", 0, true, false, false},
		{"stale window resets counter", 11 * time.Minute, true, false, false},
		{"second in new window", 0, true, false, false},
		{"third in new window locks", 0, true, true, true},
	}
	for _, s := range steps {
		clock.Advance(s.advance)
		if s.fail {
			if hit := l.fail("k"); hit != s.wantHit {
				t.Fatalf("%s: fail() = %v want %v", s.name, hit, s.wantHit)
			}
		}
		if got := l.locked("k"); got != s.locked {
			t.Fatalf("%s: locked() = %v want %v", s.name, got, s.locked)
		}
	}
	l.reset("k")
	if l.locked("k") {
		t.Fatalf("locked after reset")
	}
	if l.locked("other") {
		t.Fatalf("unknown key locked")
	}
}

func TestLoginSessions(t *testing.T) {
	clock := newFakeClock()
	ls := newLoginSessions(time.Hour, clock.Now)
	tok, sess := ls.create()
	if sess.ID == "" || !sess.ExpiresAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("session %+v", sess)
	}
	if ls.get(tok) != sess || ls.get("") != nil || ls.get("nope") != nil {
		t.Fatalf("lookup")
	}
	clock.Advance(time.Hour + time.Second)
	if ls.get(tok) != nil {
		t.Fatalf("expired session still valid")
	}
	tok2, _ := ls.create()
	ls.revoke(tok2)
	if ls.get(tok2) != nil {
		t.Fatalf("revoked session still valid")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		hdr    map[string]string
		want   string
	}{
		{"plain", "10.0.0.1:1234", nil, "10.0.0.1"},
		{"ipv6", "[::1]:1234", nil, "::1"},
		{"xff single", "10.0.0.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.5"}, "203.0.113.5"},
		{"xff chain", "10.0.0.1:1234", map[string]string{"X-Forwarded-For": "203.0.113.5, 10.0.0.2"}, "203.0.113.5"},
		{"x-real-ip", "10.0.0.1:1234", map[string]string{"X-Real-IP": " 198.51.100.7 "}, "198.51.100.7"},
		{"no port", "weird", nil, "weird"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			for k, v := range tc.hdr {
				r.Header.Set(k, v)
			}
			if got := clientIP(r); got != tc.want {
				t.Fatalf("clientIP = %q want %q", got, tc.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		name string
		auth string
		url  string
		want string
	}{
		{"bearer", "Bearer abc", "/ws/agent", "abc"},
		{"lowercase", "bearer abc ", "/ws/agent", "abc"},
		{"query", "", "/ws/agent?token=xyz", "xyz"},
		{"basic ignored", "Basic abc", "/ws/agent", ""},
		{"header wins", "Bearer h", "/ws/agent?token=q", "h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			if got := bearerToken(r); got != tc.want {
				t.Fatalf("bearerToken = %q want %q", got, tc.want)
			}
		})
	}
}

func TestStaticHandler(t *testing.T) {
	web := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app.js":        {Data: []byte("console.log(1)")},
		"manifest.webmanifest": {Data: []byte("{}")},
	}
	s := &Server{cfg: Config{WebFS: web}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.staticHandler()
	cases := []struct {
		name   string
		method string
		path   string
		status int
		body   string
		cache  string
	}{
		{"root", http.MethodGet, "/", http.StatusOK, "<html>app</html>", "no-cache"},
		{"asset", http.MethodGet, "/assets/app.js", http.StatusOK, "console.log(1)", "public, max-age=31536000, immutable"},
		{"manifest", http.MethodGet, "/manifest.webmanifest", http.StatusOK, "{}", "no-cache"},
		{"spa fallback", http.MethodGet, "/devices/dev_1", http.StatusOK, "<html>app</html>", "no-cache"},
		// ServeMux normally redirects ".." away before the handler; if one
		// still gets through, ServeFileFS refuses it rather than serving.
		{"traversal", http.MethodGet, "/../index.html", http.StatusBadRequest, "", ""},
		{"post refused", http.MethodPost, "/", http.StatusMethodNotAllowed, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status %d want %d", rec.Code, tc.status)
			}
			if tc.body != "" && rec.Body.String() != tc.body {
				t.Fatalf("body %q want %q", rec.Body.String(), tc.body)
			}
			if tc.cache != "" && rec.Header().Get("Cache-Control") != tc.cache {
				t.Fatalf("cache %q want %q", rec.Header().Get("Cache-Control"), tc.cache)
			}
		})
	}

	// no web bundle → 404 with a hint, never a panic
	s2 := &Server{cfg: Config{}, log: s.log}
	rec := httptest.NewRecorder()
	s2.staticHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no bundle: %d", rec.Code)
	}
}

func TestNewRequiresPassword(t *testing.T) {
	e := newTestEnv(t)
	if _, err := New(Config{}, e.st); err == nil {
		t.Fatalf("New without password succeeded")
	}
}
