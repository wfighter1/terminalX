package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestDevices(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	d := Device{ID: "dev_1", Name: "pc", OS: "windows", AgentVersion: "0.1", TokenHash: "h1", Fingerprint: "A7K3-9QZP", CreatedAt: now}
	if err := s.CreateDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeviceByTokenHash(ctx, "h1")
	if err != nil || got.ID != "dev_1" || !got.CreatedAt.Equal(now) {
		t.Fatalf("by token: %+v %v", got, err)
	}
	if err := s.RenameDevice(ctx, "dev_1", "laptop"); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchDevice(ctx, "dev_1", now.Add(time.Minute), "", "0.2"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDevice(ctx, "dev_1")
	if got.Name != "laptop" || got.AgentVersion != "0.2" || got.OS != "windows" || !got.LastSeen.Equal(now.Add(time.Minute)) {
		t.Fatalf("after update: %+v", got)
	}
	list, _ := s.ListDevices(ctx)
	if len(list) != 1 {
		t.Fatalf("list: %d", len(list))
	}
	if err := s.RevokeDevice(ctx, "dev_1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeviceByTokenHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked token still valid: %v", err)
	}
	if list, _ = s.ListDevices(ctx); len(list) != 0 {
		t.Fatalf("revoked still listed")
	}
	if err := s.RevokeDevice(ctx, "dev_1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke: %v", err)
	}
}

func TestPairCodes(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	cases := []struct {
		name    string
		code    PairCode
		at      time.Time
		wantErr bool
	}{
		{"valid", PairCode{Code: "AAAA2222", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}, now.Add(time.Minute), false},
		{"expired", PairCode{Code: "BBBB3333", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)}, now.Add(6 * time.Minute), true},
		{"used", PairCode{Code: "CCCC4444", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute), UsedAt: &now}, now.Add(time.Minute), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.CreatePairCode(ctx, tc.code); err != nil {
				t.Fatal(err)
			}
			err := s.RedeemPairCode(ctx, tc.code.Code, tc.at)
			if (err != nil) != tc.wantErr {
				t.Fatalf("redeem err=%v want error=%v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if err := s.RedeemPairCode(ctx, tc.code.Code, tc.at); !errors.Is(err, ErrNotFound) {
					t.Fatalf("second redeem should fail: %v", err)
				}
			}
		})
	}
	if err := s.RedeemPairCode(ctx, "NOPE0000", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing code: %v", err)
	}
	if err := s.IncPairAttempts(ctx, "AAAA2222"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetPairCode(ctx, "AAAA2222")
	if p.Attempts != 1 || p.UsedAt == nil {
		t.Fatalf("pair code: %+v", p)
	}
}

func TestApprovalsAuditSettings(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := proto.Approval{Key: "k1", SID: 7, DeviceID: "dev_1", Agent: "claude", Tool: "Bash", Summary: "go test",
		Input: []byte(`{"command":"go test"}`), Level: proto.LevelHook, Mode: proto.ApprovalRemoteFirst, CreatedAt: now, Status: proto.ApprovalPending}
	if err := s.UpsertApproval(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertApproval(ctx, a); err != nil {
		t.Fatalf("upsert twice: %v", err)
	}
	pend, _ := s.ListApprovals(ctx, true, 0)
	if len(pend) != 1 || pend[0].Key != "k1" || string(pend[0].Input) != `{"command":"go test"}` || pend[0].Level != proto.LevelHook {
		t.Fatalf("pending: %+v", pend)
	}
	if err := s.SetApprovalStatus(ctx, "k1", proto.ApprovalAllowed, "web:c1", now); err != nil {
		t.Fatal(err)
	}
	if pend, _ = s.ListApprovals(ctx, true, 0); len(pend) != 0 {
		t.Fatalf("still pending")
	}
	all, _ := s.ListApprovals(ctx, false, 0)
	if len(all) != 1 || all[0].Status != proto.ApprovalAllowed || all[0].DecidedBy != "web:c1" || all[0].DecidedAt == nil {
		t.Fatalf("all: %+v", all)
	}
	if err := s.SetApprovalStatus(ctx, "missing", proto.ApprovalDenied, "x", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing approval: %v", err)
	}

	if err := s.AppendAudit(ctx, AuditEntry{Actor: "web:u1", Action: "login"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendAudit(ctx, AuditEntry{Actor: "web:u1", DeviceID: "dev_1", SID: 7, Action: "session.open", Detail: "claude"}); err != nil {
		t.Fatal(err)
	}
	ents, _ := s.ListAudit(ctx, 10)
	if len(ents) != 2 || ents[0].Action != "session.open" || ents[0].SID != 7 || ents[0].At.IsZero() {
		t.Fatalf("audit: %+v", ents)
	}

	if v, _ := s.GetSetting(ctx, "webhook_url"); v != "" {
		t.Fatalf("unset setting: %q", v)
	}
	if err := s.SetSetting(ctx, "webhook_url", "https://a"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "webhook_url", "https://b"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetSetting(ctx, "webhook_url"); v != "https://b" {
		t.Fatalf("setting: %q", v)
	}
}
