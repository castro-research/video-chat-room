# Kurz server

Servidor multi-guild em Go. Design: `docs/superpowers/specs/2026-11-01-kurz-server-design.md`. Planos: `docs/superpowers/plans/`.

## Rodar

```bash
cd server
make db-up   # Postgres em localhost:5433
make run     # servidor em :8080 (aplica as migrações sozinho)
make test    # testes, incluindo os de integração com Postgres
```

## Configuração

| Variável | Padrão | |
|---|---|---|
| `KURZ_DATABASE_URL` | obrigatória | URL do Postgres |
| `KURZ_JWT_SECRET` | obrigatória | 32 bytes ou mais |
| `KURZ_ADDR` | `:8080` | |
| `KURZ_NODE_ID` | `0` | 0 a 1023; deve ser único por réplica (bits do snowflake) |
| `KURZ_SERVER_NAME` | `Kurz` | |
| `KURZ_REGISTRATION` | `open` | `open` ou `closed` |
