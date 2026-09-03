// Package store is the relay's SQLite metadata store. It holds devices, pair
// codes, approvals, the audit log and settings. Terminal content is never
// written here: the relay only ever sees opaque frame payloads and does not
// persist them.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/wfighter1/terminalX/internal/proto"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and runs migrations.
// Use ":memory:" for an in-memory database (tests).
func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: create data dir: %w", err)
		}
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// A single connection keeps writes serialised (SQLite allows one writer)
	// and makes ":memory:" behave as one database rather than one per conn.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle (tests / maintenance).
func (s *Store) DB() *sql.DB { return s.db }

const schema = `
CREATE TABLE IF NOT EXISTS devices (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	os            TEXT NOT NULL DEFAULT '',
	agent_version TEXT NOT NULL DEFAULT '',
	token_hash    TEXT NOT NULL DEFAULT '',
	pubkey        TEXT NOT NULL DEFAULT '',
	fingerprint   TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL,
	last_seen     TEXT NOT NULL DEFAULT '',
	revoked_at    TEXT
);
CREATE INDEX IF NOT EXISTS devices_token_hash ON devices(token_hash);

CREATE TABLE IF NOT EXISTS pair_codes (
	code       TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	used_at    TEXT,
	attempts   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS approvals (
	key             TEXT PRIMARY KEY,
	device_id       TEXT NOT NULL,
	sid             INTEGER NOT NULL,
	agent           TEXT NOT NULL DEFAULT '',
	tool            TEXT NOT NULL DEFAULT '',
	summary         TEXT NOT NULL DEFAULT '',
	input_json      TEXT NOT NULL DEFAULT '',
	cwd             TEXT NOT NULL DEFAULT '',
	level           TEXT NOT NULL DEFAULT '',
	mode            TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	hook_timeout_at TEXT,
	status          TEXT NOT NULL,
	decided_by      TEXT NOT NULL DEFAULT '',
	decided_at      TEXT
);
CREATE INDEX IF NOT EXISTS approvals_status ON approvals(status, created_at);

CREATE TABLE IF NOT EXISTS audit (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	at        TEXT NOT NULL,
	actor     TEXT NOT NULL,
	device_id TEXT NOT NULL DEFAULT '',
	sid       INTEGER NOT NULL DEFAULT 0,
	action    TEXT NOT NULL,
	detail    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS settings (
	k TEXT PRIMARY KEY,
	v TEXT NOT NULL
);
`

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// ---- time helpers -------------------------------------------------------

const timeLayout = time.RFC3339Nano

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeLayout)
}

func fmtTimePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return fmtTime(*t)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

// ---- devices ------------------------------------------------------------

// Device is a paired agent.
type Device struct {
	ID           string
	Name         string
	OS           string
	AgentVersion string
	TokenHash    string
	Pubkey       string
	Fingerprint  string
	CreatedAt    time.Time
	LastSeen     time.Time
	RevokedAt    *time.Time
}

// CreateDevice inserts a device.
func (s *Store) CreateDevice(ctx context.Context, d Device) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO devices
		(id, name, os, agent_version, token_hash, pubkey, fingerprint, created_at, last_seen, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.OS, d.AgentVersion, d.TokenHash, d.Pubkey, d.Fingerprint,
		fmtTime(d.CreatedAt), fmtTime(d.LastSeen), fmtTimePtr(d.RevokedAt))
	if err != nil {
		return fmt.Errorf("store: create device %s: %w", d.ID, err)
	}
	return nil
}

const deviceCols = `id, name, os, agent_version, token_hash, pubkey, fingerprint, created_at, last_seen, revoked_at`

func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var created, last string
	var revoked sql.NullString
	if err := row.Scan(&d.ID, &d.Name, &d.OS, &d.AgentVersion, &d.TokenHash, &d.Pubkey,
		&d.Fingerprint, &created, &last, &revoked); err != nil {
		return Device{}, err
	}
	d.CreatedAt = parseTime(created)
	d.LastSeen = parseTime(last)
	d.RevokedAt = parseTimePtr(revoked)
	return d, nil
}

