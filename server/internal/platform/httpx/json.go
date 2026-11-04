package httpx

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Decode lê o corpo JSON (até 1 MiB). Corpo inválido vira 422 validation. Qualquer string do corpo com NUL
// (\u0000) também: o Postgres não guarda NUL em text (SQLSTATE 22021) e o resultado seria um 500.
func Decode(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		return Validation("invalid JSON body")
	}
	if hasNUL(reflect.ValueOf(v), 0) {
		return errNUL
	}
	return nil
}

var errNUL = Validation("text must not contain NUL characters")

// NoNUL rejeita texto com NUL, para valores que não vêm do corpo JSON (caminho, query).
func NoNUL(s string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return errNUL
	}
	return nil
}

func hasNUL(v reflect.Value, depth int) bool {
	if depth > 16 {
		return false
	}
	switch v.Kind() {
	case reflect.String:
		return strings.IndexByte(v.String(), 0) >= 0
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && hasNUL(v.Elem(), depth+1)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() && hasNUL(v.Field(i), depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return false
		}
		for i := range v.Len() {
			if hasNUL(v.Index(i), depth+1) {
				return true
			}
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			if hasNUL(it.Key(), depth+1) || hasNUL(it.Value(), depth+1) {
				return true
			}
		}
	}
	return false
}
