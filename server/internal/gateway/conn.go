package gateway

import (
	"context"
	"encoding/json"
	"sync"
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

// onFail é chamado se a escrita falhar ou estourar o prazo: cliente parado.
func (c *conn) writeLoop(ctx context.Context, timeout time.Duration, onFail func()) {
	for {
		select {
		case <-c.done:
			return
		case <-ctx.Done():
			return
		case b := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, timeout)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				onFail()
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