// GetDevice returns a device by id (revoked ones included).
func (s *Store) GetDevice(ctx context.Context, id string) (Device, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices WHERE id = ?`, id)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("store: get device %s: %w", id, err)
	}
	return d, nil
}

// DeviceByTokenHash returns the non-revoked device owning the token hash.
func (s *Store) DeviceByTokenHash(ctx context.Context, hash string) (Device, error) {
	if hash == "" {
		return Device{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceCols+` FROM devices
		WHERE token_hash = ? AND revoked_at IS NULL`, hash)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, fmt.Errorf("store: device by token: %w", err)
	}
	return d, nil
}

// ListDevices returns all non-revoked devices ordered by creation.
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceCols+` FROM devices
		WHERE revoked_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan device: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RenameDevice sets the display name.
func (s *Store) RenameDevice(ctx context.Context, id, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET name = ? WHERE id = ? AND revoked_at IS NULL`, name, id)
	if err != nil {
		return fmt.Errorf("store: rename device %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchDevice updates last_seen and, when non-empty, os / agent_version.
func (s *Store) TouchDevice(ctx context.Context, id string, at time.Time, osName, version string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE devices SET last_seen = ?,
		os = CASE WHEN ? = '' THEN os ELSE ? END,
		agent_version = CASE WHEN ? = '' THEN agent_version ELSE ? END
		WHERE id = ?`, fmtTime(at), osName, osName, version, version, id)
	if err != nil {
		return fmt.Errorf("store: touch device %s: %w", id, err)
	}
	return nil
}

// RevokeDevice marks the device revoked and destroys its token.
func (s *Store) RevokeDevice(ctx context.Context, id string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE devices SET revoked_at = ?, token_hash = ''
		WHERE id = ? AND revoked_at IS NULL`, fmtTime(at), id)
	if err != nil {
		return fmt.Errorf("store: revoke device %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- pair codes ---------------------------------------------------------

// PairCode is a one-time pairing code.
type PairCode struct {
	Code      string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	Attempts  int
}

// CreatePairCode inserts a new code.
func (s *Store) CreatePairCode(ctx context.Context, p PairCode) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pair_codes (code, created_at, expires_at, used_at, attempts)
		VALUES (?, ?, ?, ?, ?)`, p.Code, fmtTime(p.CreatedAt), fmtTime(p.ExpiresAt), fmtTimePtr(p.UsedAt), p.Attempts)
	if err != nil {
		return fmt.Errorf("store: create pair code: %w", err)
	}
	return nil
}

// GetPairCode looks a code up.
func (s *Store) GetPairCode(ctx context.Context, code string) (PairCode, error) {
	var p PairCode
	var created, expires string
	var used sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT code, created_at, expires_at, used_at, attempts
		FROM pair_codes WHERE code = ?`, code).Scan(&p.Code, &created, &expires, &used, &p.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return PairCode{}, ErrNotFound
	}
	if err != nil {
		return PairCode{}, fmt.Errorf("store: get pair code: %w", err)
	}
	p.CreatedAt = parseTime(created)
	p.ExpiresAt = parseTime(expires)
	p.UsedAt = parseTimePtr(used)
	return p, nil
}

// IncPairAttempts records a failed attempt against an existing code.
func (s *Store) IncPairAttempts(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pair_codes SET attempts = attempts + 1 WHERE code = ?`, code)
	if err != nil {
		return fmt.Errorf("store: inc pair attempts: %w", err)
	}
	return nil
}

// RedeemPairCode atomically marks the code used if it is unused and not yet
// expired at time now. It returns ErrNotFound when the code cannot be
// redeemed (missing, used or expired).
func (s *Store) RedeemPairCode(ctx context.Context, code string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE pair_codes SET used_at = ?
		WHERE code = ? AND used_at IS NULL AND expires_at > ?`, fmtTime(now), code, fmtTime(now))
	if err != nil {
		return fmt.Errorf("store: redeem pair code: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgePairCodes deletes codes that expired before cutoff.
func (s *Store) PurgePairCodes(ctx context.Context, cutoff time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pair_codes WHERE expires_at < ?`, fmtTime(cutoff))
	if err != nil {
		return fmt.Errorf("store: purge pair codes: %w", err)
	}
	return nil
}

// ---- approvals ----------------------------------------------------------

