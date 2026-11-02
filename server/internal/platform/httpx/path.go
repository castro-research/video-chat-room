package httpx

import (
	"net/http"
	"strconv"

	"kurz/server/internal/platform/ids"
)

// PathID lê um id snowflake de um wildcard do caminho. Id malformado vira 404.
func PathID(r *http.Request, name string) (int64, error) {
	id, err := ids.Parse(r.PathValue(name))
	if err != nil {
		return 0, ErrNotFound
	}
	return id, nil
}

// Caller devolve o usuário autenticado. Só chame atrás do middleware de auth.
func Caller(r *http.Request) (int64, error) {
	id, ok := UserID(r.Context())
	if !ok {
		return 0, ErrUnauthorized
	}
	return id, nil
}

// QueryInt lê um inteiro opcional da query string, com limites.
func QueryInt(r *http.Request, name string, def, min, max int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, Validation(name + " is out of range")
	}
	return n, nil
}
