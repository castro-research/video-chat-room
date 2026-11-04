package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// conn é um socket com fila de saída limitada. Um único escritor (writeLoop) esvazia a fila;
// quem produz nunca bloqueia: fila cheia = cliente lento = só ele é desconectado (RNF9).
type conn struct {
	ws   *websocket.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once
	slow atomic.Bool // já contada como cliente lento
}

func newConn(ws *websocket.Conn, queue int) *conn {
	return &conn{ws: ws, out: make(chan []byte, queue), done: make(chan struct{})}
}

// enqueue não bloqueia. false = fila cheia (ou conexão já encerrada).
func (c *conn) enqueue(b []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- b:
		return true
	default:
		return false
	}
}

// kill encerra a conexão uma vez. graceful faz o handshake de fechamento (o cliente vê o código);
// sem ele o socket é cortado na hora, que é o certo para quem não está lendo.
// Devolve true só na chamada que de fato encerrou.
func (c *conn) kill(code websocket.StatusCode, reason string, graceful bool) (first bool) {
	c.once.Do(func() {
		first = true
		close(c.done)
		go func() {
			if graceful {
				_ = c.ws.Close(code, reason)
			} else {
				_ = c.ws.CloseNow()
			}
		}()
	})
	return first
}

// onSlow é chamado quando uma escrita estoura o prazo: o cliente parou de ler.
func (c *conn) writeLoop(ctx context.Context, timeout time.Duration, onSlow func()) {
	for {
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			return
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, timeout)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			timedOut := wctx.Err() == context.DeadlineExceeded
			cancel()
			if err != nil {
				if timedOut { // o Write cortou o socket; o handler cancela o ctx logo em seguida, então não dá para olhar o ctx pai
					onSlow()
				}
				c.kill(websocket.StatusGoingAway, "write failed", false)
				return
			}
		}
	}
}

// writeDirect escreve antes de o writeLoop existir (hello, invalid_session).
func (c *conn) writeDirect(ctx context.Context, timeout time.Duration, f frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, b)
}

// frameBucket é o token bucket de frames do cliente numa conexão (só o read loop o usa, sem lock).
type frameBucket struct {
	tokens, burst, rate float64
	last                time.Time
	now                 func() time.Time
}

func newFrameBucket(rate, burst float64) *frameBucket {
	return &frameBucket{tokens: burst, burst: burst, rate: rate, last: time.Now(), now: time.Now}
}

// take gasta cost tokens; false = estourou o limite.
func (b *frameBucket) take(cost float64) bool {
	t := b.now()
	b.tokens = min(b.burst, b.tokens+t.Sub(b.last).Seconds()*b.rate)
	b.last = t
	if b.tokens < cost {
		return false
	}
	b.tokens -= cost
	return true
}
