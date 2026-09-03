package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// PairResult is what `tx-agent pair` prints.
type PairResult struct {
	DeviceID    string
	Name        string
	Fingerprint string // as computed by the relay
	LocalFP     string // computed here from the public key sent
}

// Pair redeems a pairing code at relayURL (POST /api/pair/redeem, see
// relay.handlePairRedeem) and stores the device credentials in cfg. The
// caller saves cfg.
func Pair(ctx context.Context, cfg *Config, relayURL, code, name string) (*PairResult, error) {
	cfg.RelayURL = strings.TrimRight(strings.TrimSpace(relayURL), "/")
	base, err := cfg.HTTPURL()
	if err != nil {
		return nil, err
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("agent: pairing code is required")
	}
	if _, err := cfg.EnsureIdentity(); err != nil {
		return nil, err
	}
	if name == "" {
		name = cfg.Name
	}
	req := map[string]string{
		"code":          code,
		"name":          name,
		"os":            runtime.GOOS,
		"agent_version": Version,
		"pubkey":        cfg.PubKey,
	}
	body, _ := json.Marshal(req)
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(hctx, http.MethodPost, base+"/api/pair/redeem", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agent: build pair request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("agent: pair request to %s: %w", base, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("agent: read pair response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return nil, fmt.Errorf("agent: pairing failed (%d): %s", resp.StatusCode, e.Error)
	}
	var out struct {
		DeviceID    string `json:"device_id"`
		DeviceToken string `json:"device_token"`
		Fingerprint string `json:"fingerprint"`
		Name        string `json:"name"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("agent: parse pair response: %w", err)
	}
	if out.DeviceID == "" || out.DeviceToken == "" {
		return nil, fmt.Errorf("agent: pair response missing device_id/device_token")
	}
	cfg.DeviceID = out.DeviceID
	cfg.DeviceToken = out.DeviceToken
	cfg.Fingerprint = out.Fingerprint
	if out.Name != "" {
		cfg.Name = out.Name
	} else {
		cfg.Name = name
	}
	return &PairResult{DeviceID: out.DeviceID, Name: cfg.Name, Fingerprint: out.Fingerprint, LocalFP: LocalFingerprint(cfg.PubKey)}, nil
}

// LocalFingerprint mirrors the relay's fingerprint(): first 8 base32 chars
// of sha256(material) as XXXX-XXXX, so both sides can be compared by eye.
func LocalFingerprint(material string) string {
	h := sha256.Sum256([]byte(material))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:])
	return enc[:4] + "-" + enc[4:8]
}
