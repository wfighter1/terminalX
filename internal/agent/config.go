// Package agent implements the terminalX controlled-side agent: it owns the
// PTY sessions, keeps one outbound WebSocket to the relay, runs the
// 127.0.0.1 hooks endpoint and injects vendor presets into AI CLIs.
package agent

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wfighter1/terminalX/internal/presets"
)

// Secrets hold the vendor credentials the built-in presets draw from. They
// never leave the machine.
type Secrets struct {
	MiniMaxAPIKey       string `json:"minimax_api_key,omitempty"`
	MiniMaxBaseURL      string `json:"minimax_base_url,omitempty"`
	RelayStationBaseURL string `json:"relay_station_base_url,omitempty"`
	RelayStationToken   string `json:"relay_station_token,omitempty"`
}

// Config is agent.json.
type Config struct {
	RelayURL    string `json:"relay_url"`
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	// PrivKey / PubKey are the X25519 key pair generated at pairing
	// (base64, raw 32 bytes). Reserved for the 1.1 end-to-end encryption.
	PrivKey     string `json:"privkey,omitempty"`
	PubKey      string `json:"pubkey,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// HooksPort is the 127.0.0.1 hooks port; 0 picks a random one which is
	// then persisted after the first run.
	HooksPort int    `json:"hooks_port"`
	HookToken string `json:"hook_token"`
	// Presets are user-defined vendor presets: name → env → value.
	Presets      map[string]map[string]string `json:"presets,omitempty"`
	Secrets      Secrets                      `json:"secrets"`
	DefaultShell string                       `json:"default_shell,omitempty"`
	// Persist selects the session-persistence backend so a session outlives
	// the agent process: "auto" (default) uses tmux when it is installed,
	// "off" disables it. Windows has no backend yet, so it is always off
	// there.
	Persist string `json:"persist,omitempty"`
}

// PersistEnabled reports whether sessions should be hosted in tmux.
func (c *Config) PersistEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(c.Persist)) {
	case "off", "false", "no":
		return false
	default:
		return true
	}
}

// DefaultPath returns $HOME/.terminalx/agent.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent: home dir: %w", err)
	}
	return filepath.Join(home, ".terminalx", "agent.json"), nil
}

// Load reads the config at path. A missing file yields an empty Config and
// os.ErrNotExist wrapped in the error.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return &Config{}, fmt.Errorf("agent: read config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("agent: parse config %s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config with mode 0600 (directory 0700) via a temp file
// and rename.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agent: create %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("agent: encode config: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("agent: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("agent: rename %s: %w", path, err)
	}
	return nil
}

// Validate checks that the config is usable for `run`.
func (c *Config) Validate() error {
	if c.RelayURL == "" {
		return errors.New("relay_url is empty (run `tx-agent pair` first)")
	}
	if c.DeviceToken == "" || c.DeviceID == "" {
		return errors.New("device is not paired (run `tx-agent pair` first)")
	}
	if _, err := c.WSURL(); err != nil {
		return err
	}
	return nil
}

// WSURL turns relay_url into the /ws/agent WebSocket URL.
func (c *Config) WSURL() (string, error) {
	u, err := url.Parse(strings.TrimRight(c.RelayURL, "/"))
	if err != nil {
		return "", fmt.Errorf("agent: bad relay_url %q: %w", c.RelayURL, err)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("agent: relay_url %q must be http(s) or ws(s)", c.RelayURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("agent: relay_url %q has no host", c.RelayURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/agent"
	u.RawQuery = ""
	return u.String(), nil
}

// HTTPURL normalises relay_url for REST calls (ws → http).
func (c *Config) HTTPURL() (string, error) {
	u, err := url.Parse(strings.TrimRight(c.RelayURL, "/"))
	if err != nil {
		return "", fmt.Errorf("agent: bad relay_url %q: %w", c.RelayURL, err)
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("agent: relay_url %q must be http(s) or ws(s)", c.RelayURL)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// Resolver builds the preset resolver from the config.
func (c *Config) Resolver() presets.Resolver {
	return presets.Resolver{
		Secrets: presets.Secrets{
			MiniMaxAPIKey:       c.Secrets.MiniMaxAPIKey,
			MiniMaxBaseURL:      c.Secrets.MiniMaxBaseURL,
			RelayStationBaseURL: c.Secrets.RelayStationBaseURL,
			RelayStationAPIKey:  c.Secrets.RelayStationToken,
		},
		Custom: c.Presets,
	}
}

// EnsureIdentity fills agent_id, hook_token and the X25519 key pair when
// missing. It reports whether anything changed (so the caller can Save).
func (c *Config) EnsureIdentity() (changed bool, err error) {
	if c.AgentID == "" {
		c.AgentID = "agt_" + randomHex(8)
		changed = true
	}
	if c.HookToken == "" {
		c.HookToken = randomHex(24)
		changed = true
	}
	if c.PrivKey == "" || c.PubKey == "" {
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			return changed, fmt.Errorf("agent: generate x25519 key: %w", err)
		}
		c.PrivKey = base64.StdEncoding.EncodeToString(priv.Bytes())
		c.PubKey = base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
		changed = true
	}
	return changed, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("agent: crypto/rand: %w", err))
	}
	return hex.EncodeToString(b)
}
