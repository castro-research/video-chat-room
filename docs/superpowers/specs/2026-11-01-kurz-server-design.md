# Kurz Server: design

Servidor multi-guild inspirado no Discord para o cliente Kurz (Apple). Projeto de estudo: overengineering é bem-vindo onde ensina, mas a escala-alvo é modesta.

## Decisões

| Tema | Decisão |
|---|---|
| Objetivo | Estudo; vibe coding |
| Modelo | Plataforma multi-guild: contas globais, usuários criam guilds (muda o contrato 0.1 "um servidor = uma comunidade") |
| Linguagem | Go |
| Arquitetura | Monólito modular, stateless, 2+ réplicas no k8s local (kind/k3d) |
| Store | Postgres como único store de estado (dados, outbox, fila de jobs, FTS) |
| Eventos | Interface `Bus` com implementação Postgres (outbox + `LISTEN/NOTIFY`); NATS JetStream fica como evolução |
| Anexos | MinIO (S3) com URL pré-assinada |
| Mídia | SFU em Go/pion existente (`experimentos/`) permanece; o monólito assume o papel do manager |
| Escala-alvo | 100 usuários conectados, 10 guilds, 20 msg/s |

## Requisitos funcionais (v1)

**Identidade**
- RF1. Registro, login, refresh e logout com username e senha (Argon2id). Access token JWT curto mais refresh token rotativo.
- RF2. Perfil (`/me`): nome de exibição e avatar.

**Guilds e membros**
- RF3. Qualquer usuário cria uma guild e vira o dono.
- RF4. Convites: link com código, expiração e limite de usos opcionais. Entrar por código.
- RF5. Listar membros, sair da guild, expulsar (kick) e banir.
- RF6. Transferir a posse e excluir a guild (só o dono).

**Canais e permissões**
- RF7. Criar, renomear, reordenar e excluir canais de texto e de voz, com categorias opcionais.
- RF8. Papéis por guild com bitmask de permissões (ver canal, enviar mensagem, gerenciar mensagens, gerenciar canais, convidar, kick/ban, falar em voz, administrador).
- RF9. Hierarquia: um papel só gerencia papéis e membros abaixo do seu. O dono ignora as checagens.
- RF10. Overrides de permissão por canal, por papel ou membro (allow/deny), com resolução ao estilo do Discord.

**Mensagens**
- RF11. Enviar, listar (paginação por cursor, nos dois sentidos) e marcar como lido.
- RF12. Editar e excluir (próprias, ou de qualquer um com "gerenciar mensagens"). Respostas (reply).
- RF13. Reações com emoji, adicionar e remover.
- RF14. Anexos: upload para MinIO via URL pré-assinada, limite de tamanho e tipo.
- RF15. Busca full-text por guild ou canal, respeitando quem pode ver o canal.

**Tempo real**
- RF16. Gateway WebSocket: `identify`, `ready` e eventos (`message.create/update/delete`, `reaction.*`, `member.*`, `channel.*`, `voice.state`), sempre filtrados por permissão. Resume por número de sequência.

**Voz**
- RF17. Entrar e sair de um canal de voz com checagem de permissão. A API emite o token do SFU e o `VoiceSession`, e distribui o `voice.state`.

**Backlog (fora da v1):** presença e "digitando", DMs, threads, federação, 2FA, moderação automática.

## Requisitos não funcionais

Medidos com bots na escala modesta.

**Desempenho**
- RNF1. Entrega de evento (REST `POST` até o `message.create` chegar a outro cliente): p95 < 150 ms, p99 < 400 ms.
- RNF2. REST de leitura: p95 < 50 ms. Escrita: p95 < 100 ms.
- RNF3. Busca full-text: p95 < 300 ms em 1 milhão de mensagens.

**Consistência e ordem**
- RNF4. Ordem total por canal, com ID monotônico (snowflake). Paginação por cursor estável.
- RNF5. Nenhum evento perdido por falha do processo: outbox transacional.
- RNF6. Entrega at-least-once com consumidores idempotentes. No resume, o cliente recupera o que perdeu a partir da última sequência.
- RNF7. Permissões nunca obsoletas por mais de 1 s após uma mudança, inclusive entre réplicas.