// UpsertApproval inserts or replaces an approval row.
func (s *Store) UpsertApproval(ctx context.Context, a proto.Approval) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approvals
		(key, device_id, sid, agent, tool, summary, input_json, cwd, level, mode, created_at, hook_timeout_at, status, decided_by, decided_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			device_id = excluded.device_id, sid = excluded.sid, agent = excluded.agent, tool = excluded.tool,
			summary = excluded.summary, input_json = excluded.input_json, cwd = excluded.cwd, level = excluded.level,
			mode = excluded.mode, created_at = excluded.created_at, hook_timeout_at = excluded.hook_timeout_at,
			status = excluded.status, decided_by = excluded.decided_by, decided_at = excluded.decided_at`,
		a.Key, a.DeviceID, int64(a.SID), a.Agent, a.Tool, a.Summary, string(a.Input), a.Cwd, string(a.Level), string(a.Mode),
		fmtTime(a.CreatedAt), fmtTimePtr(a.HookTimeoutAt), a.Status, a.DecidedBy, fmtTimePtr(a.DecidedAt))
	if err != nil {
		return fmt.Errorf("store: upsert approval %s: %w", a.Key, err)
	}
	return nil
}

// SetApprovalStatus updates status / decided_by / decided_at of one approval.
func (s *Store) SetApprovalStatus(ctx context.Context, key, status, by string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE approvals SET status = ?, decided_by = ?, decided_at = ? WHERE key = ?`,
		status, by, fmtTime(at), key)
	if err != nil {
		return fmt.Errorf("store: set approval status %s: %w", key, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const approvalCols = `key, device_id, sid, agent, tool, summary, input_json, cwd, level, mode, created_at, hook_timeout_at, status, decided_by, decided_at`

func scanApproval(row interface{ Scan(...any) error }) (proto.Approval, error) {
	var a proto.Approval
	var sid int64
	var input, level, mode, created string
	var hookTimeout, decidedAt sql.NullString
	if err := row.Scan(&a.Key, &a.DeviceID, &sid, &a.Agent, &a.Tool, &a.Summary, &input, &a.Cwd, &level, &mode,
		&created, &hookTimeout, &a.Status, &a.DecidedBy, &decidedAt); err != nil {
		return proto.Approval{}, err
	}
	a.SID = uint32(sid)
	if input != "" {
		a.Input = []byte(input)
	}
	a.Level = proto.ApprovalLevel(level)
	a.Mode = proto.ApprovalMode(mode)
	a.CreatedAt = parseTime(created)
	a.HookTimeoutAt = parseTimePtr(hookTimeout)
	a.DecidedAt = parseTimePtr(decidedAt)
	return a, nil
}

// GetApproval returns one approval by key.
func (s *Store) GetApproval(ctx context.Context, key string) (proto.Approval, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE key = ?`, key)
	a, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return proto.Approval{}, ErrNotFound
	}
	if err != nil {
		return proto.Approval{}, fmt.Errorf("store: get approval %s: %w", key, err)
	}
	return a, nil
}

// ListApprovals returns approvals, pending only or all, newest first, at most limit rows.
func (s *Store) ListApprovals(ctx context.Context, pendingOnly bool, limit int) ([]proto.Approval, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + approvalCols + ` FROM approvals`
	args := []any{}
	if pendingOnly {
		q += ` WHERE status = ?`
		args = append(args, proto.ApprovalPending)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list approvals: %w", err)
	}
	defer rows.Close()
	var out []proto.Approval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan approval: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- audit --------------------------------------------------------------

// AuditEntry is one row of the audit log (metadata only, never terminal content).
type AuditEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Actor    string    `json:"actor"`
	DeviceID string    `json:"device_id,omitempty"`
	SID      uint32    `json:"sid,omitempty"`
	Action   string    `json:"action"`
	Detail   string    `json:"detail,omitempty"`
}

// AppendAudit records an entry.
func (s *Store) AppendAudit(ctx context.Context, e AuditEntry) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, actor, device_id, sid, action, detail) VALUES (?, ?, ?, ?, ?, ?)`,
		fmtTime(e.At), e.Actor, e.DeviceID, int64(e.SID), e.Action, e.Detail)
	if err != nil {
		return fmt.Errorf("store: append audit: %w", err)
	}
	return nil
}

// ListAudit returns the newest entries, at most limit.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, actor, device_id, sid, action, detail FROM audit
		ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at string
		var sid int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.DeviceID, &sid, &e.Action, &e.Detail); err != nil {
			return nil, fmt.Errorf("store: scan audit: %w", err)
		}
		e.At = parseTime(at)
		e.SID = uint32(sid)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- settings -----------------------------------------------------------

// GetSetting returns the value for k, or "" when unset.
func (s *Store) GetSetting(ctx context.Context, k string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM settings WHERE k = ?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting %s: %w", k, err)
	}
	return v, nil
}

// SetSetting stores k=v.
func (s *Store) SetSetting(ctx context.Context, k, v string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (k, v) VALUES (?, ?)
		ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
	if err != nil {
		return fmt.Errorf("store: set setting %s: %w", k, err)
	}
	return nil
}
