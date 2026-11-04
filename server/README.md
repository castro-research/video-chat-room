# Kurz server

Servidor multi-guild em Go. Design: `docs/superpowers/specs/2026-11-01-kurz-server-design.md`. Planos: `docs/superpowers/plans/`.

## Rodar

```bash
cd server
make db-up   # Postgres em localhost:5433
make s3-up   # SeaweedFS (S3) em localhost:8333 (kurzdev / kurzdev-secret); opcional, liga os anexos
make run     # servidor em :8080 (aplica as migrações sozinho; usa o SeaweedFS local)
make test    # testes, incluindo Postgres real e, se o SeaweedFS estiver de pé, S3 real (senão fake, com SKIP explícito)
make test-short     # sem os testes de volume
make bench-search   # mede a busca com 1M de mensagens (KURZ_BENCH_MESSAGES muda o volume)
make loadtest       # bots de carga contra um servidor de pé (veja "Teste de carga")
make kind-up        # cluster kind local com Postgres, SeaweedFS e 2 réplicas (veja "Kubernetes")
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
| `KURZ_S3_ENDPOINT` | vazio | `host:port` ou URL do S3 (SeaweedFS no compose). **Vazio desliga os anexos** (endpoints respondem 501, sem a capability `attachments`) |
| `KURZ_S3_BUCKET` | `kurz` | criado na subida se não existir |
| `KURZ_S3_ACCESS_KEY` / `KURZ_S3_SECRET_KEY` | `kurzdev` / `kurzdev-secret` | credenciais de dev do compose (`deploy/seaweedfs/s3.json`) |
| `KURZ_S3_REGION` | `us-east-1` | |
| `KURZ_S3_PUBLIC_ENDPOINT` | = endpoint | host que o CLIENTE alcança, usado nas URLs pré-assinadas (a assinatura cobre o host) |
| `KURZ_SECRET` | vazio | segredo HS256 do token de mídia, **o mesmo do SFU** (16 bytes ou mais). Vem junto com `KURZ_SFU_URL`; vazios desligam a voz (501 `not_supported`, sem a capability `voice`) |
| `KURZ_SFU_URL` | vazio | `ws://` ou `wss://` do `/v1/media` do SFU, como o CLIENTE alcança (vai em `VoiceSession.media.url`) |
| `KURZ_ICE_SERVERS` | STUN do Google | JSON `[{"urls":[...],"username":"","credential":""}]` entregue em `VoiceSession.iceServers` |
| `KURZ_NODE_ID_FROM_HOSTNAME` | `false` | `true`: o node id é o sufixo numérico do `HOSTNAME` (`kurz-1` → 1), para StatefulSet; tem prioridade sobre `KURZ_NODE_ID` |
| `KURZ_RATELIMIT` | `on` | `off` desliga o rate limit (use no loadtest) |
| `KURZ_RATELIMIT_AUTH_BURST` / `KURZ_RATELIMIT_AUTH_PER_MIN` | `20` / `20` | token bucket **por IP** em `POST /v1/auth/*`: rajada e reposição por minuto |
| `KURZ_RATELIMIT_WRITE_BURST` / `KURZ_RATELIMIT_WRITE_PER_SEC` | `60` / `20` | token bucket **por usuário** nas demais escritas (POST/PUT/PATCH/DELETE; sem token válido, por IP) |
| `KURZ_WS_FRAME_RATE` / `KURZ_WS_FRAME_BURST` | `10` / `20` | token bucket **por conexão WebSocket** para frames do cliente (heartbeat custa 1, `voice.state.set` custa 4). Estourou: fecha com `4011` |
| `KURZ_WS_MAX_SESSIONS_PER_USER` | `20` | teto de sessões do gateway por usuário e réplica (ao vivo ou esperando resume; as abandonadas são recolhidas antes de recusar). Acima: `4012` |
| `KURZ_METRICS_ADDR` | vazio | listener próprio do `/metrics` (ex.: `:9090`), **sem auth** e fora do rate limit; nesse caso o `/metrics` é **removido** do listener da API (404). **Vazio = `/metrics` no listener principal, só para dev** (o servidor avisa no log). No k8s a porta é `9090`, só no pod (sem Service/NodePort) |
| `KURZ_REFRESH_RETENTION_DAYS` | `7` | job horário apaga refresh tokens expirados, usados ou revogados há mais que isso (a tabela crescia a cada login e refresh). Passado o prazo, reapresentar um token usado dá 401 sem revogar a família |
| `KURZ_JOBS_FAILED_RETENTION_DAYS` | `7` | jobs que esgotaram as tentativas são apagados depois disso (`kurz_jobs_failed` conta os que ainda estão lá: alerte antes). Os concluídos saem em 24 h |
| `KURZ_TRUSTED_PROXY` | `false` | `true` confia no `X-Forwarded-For` (usa a entrada mais à direita, a do nosso proxy). Só ligue atrás de um proxy que sobrescreve o header |
| `KURZ_OTEL_ENDPOINT` | vazio | URL OTLP/HTTP do coletor (`http://otel:4318`; sem caminho usa `/v1/traces`). **Vazio desliga os traces** |

