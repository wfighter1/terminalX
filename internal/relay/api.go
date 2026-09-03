package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/relay/store"
)

const (
	settingWebhookURL = "webhook_url"
	maxBodyBytes      = 1 << 20
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		writeError(w, ae.status, ae.msg)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return errBadRequest("read body: " + err.Error())
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return errBadRequest("bad json: " + err.Error())
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	online := 0
	for _, d := range s.devices {
		if d.conn != nil {
			online++
		}
	}
	nc := len(s.clients)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "devices_online": online, "clients": nc})
}

// ---- auth ---------------------------------------------------------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	key := "login:" + clientIP(r)
	if s.limiter.locked(key) {
		writeError(w, http.StatusTooManyRequests, "too many failed logins, try again later")
		return
	}
	if !s.passwordOK(req.Password) {
		if s.limiter.fail(key) {
			s.log.Warn("login locked out", "ip", clientIP(r))
		}
		s.audit("anon:"+clientIP(r), "", 0, "login.failed", "")
		writeError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	s.limiter.reset(key)
	tok, ls := s.logins.create()
	s.setSessionCookie(w, r, tok, int(s.cfg.SessionTTL/time.Second))
	s.audit("web:"+ls.ID, "", 0, "login", clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "client_id": ls.ID, "expires_at": ls.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if ls := s.logins.get(c.Value); ls != nil {
			s.audit("web:"+ls.ID, "", 0, "logout", "")
		}
		s.logins.revoke(c.Value)
	}
	s.setSessionCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	ls := s.sessionFromRequest(r)
	if ls == nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "client_id": ls.ID, "expires_at": ls.ExpiresAt})
}

// ---- pairing ------------------------------------------------------------

func (s *Server) handlePairNew(w http.ResponseWriter, r *http.Request) {
	ls := s.sessionFromRequest(r)
	now := s.now()
	_ = s.store.PurgePairCodes(r.Context(), now.Add(-24*time.Hour))
	var code string
	for i := 0; i < 5; i++ {
		code = newPairCode()
		err := s.store.CreatePairCode(r.Context(), store.PairCode{Code: code, CreatedAt: now, ExpiresAt: now.Add(s.cfg.PairCodeTTL)})
		if err == nil {
			break
		}
		if i == 4 {
			writeErr(w, err)
			return
		}
	}
	s.audit("web:"+ls.ID, "", 0, "pair.new", "")
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "expires_at": now.Add(s.cfg.PairCodeTTL)})
}

func (s *Server) handlePairRedeem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code         string `json:"code"`
		Name         string `json:"name"`
		OS           string `json:"os"`
		AgentVersion string `json:"agent_version"`
		Pubkey       string `json:"pubkey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	ip := clientIP(r)
	code := normalizePairCode(req.Code)
	ipKey, codeKey := "pair:ip:"+ip, "pair:code:"+code
	if s.limiter.locked(ipKey) || s.limiter.locked(codeKey) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, locked for 15 minutes")
		return
	}
	now := s.now()
	fail := func(reason string) {
		_ = s.store.IncPairAttempts(r.Context(), code)
		// count against both keys (no short-circuit: the per-code lock must
		// advance even when the IP just got locked)
		ipLocked := s.limiter.fail(ipKey)
		codeLocked := s.limiter.fail(codeKey)
		if ipLocked || codeLocked {
			s.log.Warn("pairing locked out", "ip", ip, "ip_locked", ipLocked, "code_locked", codeLocked)
		}
		s.audit("anon:"+ip, "", 0, "pair.failed", reason)
		writeError(w, http.StatusBadRequest, "invalid or expired pairing code")
	}
	if len(code) != 8 {
		fail("bad code format")
		return
	}
	if err := s.store.RedeemPairCode(r.Context(), code, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail("code not redeemable")
			return
		}
		writeErr(w, err)
		return
	}
	s.limiter.reset(ipKey)

	token, hash := newDeviceToken()
	id := randomID("dev_", 8)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "device-" + id[len(id)-4:]
	}
	material := req.Pubkey
	if material == "" {
		material = hash // no pubkey yet (phase 1): derive from the token hash
	}
	dev := store.Device{
		ID: id, Name: name, OS: req.OS, AgentVersion: req.AgentVersion, TokenHash: hash,
		Pubkey: req.Pubkey, Fingerprint: fingerprint(material), CreatedAt: now, LastSeen: now,
	}
	if err := s.store.CreateDevice(r.Context(), dev); err != nil {
		writeErr(w, err)
		return
	}
	s.mu.Lock()
	s.devices[id] = newDeviceState(dev)
	s.mu.Unlock()
	s.audit("anon:"+ip, id, 0, "pair.redeem", name+" "+dev.Fingerprint)
	s.log.Info("device paired", "device_id", id, "name", name, "fingerprint", dev.Fingerprint, "ip", ip)
	s.broadcastDeviceList()
	writeJSON(w, http.StatusOK, map[string]any{"device_id": id, "device_token": token, "fingerprint": dev.Fingerprint, "name": name})
}

// ---- devices ------------------------------------------------------------

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := s.deviceListLocked()
	s.mu.Unlock()
	if list == nil {
		list = []proto.DeviceInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

func (s *Server) handleDeviceRename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if err := s.store.RenameDevice(r.Context(), id, name); err != nil {
		writeErr(w, err)
		return
	}
	s.mu.Lock()
	if d := s.devices[id]; d != nil {
		d.info.Name = name
	}
	info, _ := s.deviceInfoLocked(id)
	s.mu.Unlock()
	s.audit("web:"+s.sessionFromRequest(r).ID, id, 0, "device.rename", name)
	s.broadcastDeviceList()
	writeJSON(w, http.StatusOK, map[string]any{"device": info})
}

func (s *Server) handleDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.revokeDevice(id, "web:"+s.sessionFromRequest(r).ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- approvals ----------------------------------------------------------

func (s *Server) handleApprovals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var (
		list []proto.Approval
		err  error
	)
	switch status {
	case "pending":
		s.mu.Lock()
		list = s.pendingApprovalsLocked()
		s.mu.Unlock()
	case "all":
		list, err = s.store.ListApprovals(r.Context(), false, limit)
	default:
		writeError(w, http.StatusBadRequest, "status must be pending or all")
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []proto.Approval{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": list})
}

func (s *Server) handleApprovalDecide(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req struct {
		Decision string `json:"decision"`
		DeviceID string `json:"device_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	actor := "web:" + s.sessionFromRequest(r).ID
	if err := s.decideApproval(key, req.Decision, actor, req.DeviceID, ""); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": key, "decision": req.Decision})
}

// ---- audit / settings ---------------------------------------------------

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	list, err := s.store.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	if list == nil {
		list = []store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": list})
}

func (s *Server) handleWebhookGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetSetting(r.Context(), settingWebhookURL)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": u})
}

func (s *Server) handleWebhookPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	u := strings.TrimSpace(req.URL)
	if u != "" {
		p, err := url.Parse(u)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			writeError(w, http.StatusBadRequest, "url must be http(s)")
			return
		}
	}
	if err := s.store.SetSetting(r.Context(), settingWebhookURL, u); err != nil {
		writeErr(w, err)
		return
	}
	s.audit("web:"+s.sessionFromRequest(r).ID, "", 0, "settings.webhook", u)
	writeJSON(w, http.StatusOK, map[string]any{"url": u})
}

// WebhookURL returns the configured webhook URL (empty when unset).
func (s *Server) WebhookURL(ctx context.Context) (string, error) {
	return s.store.GetSetting(ctx, settingWebhookURL)
}
