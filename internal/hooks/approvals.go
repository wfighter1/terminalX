package hooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

// Decision is what a remote decider returns for a level-A approval.
type Decision struct {
	Decision string // allow | deny
	By       string // web:<client> | relay | …
}

type pending struct {
	a  proto.Approval
	ch chan Decision // non-nil for level A while the hook handler is blocked
}

// Store keeps the pending approvals of this agent, keyed by Approval.Key.
type Store struct {
	mu    sync.Mutex
	items map[string]*pending
}

// NewStore creates an empty approval store.
func NewStore() *Store { return &Store{items: map[string]*pending{}} }

// Add registers a pending approval. If an approval with the same key is
// already pending it is returned and dup is true (60 s dedupe window per the
// design doc: the key includes tool input, so an identical re-request is the
// same item).
func (s *Store) Add(a proto.Approval) (stored proto.Approval, dup bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.items[a.Key]; ok && p.a.Status == proto.ApprovalPending {
		if time.Since(p.a.CreatedAt) < 60*time.Second {
			return p.a, true
		}
	}
	a.Status = proto.ApprovalPending
	p := &pending{a: a}
	if a.Level == proto.LevelHook {
		p.ch = make(chan Decision, 1)
	}
	s.items[a.Key] = p
	return a, false
}

// Get returns an approval by key.
func (s *Store) Get(key string) (proto.Approval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[key]
	if !ok {
		return proto.Approval{}, false
	}
	return p.a, true
}

// Pending returns all pending approvals, oldest first.
func (s *Store) Pending() []proto.Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []proto.Approval
	for _, p := range s.items {
		if p.a.Status == proto.ApprovalPending {
			out = append(out, p.a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// PendingForSession returns pending approvals of one session.
func (s *Store) PendingForSession(sid uint32) []proto.Approval {
	var out []proto.Approval
	for _, a := range s.Pending() {
		if a.SID == sid {
			out = append(out, a)
		}
	}
	return out
}

// Decide applies a remote decision. For level-A approvals whose hook handler
// is still blocked, the handler is woken and returns the decision to the
// tool. The returned bool is false when the key is unknown or no longer
// pending.
func (s *Store) Decide(key, decision, by string) (proto.Approval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[key]
	if !ok || p.a.Status != proto.ApprovalPending {
		return proto.Approval{}, false
	}
	now := time.Now()
	if decision == "allow" {
		p.a.Status = proto.ApprovalAllowed
	} else {
		p.a.Status = proto.ApprovalDenied
	}
	p.a.DecidedBy = by
	p.a.DecidedAt = &now
	if p.ch != nil {
		select {
		case p.ch <- Decision{Decision: decision, By: by}:
		default:
		}
	}
	return p.a, true
}

// Close marks a pending approval with a terminal status (closed_local,
// fallback) without a remote decision. A blocked level-A handler is woken
// with an empty decision so it returns "no decision" to the tool.
func (s *Store) Close(key, status, by string) (proto.Approval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[key]
	if !ok || p.a.Status != proto.ApprovalPending {
		return proto.Approval{}, false
	}
	now := time.Now()
	p.a.Status = status
	p.a.DecidedBy = by
	p.a.DecidedAt = &now
	if p.ch != nil {
		select {
		case p.ch <- Decision{}:
		default:
		}
	}
	return p.a, true
}

// CloseSession closes every pending approval of a session (used when the
// session is closed or the tool exited).
func (s *Store) CloseSession(sid uint32, status, by string) []proto.Approval {
	var out []proto.Approval
	for _, a := range s.PendingForSession(sid) {
		if closed, ok := s.Close(a.Key, status, by); ok {
			out = append(out, closed)
		}
	}
	return out
}

// wait returns the decision channel of a level-A approval.
func (s *Store) wait(key string) (chan Decision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[key]
	if !ok || p.ch == nil {
		return nil, false
	}
	return p.ch, true
}

// Sweep drops finished approvals older than maxAge to bound memory.
func (s *Store) Sweep(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.items {
		if p.a.Status != proto.ApprovalPending && p.a.DecidedAt != nil && time.Since(*p.a.DecidedAt) > maxAge {
			delete(s.items, k)
		}
	}
}

// ApprovalKey computes hex(sha256(sessionID ‖ toolName ‖ canonical_json(toolInput)))[:16].
func ApprovalKey(sessionID, toolName string, toolInput json.RawMessage) string {
	canon := CanonicalJSON(toolInput)
	h := sha256.New()
	h.Write([]byte(sessionID))
	h.Write([]byte(toolName))
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// CanonicalJSON re-encodes JSON with object keys sorted and no insignificant
// whitespace. Numbers are preserved verbatim. Invalid JSON is returned
// trimmed as-is so a key can still be derived.
func CanonicalJSON(raw json.RawMessage) []byte {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []byte(strings.TrimSpace(string(raw)))
	}
	out, err := json.Marshal(v) // encoding/json sorts map keys
	if err != nil {
		return []byte(strings.TrimSpace(string(raw)))
	}
	return out
}

// Summary picks the human-readable line for an approval card.
func Summary(toolName string, toolInput json.RawMessage) string {
	var m map[string]any
	if err := json.Unmarshal(toolInput, &m); err == nil {
		switch toolName {
		case "Bash":
			if s, ok := m["command"].(string); ok && s != "" {
				return s
			}
		case "Write", "Edit", "Read", "MultiEdit", "NotebookEdit":
			if s, ok := m["file_path"].(string); ok && s != "" {
				return s
			}
		}
		// Generic fallbacks for other tools.
		for _, k := range []string{"command", "file_path", "path", "url", "pattern", "query"} {
			if s, ok := m[k].(string); ok && s != "" {
				return fmt.Sprintf("%s: %s", toolName, s)
			}
		}
	}
	return toolName
}
