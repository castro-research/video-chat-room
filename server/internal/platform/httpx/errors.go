// Package httpx reúne o que todo handler HTTP do servidor usa: erros tipados, JSON, usuário no contexto e middlewares.
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// Error é o erro que vira resposta `{code, message}` (ApiError do contrato).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

var (
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "you are not allowed to do this")
	ErrNotFound     = NewError(http.StatusNotFound, "not_found", "not found")
	errInternal     = NewError(http.StatusInternalServerError, "internal", "internal error")
)

func Validation(message string) *Error {
	return NewError(http.StatusUnprocessableEntity, "validation", message)
}

// WriteError escreve err como ApiError. Erros que não são *Error viram 500 sem vazar a mensagem.
func WriteError(w http.ResponseWriter, err error) { writeError(context.Background(), w, err) }

func writeError(ctx context.Context, w http.ResponseWriter, err error) {
	var he *Error
	if !errors.As(err, &he) {
		slog.ErrorContext(ctx, "unhandled error", "err", err)
		he = errInternal
	}
	WriteJSON(w, he.Status, map[string]string{"code": he.Code, "message": he.Message})
}

// Handler é um handler que devolve erro em vez de escrevê-lo.
type Handler func(http.ResponseWriter, *http.Request) error

func Wrap(h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(r.Context(), w, err)
		}
	}
}

// Erros de autorização compartilhados pelos domínios.
var (
	ErrMissingPermission = NewError(http.StatusForbidden, "missing_permission", "you lack the permission for this")
	ErrHierarchy         = NewError(http.StatusForbidden, "hierarchy", "your highest role is not above the target")
)
