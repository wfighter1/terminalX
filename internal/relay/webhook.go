package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

// webhookNotifier POSTs approval notifications to the configured URL.
type webhookNotifier struct {
	log       *slog.Logger
	publicURL string
	getURL    func(ctx context.Context) (string, error)
	client    *http.Client
	attempts  int
}

// WebhookPayload is the JSON body sent to the webhook URL.
type WebhookPayload struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Device  string `json:"device"`
	Session string `json:"session"`
	Key     string `json:"key"`
	URL     string `json:"url"`
}

func newWebhookNotifier(log *slog.Logger, publicURL string, getURL func(ctx context.Context) (string, error)) *webhookNotifier {
	return &webhookNotifier{
		log: log, publicURL: strings.TrimRight(publicURL, "/"), getURL: getURL,
		client: &http.Client{Timeout: 10 * time.Second}, attempts: 3,
	}
}

// notifyApproval builds the payload and sends it asynchronously.
func (n *webhookNotifier) notifyApproval(a proto.Approval, deviceName, sessionName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	target, err := n.getURL(ctx)
	cancel()
	if err != nil {
		n.log.Error("webhook: read setting", "err", err)
		return
	}
	if target == "" {
		return
	}
	if deviceName == "" {
		deviceName = a.DeviceID
	}
	if sessionName == "" {
		sessionName = fmt.Sprintf("sid:%d", a.SID)
	}
	title := fmt.Sprintf("需要你确认：%s %s", a.Agent, a.Tool)
	body := a.Summary
	if a.Cwd != "" {
		body += "\n" + a.Cwd
	}
	if a.Level == proto.LevelSuspect {
		title = "疑似等待输入：" + sessionName
	}
	p := WebhookPayload{Title: title, Body: body, Device: deviceName, Session: sessionName, Key: a.Key}
	if n.publicURL != "" {
		p.URL = n.publicURL + "/?approval=" + a.Key
	}
	go n.post(target, p)
}

// post tries up to n.attempts times; failures are only logged.
func (n *webhookNotifier) post(target string, p WebhookPayload) {
	data, err := json.Marshal(p)
	if err != nil {
		n.log.Error("webhook: marshal", "err", err)
		return
	}
	var lastErr error
	for i := 0; i < n.attempts; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 2 * time.Second)
		}
		req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(data))
		if err != nil {
			n.log.Error("webhook: build request", "err", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := n.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		lastErr = fmt.Errorf("status %d", resp.StatusCode)
	}
	n.log.Warn("webhook: giving up", "key", p.Key, "attempts", n.attempts, "err", lastErr)
}
