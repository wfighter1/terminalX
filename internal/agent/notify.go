package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Notify forwards a Codex `notify` payload (the JSON Codex passes as the
// last argv element) to the local hooks endpoint:
// POST http://127.0.0.1:<port>/hook/codex/<sid>/notify.
func Notify(ctx context.Context, port int, sid uint32, token, payload string) error {
	payload = strings.TrimSpace(payload)
	if port <= 0 || sid == 0 || token == "" {
		return fmt.Errorf("agent notify: --port, --sid and --token are required")
	}
	if payload == "" || payload[0] != '{' {
		return fmt.Errorf("agent notify: last argument must be the JSON payload")
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://127.0.0.1:%d/hook/codex/%d/notify", port, sid)
	req, err := http.NewRequestWithContext(hctx, http.MethodPost, url, bytes.NewReader([]byte(payload)))
	if err != nil {
		return fmt.Errorf("agent notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent notify: post %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agent notify: hooks endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// ForwardHook relays a hook payload read from in to the local hooks endpoint
// and copies the response body to out. It backs `tx-agent hook`, which
// Claude runs as a command hook for the events it does not deliver over http
// (SessionStart) and for statusLine, so the machine needs no curl.
//
// event "statusline" targets POST /statusline/<sid>; any other value targets
// POST /hook/claude/<sid>/<event>.
func ForwardHook(ctx context.Context, port int, sid uint32, token, event string, in io.Reader, out io.Writer) error {
	if port <= 0 || sid == 0 || token == "" || event == "" {
		return fmt.Errorf("agent hook: --port, --sid, --token and --event are required")
	}
	body, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return fmt.Errorf("agent hook: read stdin: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hook/claude/%d/%s", port, sid, event)
	if event == "statusline" {
		url = fmt.Sprintf("http://127.0.0.1:%d/statusline/%d", port, sid)
	}
	// The PermissionRequest endpoint can block for the whole remote-first
	// window, so the client must not impose a shorter deadline; Claude's own
	// hook timeout is the bound.
	hctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent hook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("agent hook: post %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agent hook: hooks endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, 4<<20)); err != nil {
		return fmt.Errorf("agent hook: copy response: %w", err)
	}
	return nil
}
