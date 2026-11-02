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

## Tempo real

O processo sobe o relay do outbox e o gateway junto com o HTTP. Escritas gravam o evento em `outbox` na própria transação; o relay
publica com `NOTIFY` e cada réplica entrega aos WebSockets (`GET /v1/gateway`). Protocolo em `contract/gateway.md`.
