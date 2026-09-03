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
