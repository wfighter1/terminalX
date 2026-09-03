package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wfighter1/terminalX/internal/presets"
	"github.com/wfighter1/terminalX/internal/session"
)

// Doctor prints a diagnostic summary: config, shells, AI CLIs on PATH,
// relay reachability and the hooks port. It returns an error only when the
// config itself is unusable.
func Doctor(ctx context.Context, cfg *Config, cfgPath string, w io.Writer) error {
	p := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }
	p("tx-agent %s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
	p("")
	p("config: %s", cfgPath)
	p("  relay_url:     %s", orDash(cfg.RelayURL))
	p("  device_id:     %s", orDash(cfg.DeviceID))
	p("  name:          %s", orDash(cfg.Name))
	p("  agent_id:      %s", orDash(cfg.AgentID))
	p("  fingerprint:   %s", orDash(cfg.Fingerprint))
	p("  paired:        %v", cfg.DeviceToken != "")
	p("  hooks_port:    %d (0 = random, persisted after first run)", cfg.HooksPort)
	p("  hook_token:    %s", mask(cfg.HookToken))
	p("  default_shell: %s", orDash(cfg.DefaultShell))
	r := cfg.Resolver()
	p("  presets:       %s", strings.Join(r.Names(), ", "))
	for _, name := range []string{presets.MiniMax, presets.RelayStation} {
		if _, err := r.Resolve(name); err != nil {
			p("    %-14s not configured", name)
		} else {
			p("    %-14s ok", name)
		}
	}
	p("")
	p("shells:")
	path, kind, _, err := session.ResolveShell(cfg.DefaultShell)
	if err != nil {
		p("  default:       NOT FOUND (%v)", err)
	} else {
		p("  default:       %s (%s)", path, kind)
	}
	for _, sh := range session.CandidateShells() {
		if pth, err := exec.LookPath(sh); err == nil {
			p("  %-14s %s", sh, pth)
		} else {
			p("  %-14s -", sh)
		}
	}
	p("")
	p("AI CLIs on PATH:")
	for _, tool := range []string{"claude", "codex", "grok"} {
		if pth, err := exec.LookPath(tool); err == nil {
			p("  %-14s %s", tool, pth)
		} else {
			p("  %-14s not found", tool)
		}
	}
	if _, err := exec.LookPath(curlName()); err != nil {
		p("  %-14s not found (Claude statusLine metrics need it)", curlName())
	} else {
		p("  %-14s ok", curlName())
	}
	p("")
	p("relay:")
	if cfg.RelayURL == "" {
		p("  not configured")
	} else if base, err := cfg.HTTPURL(); err != nil {
		p("  bad relay_url: %v", err)
	} else {
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(hctx, http.MethodGet, base+"/healthz", nil)
		start := time.Now()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			p("  %s/healthz: UNREACHABLE (%v)", base, err)
		} else {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			p("  %s/healthz: %d in %s %s", base, resp.StatusCode, time.Since(start).Round(time.Millisecond), strings.TrimSpace(string(body)))
		}
		if ws, err := cfg.WSURL(); err == nil {
			p("  ws endpoint:   %s", ws)
		}
	}
	p("")
	p("hooks endpoint:")
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.HooksPort)))
	if err != nil {
		p("  127.0.0.1:%d: cannot bind (%v) — probably the running agent holds it", cfg.HooksPort, err)
		if cfg.HooksPort != 0 {
			if code, ok := probeHooks(ctx, cfg.HooksPort); ok {
				p("  running agent answers /healthz: %d", code)
			}
		}
	} else {
		p("  127.0.0.1:%d: bindable", ln.Addr().(*net.TCPAddr).Port)
		ln.Close()
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func mask(s string) string {
	if len(s) <= 6 {
		if s == "" {
			return "-"
		}
		return "***"
	}
	return s[:3] + "…" + s[len(s)-3:]
}

func curlName() string {
	if runtime.GOOS == "windows" {
		return "curl.exe"
	}
	return "curl"
}

// probeHooks asks a running agent's hooks endpoint for /healthz.
func probeHooks(ctx context.Context, port int) (int, bool) {
	hctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		return 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, false
	}
	resp.Body.Close()
	return resp.StatusCode, true
}