**Escala e resiliência**
- RNF8. Monólito stateless com 2+ réplicas. Matar uma réplica derruba só as conexões dela; os clientes reconectam com backoff e jitter.
- RNF9. Backpressure no gateway: cliente lento tem fila limitada e é desconectado ao estourar, sem travar os outros.
- RNF10. Rate limit (token bucket) por usuário e por IP nos endpoints de escrita e de auth.

**Segurança**
- RNF11. Argon2id, refresh token rotativo com detecção de reuso, sem segredos no repositório.
- RNF12. Autorização checada no servidor, por operação. Testes automatizados da matriz de permissões.
- RNF13. Uploads validados por tipo e tamanho, servidos por URL pré-assinada com expiração.

**Operação**
- RNF14. Logs JSON, métricas Prometheus (latência, conexões WS, lag do outbox) e traces OpenTelemetry.
- RNF15. Migrações versionadas e reversíveis. Um comando sobe o ambiente local completo (kind, Postgres, MinIO, monólito).
- RNF16. Testes unitários nos domínios, integração com Postgres real (testcontainers) e gerador de carga em Go que verifica RNF1 e RNF2.

## Arquitetura

```
server/
  cmd/kurz/            main: sobe HTTP + WS + workers no mesmo binário
  internal/
    identity/          contas, sessões, tokens
    guilds/            guilds, membros, convites, bans
    channels/          canais, categorias, overrides
    permissions/       bitmask, hierarquia, resolução (puro, sem I/O)
    messages/          mensagens, reações, anexos, leitura
    search/            consulta FTS
    voice/             emite token do SFU, voice.state
    gateway/           WebSocket, sessões, resume, fan-out
    bus/               interface Bus + impl Postgres (outbox, LISTEN/NOTIFY)
    platform/          db, config, log, métricas, ratelimit, ids (snowflake)
  migrations/
  deploy/k8s/ + kind.yaml
  loadtest/            bots em Go
```

Regra de dependência: os domínios dependem só de `permissions`, `platform` e `bus`. Nenhum domínio importa outro diretamente; falam por interfaces pequenas definidas no consumidor.

## Dados (Postgres)

`users`, `sessions`, `guilds`, `members`, `roles`, `member_roles`, `channels`, `channel_overrides`, `invites`, `bans`, `messages` (`tsvector` + índice GIN), `reactions`, `attachments`, `read_states`, `outbox`, `jobs`. IDs são snowflakes `bigint`.

## Fluxo de uma mensagem

1. `POST /v1/channels/{id}/messages` passa por auth, rate limit e checagem de permissão.
2. Uma única transação grava `messages`, `outbox` (`message.create`) e, se preciso, `jobs`.
3. O relay lê o `outbox` e emite `NOTIFY`. O gateway de cada réplica escuta.
4. O gateway resolve quem vê o canal (cache de permissões invalidado por evento) e envia aos sockets com `seq` por sessão.
5. Quem enviou recebe o mesmo evento com o `nonce`, para trocar a cópia otimista pela real.

**Resume:** buffer circular por sessão no gateway. Se o `seq` pedido já saiu do buffer, o cliente recebe `invalid_session` e refaz o fetch.

## Voz

O SFU de `experimentos/sfu` é mantido. O módulo `voice` valida a permissão de falar/entrar, emite o JWT de 60 s de uso único (mesmo formato do manager atual, segredo compartilhado `KURZ_SECRET`) e devolve o `VoiceSession`. O estado de voz (`voice.state`) é mantido no monólito e distribuído pelo gateway.

## Erros

Formato `{code, message}` do contrato. Códigos novos: `missing_permission`, `hierarchy`, `invite_expired`, `banned`, `rate_limited`. Pânico é capturado por middleware e vira 500 com log.

## Testes

- `permissions`: testes de tabela cobrindo hierarquia e overrides.
- Domínios: integração com Postgres via testcontainers.
- `loadtest`: bots que conectam ao gateway, enviam mensagens e verificam RNF1/RNF2.

## Contrato

`contract/openapi.yaml` passa a v0.2: ganha `/v1/guilds/...` (guilds, membros, convites, papéis, canais por guild) e os eventos novos do gateway. Os tipos Go são gerados com `oapi-codegen` (`skip-prune: true`).

## Pontos em aberto

- O cliente Apple hoje fala só com o manager de salas; adaptá-lo ao novo contrato é um sub-projeto separado.
- Substituir `LISTEN/NOTIFY` por NATS JetStream fica como evolução, atrás da interface `Bus`.
