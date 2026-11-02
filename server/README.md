# Kurz server

Servidor multi-guild em Go. Design: `docs/superpowers/specs/2026-11-01-kurz-server-design.md`. Planos: `docs/superpowers/plans/`.

## Rodar

```bash
cd server
make db-up   # Postgres em localhost:5433
make s3-up   # MinIO em localhost:9000 (console :9001, kurzdev / kurzdev-secret); opcional, liga os anexos
make run     # servidor em :8080 (aplica as migrações sozinho; usa o MinIO local)
make test    # testes, incluindo Postgres real e, se o MinIO estiver de pé, S3 real (senão fake, com SKIP explícito)
make test-short     # sem os testes de volume
make bench-search   # mede a busca com 1M de mensagens (KURZ_BENCH_MESSAGES muda o volume)
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
| `KURZ_S3_ENDPOINT` | vazio | `host:port` ou URL do S3/MinIO. **Vazio desliga os anexos** (endpoints respondem 501, sem a capability `attachments`) |
| `KURZ_S3_BUCKET` | `kurz` | criado na subida se não existir |
| `KURZ_S3_ACCESS_KEY` / `KURZ_S3_SECRET_KEY` | `kurzdev` / `kurzdev-secret` | credenciais do MinIO do compose |
| `KURZ_S3_REGION` | `us-east-1` | |
| `KURZ_S3_PUBLIC_ENDPOINT` | = endpoint | host que o CLIENTE alcança, usado nas URLs pré-assinadas (a assinatura cobre o host) |
| `KURZ_SECRET` | vazio | segredo HS256 do token de mídia, **o mesmo do SFU** (16 bytes ou mais). Vem junto com `KURZ_SFU_URL`; vazios desligam a voz (501 `not_supported`, sem a capability `voice`) |
| `KURZ_SFU_URL` | vazio | `ws://` ou `wss://` do `/v1/media` do SFU, como o CLIENTE alcança (vai em `VoiceSession.media.url`) |
| `KURZ_ICE_SERVERS` | STUN do Google | JSON `[{"urls":[...],"username":"","credential":""}]` entregue em `VoiceSession.iceServers` |

A imagem oficial `minio/minio` saiu do Docker Hub; o compose usa `cgr.dev/chainguard/minio`.

## Tempo real

O processo sobe o relay do outbox e o gateway junto com o HTTP. Escritas gravam o evento em `outbox` na própria transação; o relay
publica com `NOTIFY` e cada réplica entrega aos WebSockets (`GET /v1/gateway`). Protocolo em `contract/gateway.md`.

## Anexos, busca e jobs

- Anexos: `POST /v1/channels/{id}/attachments` → `PUT` direto no S3 → `POST .../messages` com `attachmentIds`. O servidor confere tipo (allowlist),
  tamanho (25 MiB) e, ao ligar na mensagem, o objeto (Stat) na mesma transação. Uploads nunca ligados são varridos pelo job `attachments.cleanup`.
- Busca: `GET /v1/guilds/{id}/search?q=`. `tsvector` gerado com config `portuguese`, índice GIN, mais recentes primeiro, só canais visíveis.
- Jobs: tabela `jobs` + worker `FOR UPDATE SKIP LOCKED` no mesmo binário (retry com backoff exponencial, lease para réplicas que morrem).

## Voz

SFU de `experimentos/sfu` mantido. `POST /v1/channels/{id}/voice/join` checa `viewChannel` + `connect`, grava o estado em `voice_states` (Postgres: vale para 2+ réplicas;
um usuário por canal, entrar em outro move) e emite o JWT de 60 s de uso único no formato do manager antigo (`iss=kurz-manager`, `sub`, `name`, `room`=id do canal, `jti`, `speak`).
O SFU consome o `jti` (guarda até o `exp`): um token reaproveitado fecha com 4001. O estado vive do heartbeat do gateway (3 min sem heartbeat e o job `voice.sweep` remove);
sair/kick/ban da guild remove na hora (o módulo ouve `member.leave` no bus) e o `voice.state` sai pelo outbox. O SFU não tem API para derrubar uma sessão de mídia aberta,
nem aplica `speak`: o servidor só para de emitir tokens, limpa o estado e força `muted` sem `speak`.
