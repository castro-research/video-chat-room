// Package ids gera IDs snowflake: 41 bits de milissegundos desde 2026-01-01, 10 bits de nó, 12 bits de sequência.
package ids

import (
	"errors"
	"strconv"
	"sync"
	"time"
)

const (
	epochMs  = 1767225600000 // 2026-01-01T00:00:00Z
	nodeBits = 10
	seqBits  = 12
	seqMask  = 1<<seqBits - 1
	MaxNode  = 1<<nodeBits - 1
)

type Generator struct {
	mu   sync.Mutex
	node int64
	last int64
	seq  int64
	now  func() time.Time
}

func NewGenerator(node int64) *Generator { return newGenerator(node, time.Now) }

func newGenerator(node int64, now func() time.Time) *Generator {
	if node < 0 || node > MaxNode {
		panic("ids: node out of range")
	}
	return &Generator{node: node, now: now}
}

// Next devolve um ID único e estritamente crescente por gerador, mesmo se o relógio voltar.
func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	ms := g.now().UnixMilli() - epochMs
	if ms < g.last {
		ms = g.last
	}
	if ms == g.last {
		g.seq = (g.seq + 1) & seqMask
		if g.seq == 0 { // estourou a sequência: pega emprestado o próximo milissegundo
			ms = g.last + 1
		}
	} else {
		g.seq = 0
	}
	g.last = ms
	return ms<<(nodeBits+seqBits) | g.node<<seqBits | g.seq
}

// Format escreve o ID como string, que é como ele aparece na API.
func Format(id int64) string { return strconv.FormatInt(id, 10) }

// Parse lê um ID da API. Só aceita inteiros positivos.
func Parse(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("ids: invalid id")
	}
	return id, nil
}
