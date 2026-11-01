// Package migrations embute os arquivos .sql para o binário aplicá-los sozinho.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
