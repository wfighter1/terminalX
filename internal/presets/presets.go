// Package presets resolves vendor presets into environment variables that
// the agent injects into AI CLI child processes on the local machine. The
// values never travel through the relay.
package presets

import (
	"fmt"
	"sort"
	"strings"
)

// Built-in preset names.
const (
	Anthropic    = "anthropic"
	MiniMax      = "minimax"
	RelayStation = "relay-station"
)

// Secrets are the config values the built-in presets draw from.
type Secrets struct {
	MiniMaxAPIKey       string
	MiniMaxBaseURL      string // optional override of the default endpoint
	RelayStationBaseURL string
	RelayStationAPIKey  string
}

// DefaultMiniMaxBaseURL is the Anthropic-compatible MiniMax endpoint. It is
// marked "to be verified" in the architecture doc and can be overridden via
// Secrets.MiniMaxBaseURL.
const DefaultMiniMaxBaseURL = "https://api.minimax.io/anthropic"

// Resolver combines built-in presets with user-defined ones from the config.
type Resolver struct {
	Secrets Secrets
	// Custom presets from agent.json: name → env → value. A custom preset
	// with the same name as a built-in one replaces it entirely.
	Custom map[string]map[string]string
}

// Names lists all available preset names, sorted.
func (r Resolver) Names() []string {
	set := map[string]bool{Anthropic: true, MiniMax: true, RelayStation: true}
	for k := range r.Custom {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Resolve returns the environment entries ("KEY=VALUE") for a preset. An
// empty name means "no preset" and yields no entries. Unknown presets or
// presets whose secrets are missing return an error so the session opener
// can report a clear message instead of starting a half-configured tool.
func (r Resolver) Resolve(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	if m, ok := r.Custom[name]; ok {
		return envList(m), nil
	}
	switch name {
	case Anthropic:
		return nil, nil
	case MiniMax:
		if r.Secrets.MiniMaxAPIKey == "" {
			return nil, fmt.Errorf("preset %q: minimax_api_key is not set in agent.json", name)
		}
		base := r.Secrets.MiniMaxBaseURL
		if base == "" {
			base = DefaultMiniMaxBaseURL
		}
		return envList(map[string]string{
			"ANTHROPIC_BASE_URL":   base,
			"ANTHROPIC_AUTH_TOKEN": r.Secrets.MiniMaxAPIKey,
		}), nil
	case RelayStation:
		if r.Secrets.RelayStationBaseURL == "" || r.Secrets.RelayStationAPIKey == "" {
			return nil, fmt.Errorf("preset %q: relay_station_base_url / relay_station_api_key are not set in agent.json", name)
		}
		return envList(map[string]string{
			"ANTHROPIC_BASE_URL":   r.Secrets.RelayStationBaseURL,
			"ANTHROPIC_AUTH_TOKEN": r.Secrets.RelayStationAPIKey,
		}), nil
	}
	return nil, fmt.Errorf("unknown preset %q (available: %s)", name, strings.Join(r.Names(), ", "))
}

func envList(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}
