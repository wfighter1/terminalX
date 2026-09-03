package agent

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
)

const (
	wsReadLimit  = 4 << 20
	sendQueueLen = 4096
	backoffMin   = 2 * time.Second
	backoffMax   = 16 * time.Second
	dialTimeout  = 15 * time.Second
)

// outMsg is one queued WebSocket message.
type outMsg struct {
	typ  websocket.MessageType
	data []byte
}

// wsConn is one live connection to the relay.
type wsConn struct {
	c      *websocket.Conn
	out    chan outMsg
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (w *wsConn) close(code websocket.StatusCode, reason string) {
	w.once.Do(func() {
		_ = w.c.Close(code, reason)
		w.cancel()
	})
}

// backoffDelay returns the reconnect delay for attempt n (0-based):
// 2 s → 4 s → 8 s → 16 s with ±25 % jitter.
func backoffDelay(n int) time.Duration {
	d := backoffMin
	for i := 0; i < n && d < backoffMax; i++ {
		d *= 2
	}
	if d > backoffMax {
		d = backoffMax
	}
	jitter := time.Duration((rand.Float64()*0.5 - 0.25) * float64(d))
	return d + jitter
}

// connectLoop dials the relay, runs the connection and reconnects with
// exponential backoff until ctx is cancelled.
func (a *Agent) connectLoop(ctx context.Context) error {
	wsURL, err := a.cfg.WSURL()
	if err != nil {
		return err
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := a.runConn(ctx, wsURL)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.mu.Lock()
		ok := a.helloOK
		a.helloOK = false
		a.mu.Unlock()
		if ok {
			attempt = 0
		}
		d := backoffDelay(attempt)
		attempt++
		a.log.Warn("relay connection lost, reconnecting", "err", err, "in", d.Round(100*time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// runConn dials, sends agent.hello and pumps messages until the
// connection drops.
func (a *Agent) runConn(ctx context.Context, wsURL string) error {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+a.cfg.DeviceToken)
	hdr.Set("User-Agent", "tx-agent/"+Version)
	c, resp, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{
		HTTPHeader:      hdr,
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errors.New("relay rejected the device token (revoked? re-pair with `tx-agent pair`)")
		}
		return err
	}
	c.SetReadLimit(wsReadLimit)
	cctx, ccancel := context.WithCancel(ctx)
	w := &wsConn{c: c, out: make(chan outMsg, sendQueueLen), ctx: cctx, cancel: ccancel}
	helloID := "hello-" + randomHex(4)
	hello := a.helloMsg(helloID)
	a.mu.Lock()
	a.conn = w
	a.helloID = helloID
	a.mu.Unlock()
	a.log.Info("connected to relay", "url", wsURL, "sessions", len(hello.Sessions))

	go a.writeLoop(w)
	a.sendMsg(hello)
	a.sendHeartbeat()
	err = a.readLoop(w)
	w.close(websocket.StatusNormalClosure, "")
	a.mu.Lock()
	if a.conn == w {
		a.conn = nil
	}
	a.mu.Unlock()
	return err
}

func (a *Agent) writeLoop(w *wsConn) {
	for {
		select {
		case <-w.ctx.Done():
			return
		case m := <-w.out:
			wctx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
			err := w.c.Write(wctx, m.typ, m.data)
			cancel()
			if err != nil {
				w.close(websocket.StatusAbnormalClosure, "write failed")
				return
			}
		}
	}
}

func (a *Agent) readLoop(w *wsConn) error {
	for {
		typ, data, err := w.c.Read(w.ctx)
		if err != nil {
			return err
		}
		switch typ {
		case websocket.MessageText:
			m, err := proto.Decode(data)
			if err != nil {
				a.log.Warn("bad control message from relay", "err", err)
				continue
			}
			a.handleMsg(m)
		case websocket.MessageBinary:
			f, err := proto.Unmarshal(data)
			if err != nil {
				// fail-closed on protocol violations
				a.log.Error("bad frame from relay, closing", "err", err)
				return err
			}
			a.handleFrame(f)
		}
	}
}

// send queues a message on the current connection; it is dropped when
// offline. A full queue closes the connection (the reconnect re-snapshots).
func (a *Agent) send(typ websocket.MessageType, data []byte) bool {
	a.mu.Lock()
	w := a.conn
	a.mu.Unlock()
	if w == nil {
		return false
	}
	select {
	case w.out <- outMsg{typ, data}:
		return true
	default:
		a.log.Warn("relay send queue full, closing connection")
		w.close(websocket.StatusPolicyViolation, "slow relay")
		return false
	}
}

func (a *Agent) sendMsg(m proto.Msg) bool {
	data, err := m.Encode()
	if err != nil {
		a.log.Error("encode control message", "t", m.T, "err", err)
		return false
	}
	return a.send(websocket.MessageText, data)
}

func (a *Agent) sendFrame(f proto.Frame) bool {
	data, err := f.Marshal()
	if err != nil {
		a.log.Error("marshal frame", "type", f.Type, "err", err)
		return false
	}
	return a.send(websocket.MessageBinary, data)
}

func (a *Agent) closeConn() {
	a.mu.Lock()
	w := a.conn
	a.conn = nil
	a.mu.Unlock()
	if w != nil {
		w.close(websocket.StatusGoingAway, "agent shutting down")
	}
}
