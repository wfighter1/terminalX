// Package relay implements the terminalX relay: device pairing and auth,
// console login, control-plane JSON forwarding, data-plane frame routing by
// sid, SQLite metadata, audit, webhook notifications and the hosted web UI.
//
// The relay never parses frame payloads (proto.PeekHeader only) and never
// writes terminal content to disk.
package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/relay/store"
)

// Config configures a Server.
type Config struct {
	// AdminPassword is the single console password (phase 1). Required.
	AdminPassword string
	// PublicURL is the externally reachable base URL (used in webhook links).
	PublicURL string
	// WebFS serves the console; nil disables static hosting.
	WebFS fs.FS
	// AllowedOrigins are extra WebSocket origin patterns for /ws/client
	// (same-origin is always allowed). Example: "localhost:5173".
	AllowedOrigins []string
	// Logger defaults to slog.Default().
	Logger *slog.Logger

	// HeartbeatTimeout marks an agent offline when no heartbeat arrived for
	// this long. Default 45s.
	HeartbeatTimeout time.Duration
	// SweepInterval is how often offline detection runs. Default 5s.
	SweepInterval time.Duration
	// SessionTTL is the console cookie lifetime. Default 30 days.
	SessionTTL time.Duration
	// PairCodeTTL is the pairing code lifetime. Default 5 minutes.
	PairCodeTTL time.Duration
	// LockoutAfter failed pair attempts (per IP / per code) lock for LockoutFor.
	LockoutAfter int
	LockoutFor   time.Duration
	// Now overrides the clock (tests).
	Now func() time.Time
}

func (c *Config) defaults() {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.HeartbeatTimeout <= 0 {
		c.HeartbeatTimeout = 45 * time.Second
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = 5 * time.Second
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	if c.PairCodeTTL <= 0 {
		c.PairCodeTTL = 5 * time.Minute
	}
	if c.LockoutAfter <= 0 {
		c.LockoutAfter = 5
	}
	if c.LockoutFor <= 0 {
		c.LockoutFor = 15 * time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Server is the relay.
type Server struct {
	cfg   Config
	store *store.Store
	log   *slog.Logger
	mux   *http.ServeMux

	logins  *loginSessions
	limiter *lockout
	webhook *webhookNotifier

	// hub state (guarded by mu)
	mu          sync.Mutex
	devices     map[string]*deviceState    // deviceID -> state (non-revoked)
	clients     map[string]*clientConn     // clientID -> conn
	sidOwner    map[uint32]string          // sid -> deviceID
	attachments map[uint32]map[string]bool // sid -> clientID -> pendingSnapshot
	approvals   map[string]*proto.Approval // key -> cached approval

	closed chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

// New creates a Server backed by st. It loads devices and pending approvals
// from the store.
func New(cfg Config, st *store.Store) (*Server, error) {
	cfg.defaults()
	if cfg.AdminPassword == "" {
		return nil, fmt.Errorf("relay: admin password is required")
	}
	s := &Server{
		cfg:         cfg,
		store:       st,
		log:         cfg.Logger,
		mux:         http.NewServeMux(),
		logins:      newLoginSessions(cfg.SessionTTL, cfg.Now),
		limiter:     newLockout(cfg.LockoutAfter, cfg.LockoutFor, cfg.Now),
		devices:     map[string]*deviceState{},
		clients:     map[string]*clientConn{},
		sidOwner:    map[uint32]string{},
		attachments: map[uint32]map[string]bool{},
		approvals:   map[string]*proto.Approval{},
		closed:      make(chan struct{}),
	}
	s.webhook = newWebhookNotifier(s.log, cfg.PublicURL, func(ctx context.Context) (string, error) {
		return st.GetSetting(ctx, settingWebhookURL)
	})
	if err := s.loadState(context.Background()); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) loadState(ctx context.Context) error {
	devs, err := s.store.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("relay: load devices: %w", err)
	}
	for _, d := range devs {
		s.devices[d.ID] = newDeviceState(d)
	}
	pend, err := s.store.ListApprovals(ctx, true, 0)
	if err != nil {
		return fmt.Errorf("relay: load approvals: %w", err)
	}
	for i := range pend {
		a := pend[i]
		s.approvals[a.Key] = &a
	}
	return nil
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.handleHealth)

	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)
	m.HandleFunc("GET /api/me", s.handleMe)
	m.HandleFunc("POST /api/pair/redeem", s.handlePairRedeem)

	m.Handle("POST /api/pair/new", s.requireLogin(s.handlePairNew))
	m.Handle("GET /api/devices", s.requireLogin(s.handleDevices))
	m.Handle("PATCH /api/devices/{id}", s.requireLogin(s.handleDeviceRename))
	m.Handle("DELETE /api/devices/{id}", s.requireLogin(s.handleDeviceRevoke))
	m.Handle("GET /api/approvals", s.requireLogin(s.handleApprovals))
	m.Handle("POST /api/approvals/{key}/decide", s.requireLogin(s.handleApprovalDecide))
	m.Handle("GET /api/audit", s.requireLogin(s.handleAudit))
	m.Handle("GET /api/settings/webhook", s.requireLogin(s.handleWebhookGet))
	m.Handle("PUT /api/settings/webhook", s.requireLogin(s.handleWebhookPut))
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown api route")
	})

	m.HandleFunc("/ws/agent", s.handleAgentWS)
	m.HandleFunc("/ws/client", s.handleClientWS)
	m.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown ws route")
	})

	m.Handle("/", s.staticHandler())
}

// Handler returns the HTTP handler (API + WS + static).
func (s *Server) Handler() http.Handler { return s.mux }

// Start launches background tasks (offline sweeper). Call Close to stop.
func (s *Server) Start() {
	s.wg.Add(1)
	go s.sweeper()
}

// Close stops background tasks and closes every WebSocket connection.
func (s *Server) Close() {
	s.once.Do(func() { close(s.closed) })
	s.wg.Wait()
	s.mu.Lock()
	var conns []interface{ closeNow(websocket.StatusCode, string) }
	for _, d := range s.devices {
		if d.conn != nil {
			conns = append(conns, d.conn)
		}
	}
	for _, c := range s.clients {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.closeNow(websocket.StatusGoingAway, "relay shutting down")
	}
}

func (s *Server) now() time.Time { return s.cfg.Now() }

// sweeper marks agents offline when heartbeats stop.
func (s *Server) sweeper() {
	defer s.wg.Done()
	t := time.NewTicker(s.cfg.SweepInterval)
	defer t.Stop()
	for {
		select {
		case <-s.closed:
			return
		case <-t.C:
			s.sweepOnce()
		}
	}
}

// sweepOnce checks heartbeat deadlines; exported for tests via SweepNow.
func (s *Server) sweepOnce() {
	now := s.now()
	var stale []*agentConn
	s.mu.Lock()
	for _, d := range s.devices {
		if d.conn != nil && now.Sub(d.conn.lastHeartbeat) > s.cfg.HeartbeatTimeout {
			stale = append(stale, d.conn)
		}
	}
	s.mu.Unlock()
	for _, ac := range stale {
		s.log.Warn("agent heartbeat timeout, marking offline", "device_id", ac.deviceID)
		ac.closeNow(websocket.StatusPolicyViolation, "heartbeat timeout")
		// agentConn's read loop unregisters and broadcasts device.state.
	}
}

// SweepNow runs offline detection immediately (tests / admin).
func (s *Server) SweepNow() { s.sweepOnce() }

func randomID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("relay: crypto/rand: %w", err))
	}
	return prefix + hex.EncodeToString(b)
}
