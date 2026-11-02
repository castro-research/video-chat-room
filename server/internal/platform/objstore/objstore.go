// Package objstore é a fronteira com o armazenamento de objetos (S3/MinIO) dos anexos.
//
// A interface é pequena de propósito: o servidor nunca toca nos bytes. O cliente envia e baixa direto
// pelo storage com URLs pré-assinadas (RNF13) e o servidor só confere o resultado (Stat) e apaga (Delete).
package objstore

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("objstore: object not found")

// Info é o que o servidor confere de um objeto já enviado.
type Info struct {
	Size        int64
	ContentType string
}

// GetOptions molda a resposta do download (response-content-* assinados na URL).
type GetOptions struct {
	Filename    string
	ContentType string
	Inline      bool // abre no navegador (imagens, áudio, vídeo); senão força o download
}

type Store interface {
	// PresignPut devolve uma URL para o cliente enviar o objeto com PUT, válida por ttl.
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error)
	// PresignGet devolve uma URL de download válida por ttl.
	PresignGet(ctx context.Context, key string, opt GetOptions, ttl time.Duration) (string, error)
	// Stat devolve o tamanho do objeto, ou ErrNotFound.
	Stat(ctx context.Context, key string) (Info, error)
	// Delete remove o objeto; apagar o que não existe não é erro (os jobs de limpeza são idempotentes).
	Delete(ctx context.Context, key string) error
}