### Docker Swarm (nó único, ex.: pizito)

`./deploy.sh` no nó do swarm: builda a imagem e sobe a stack `kurzserver` (`stack.yml`: kurz, Postgres 18 e SeaweedFS). Portas: `30082` (API + WebSocket) e `30083` (S3,
que o cliente acessa direto nas URLs pré-assinadas). Os segredos nascem em `server/.env` (fora do git) e o `KURZ_SECRET` é copiado de `experimentos/.env`, porque tem de ser o do SFU.
Atrás do túnel: `KURZ_SFU_URL=wss://cord.kurz.fyi/v1/media ./deploy.sh`. O ingress do túnel (`/etc/cloudflared/config.yml`) é configurado fora do repo e tem de apontar para a `30082`.
O build roda na arquitetura do nó; de outra máquina use `docker build --platform linux/amd64 -t kurz/server:latest server/`.

### Armazenamento (S3) e Postgres no compose

- **S3 dos anexos: SeaweedFS** (`chrislusf/seaweedfs:4.47`, tag pinada; `weed server -s3`, gateway S3 em `:8333`, path-style, região `us-east-1`).
  As credenciais de dev vêm das identities de `deploy/seaweedfs/s3.json` (identity `kurzdev` com `Admin`, necessário porque o servidor cria o bucket na subida).
  O cliente continua sendo o `minio-go`, que só fala S3.
- **`KURZ_S3_PUBLIC_ENDPOINT` tem que ser o host que o cliente usa** para alcançar o S3 (ex.: `localhost:8333`, ou `localhost:29000` no kind). A URL pré-assinada
  usa SigV4, que assina o cabeçalho `Host`: se o cliente chamar outro host (ou outra porta) que o assinado, o S3 responde `SignatureDoesNotMatch`.
  O `KURZ_S3_ENDPOINT` (servidor para S3) pode ser um nome interno diferente.
- **Postgres 18**: a imagem usa `/var/lib/postgresql` como volume e `PGDATA=/var/lib/postgresql/18/docker`; o compose e o StatefulSet montam esse diretório.
  Um volume criado com o Postgres 17 **não abre** no 18: recrie-o (`docker compose down -v` dentro de `server/`, só apaga dados de dev deste projeto; no kind,
  `make kind-down && make kind-up`, ou apague o PVC `data-postgres-0`) ou migre os dados com `pg_upgrade`/dump e restore.

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

## Rate limit

Token bucket em memória (`internal/platform/ratelimit`): `POST /v1/auth/*` por IP e as demais escritas por usuário. Estourou: `429`
`{"code":"rate_limited"}` com `Retry-After` em segundos (contrato). Leituras, `/healthz` e `/metrics` não são limitados. Buckets cheios
(ociosos) são varridos a cada minuto. **Dívida:** o limite é por réplica, não global; com N réplicas um cliente pode chegar a N vezes o
configurado (global exigiria Redis ou afinidade no balanceador). Atrás de proxy/Ingress, ligue `KURZ_TRUSTED_PROXY=true`, senão todos
os clientes parecem ter o IP do proxy.

## Observabilidade

