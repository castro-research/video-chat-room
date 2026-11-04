package identity

import (
	"context"
	"encoding/json"
	"time"

	"kurz/server/internal/jobs"
)

const (
	// JobCleanupRefresh é o kind do job periódico que apaga refresh tokens velhos (a tabela cresce a cada login e refresh).
	JobCleanupRefresh   = "identity.cleanup_refresh"
	cleanupRefreshEvery = time.Hour
	cleanupRefreshBatch = 5000
)

// CleanupRefresh apaga os refresh tokens que expiraram, foram usados ou foram revogados há mais de RefreshRetention.
// Devolve quantos apagou. Idempotente; qualquer réplica pode rodar.
func (s *Service) CleanupRefresh(ctx context.Context) (int, error) {
	cutoff := s.now().Add(-s.cfg.RefreshRetention)
	total := 0
	for {
		n, err := s.store.deleteStaleRefresh(ctx, cutoff, cleanupRefreshBatch)
		total += int(n)
		if err != nil || n < cleanupRefreshBatch {
			return total, err
		}
	}
}

// RegisterJobs registra a limpeza periódica de refresh tokens no worker.
func (s *Service) RegisterJobs(w *jobs.Worker) {
	w.Handle(JobCleanupRefresh, func(ctx context.Context, _ json.RawMessage) error {
		_, err := s.CleanupRefresh(ctx)
		return err
	})
	w.Every(JobCleanupRefresh, cleanupRefreshEvery)
}
