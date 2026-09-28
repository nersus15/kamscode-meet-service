package signaling

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nersus15/kamscode-meet-v2/internal/protocol"
)

const (
	outBuffer    = 64
	writeTimeout = 5 * time.Second
	flushTimeout = time.Second
	readLimit    = 64 << 10
)

// conn membungkus satu koneksi WS: satu pembaca (pemanggil readLoop) dan satu penulis (writeLoop).
type conn struct {
	ws  *websocket.Conn
	log *slog.Logger

	out  chan protocol.Message
	done chan struct{}

	closeOnce   sync.Once
	closeCode   websocket.StatusCode
	closeReason string
}

func newConn(ws *websocket.Conn, log *slog.Logger) *conn {
	ws.SetReadLimit(readLimit)
	c := &conn{
		ws:   ws,
		log:  log,
		out:  make(chan protocol.Message, outBuffer),
		done: make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

func (c *conn) send(m protocol.Message) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.out <- m:
		return true
	default:
		c.log.Warn("outbound buffer full, closing", "type", m.Type)
		c.closeWith(websocket.StatusTryAgainLater, "slow consumer")
		return false
	}
}

func (c *conn) sendError(msg string) {
	m, _ := protocol.New(protocol.TypeError, protocol.ErrorPayload{Message: msg})
	c.send(m)
}

func (c *conn) readLoop(ctx context.Context, handle func(protocol.Message)) error {
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			c.sendError("binary messages not supported")
			continue
		}

		var m protocol.Message
		if err := json.Unmarshal(data, &m); err != nil {
			c.sendError("invalid message")
			continue
		}
		handle(m)
	}
}

func (c *conn) close() {
	c.closeWith(websocket.StatusNormalClosure, "")
}

func (c *conn) closeWith(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode = code
		c.closeReason = reason
		close(c.done)
	})
}

func (c *conn) writeLoop() {
	for {
		select {
		case m := <-c.out:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.write(ctx, m)
			cancel()
			if err != nil {
				c.log.Info("ws write failed", "err", err)
				c.closeWith(websocket.StatusInternalError, "write failed")
				c.ws.CloseNow()
				return
			}
		case <-c.done:
			c.flush()
			c.ws.Close(c.closeCode, c.closeReason)
			return
		}
	}
}

func (c *conn) flush() {
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	for {
		select {
		case m := <-c.out:
			if err := c.write(ctx, m); err != nil {
				return
			}
		default:
			return
		}
	}
}

func (c *conn) write(ctx context.Context, m protocol.Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageText, data)
}