- Logs: JSON (`slog`). Cada requisição loga `request_id` (aceita o `X-Request-Id` do cliente, senão gera um; é devolvido no header) e `trace_id` quando há trace.
- Métricas: `GET /metrics` (Prometheus). **Interno**: sem auth e fora do rate limit. Com `KURZ_METRICS_ADDR` ele vive num listener separado (e some do da API); sem ele fica no
  listener principal, só aceitável em dev. O label `method` só aceita os métodos HTTP padrão (o resto vira `OTHER`). Séries principais: `kurz_http_request_duration_seconds{method,route}` e `kurz_http_requests_total{method,route,status}`
  (a rota é o padrão do ServeMux, ex. `POST /v1/channels/{channelId}/messages`, sem ids; `unmatched` e `rate_limited` para o que não chegou ao mux), `kurz_ws_connections`,
  `kurz_ws_sessions`, `kurz_gateway_events_delivered_total`, `kurz_gateway_events_dropped_total` (backpressure), `kurz_gateway_slow_disconnects_total`,
  `kurz_outbox_lag_seconds` (idade do evento pendente mais antigo), `kurz_outbox_pending`, `kurz_jobs_pending`, `kurz_jobs_failed`, `kurz_pgx_pool_*` e as de runtime do Go.
  O lag do outbox e os jobs vêm de 2 queries curtas por raspagem.
- Traces: com `KURZ_OTEL_ENDPOINT`, um span por requisição HTTP (otelhttp; `/metrics` e `/healthz` ficam de fora) e um por query (otelpgx), exportados por OTLP/HTTP.
  Desligado por padrão, sem custo.

## Teste de carga

`loadtest/` são bots em Go (RNF16). Rode o servidor com `KURZ_RATELIMIT=off` (o gerador vem de um IP só) e:

```bash
go run ./loadtest --url http://localhost:8080 --users 20 --rate 20 --duration 30s   # ou: make loadtest
```

Registra N usuários, cria uma guild, entra por convite, conecta N sockets, envia `--rate` mensagens/s (e `--read-rate` leituras/s, padrão igual) e mede
latência REST de escrita e leitura e a ponta a ponta (do `POST` até o `message.create` chegar ao socket de **outro** bot). Imprime p50/p95/p99 e PASS/FAIL de
RNF1 (e2e p95 < 150 ms, p99 < 400 ms) e RNF2 (leitura p95 < 50 ms, escrita p95 < 100 ms), mais perda de eventos e erros; sai com código 1 se algo violar.
Os sockets reconectam sozinhos (backoff exponencial com jitter total; `resume` quando a sessão ainda existe). `--chaos` tolera perda/erros e exige que todos os sockets voltem (RNF8).
Os resultados dependem da máquina: rode ao menos duas vezes e olhe a variação.

## Kubernetes (kind)

```bash
make kind-up            # cria o cluster kind "kurz", builda e carrega a imagem, aplica os manifests e espera o rollout
curl localhost:28080/healthz        # API (NodePort 30080); S3 em localhost:29000
make kind-loadtest      # desliga o rate limit (overlay kind-loadtest) e roda o loadtest contra as 2 réplicas
make kind-kill          # mata kurz-1 sem aviso: os clientes reconectam, o servidor segue
make kind-down
```

- `Dockerfile`: multi-stage, build estático (`CGO_ENABLED=0`), imagem `distroless/static:nonroot`; inclui o binário `/kurz-loadtest`.
- `deploy/k8s/base` (kustomize): namespace `kurz`, Postgres (StatefulSet + PVC, **só para estudo**), SeaweedFS `chrislusf/seaweedfs:4.47` (Deployment, `emptyDir`, bucket criado pelo servidor),
  o monólito como **StatefulSet** de 2 réplicas (`KURZ_NODE_ID_FROM_HOSTNAME=true`: `kurz-0` → node 0, `kurz-1` → node 1, estável quando o pod é recriado), Service,
  ConfigMap (gerado, com hash no nome: mudar a config reinicia as réplicas), Secrets, probes em `/healthz`, requests/limits e PodDisruptionBudget.
- `deploy/k8s/overlays/kind` expõe a API e o S3 por NodePort; `kind-loadtest` acrescenta `KURZ_RATELIMIT=off`.
- **Os Secrets são de desenvolvimento e estão no repositório de propósito** (valores marcados `DEV-ONLY`/`dev-only`). Nada disso serve fora do kind.
- Dívidas: Postgres único sem backup; SeaweedFS sem persistência; a liveness em `/healthz` pinga o banco (tolerância alta de 60 s); sem Ingress/TLS; sem HPA; o rate limit é por réplica.
