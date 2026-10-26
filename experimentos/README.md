# Experimentos: SFU + Manager em Go

Objetivo: usar **áudio, vídeo e compartilhamento de tela** numa sala, com dois containers Go (sobem em Docker Swarm) e o cliente Kotlin Desktop.

```
 cliente Kotlin ──HTTP──▶ manager  (cria sala, entra na sala, emite token)
       │                     │  HTTP interno (quem está na sala)
       │                     ▼
       └──WS /v1/media + UDP──▶ sfu  (recebe RTP de cada um e repassa aos outros)
```

## Levantamento

| Peça | Escolha | Por quê |
|---|---|---|
| SFU | Go + `pion/webrtc/v4` | Encaminha RTP sem decodificar. 1 `PeerConnection` por cliente, renegociada quando alguém publica ou sai. |
| Sinalização | WebSocket `/v1/media`, frames de `contract/openapi.yaml` (`join`, `publish`, `offer`, `answer`, `candidate`) | O contrato já existe; o cliente não muda se trocarmos o SFU por LiveKit. |
| Manager | Go stdlib `net/http` | Sala em memória. `join` devolve o `VoiceSession` do contrato: URL do SFU, token, `iceServers`. |
| Token | JWT HS256, 60 s, uso único, segredo compartilhado (`KURZ_SECRET`) | Manager assina, SFU valida. Sem chamada entre os dois no caminho quente. |
| Cliente | Desktop JVM com `webrtc-java` (`dev.onvoid.webrtc`) | Única lib que captura mic/câmera/tela e renderiza na JVM. Android e iOS ficam de fora (ver limites). |
| Codecs | Opus (áudio) + VP8 (vídeo e tela) | É o mínimo que o contrato exige. |
| Rede | 1 porta UDP única (`ICEUDPMux`) + `NAT1To1` | Dá para publicar uma porta UDP no Swarm (modo `host`). Sem TURN: só funciona na mesma rede ou com o IP do nó alcançável. |

Alternativa já considerada: LiveKit. O contrato de mídia é compatível em espírito, então dá para trocar peça por peça.

## Interface entre os serviços (fixa: os três agentes seguem isto)

### Portas e variáveis

| Serviço | Porta | Para quê | Env |
|---|---|---|---|
| manager | `8080` TCP (publicada em `30080`) | REST público | `KURZ_SECRET`, `SFU_INTERNAL_URL` (`http://sfu:8082`), `SFU_PUBLIC_WS_URL` (ex.: `ws://<ipDoNó>:30081/v1/media`), `ICE_SERVERS` (JSON `[{"urls":[...]}]`, opcional; padrão: STUN do Google) |
| sfu | `8081` TCP (publicada em `30081`) | WebSocket `/v1/media` e `/healthz` | `KURZ_SECRET`, `SFU_PUBLIC_IP` (IP anunciado nos candidatos ICE), `SFU_UDP_PORT` (padrão `30100`) |
| sfu | `30100` UDP (publicada em `30100`, **mesmo número dentro e fora**) | RTP/ICE de todos | idem |
| sfu | `8082` TCP, não publicada | API interna (só a rede overlay do stack alcança) | — |

No Swarm `SFU_PUBLIC_IP` e o IP de `SFU_PUBLIC_WS_URL` vêm de `PUBLIC_IP`, que o `deploy.sh` passa ao `stack.yml`. Tem de ser um IP que o **cliente** alcança, e não `127.0.0.1`: o libwebrtc não forma par ICE entre o candidato da LAN e um candidato remoto em loopback (testado: com `127.0.0.1` falha, com o IP da LAN conecta). Por isso o `deploy.sh` usa o IP da LAN por padrão.

### Manager (REST, JSON, erros no formato `ApiError` do contrato: `{code, message}`)

```
Room        { id, name, createdAt (RFC 3339), participants: [Participant] }
Participant { userId, name, mic, camera, screen }          // booleans: a fonte está publicada
POST /v1/rooms             { name }                -> 201 Room        (id: 6 chars [a-z0-9]; name 1..64, senão 422 validation)
GET  /v1/rooms                                     -> 200 { rooms: [Room] }
GET  /v1/rooms/{id}                                -> 200 Room | 404 not_found
POST /v1/rooms/{id}/join   { userId, name }        -> 200 VoiceSession do contrato | 404 | 422
GET  /healthz                                      -> 200
```

`participants` vem do SFU (fonte da verdade). `VoiceSession.participants[]` é o mapeamento de `Participant` para `VoiceParticipant` do contrato:
`user = {id: userId, username: userId, name, status: "online", role: "member"}`, `muted = !mic`, `camOn = camera`, `sharing = screen`.
Sair da sala = fechar o WebSocket de mídia (não há `leave` no manager). Sem autenticação: `userId` é livre (experimento).

### Token (JWT HS256, `github.com/golang-jwt/jwt/v5`)

Claims: `iss="kurz-manager"`, `sub=userId`, `name`, `room`, `jti` (aleatório), `exp = agora + 60 s`.
O SFU rejeita assinatura inválida, expirado ou `jti` já usado: fecha o WebSocket com código **4001**.

### SFU

- `GET /v1/media` (WebSocket, 8081): protocolo do contrato. Primeiro frame `join {token}` em até 10 s.
  Sala é criada no primeiro `join`; some quando o último sai.
- Cada track encaminhada chega ao assinante com **msid stream id = userId do publicador** e **track id = `<userId>:<source>`** (`mic`, `camera`, `screen`).
- `GET /internal/rooms/{room}` (8082) -> `200 {participants:[{userId, name, mic, camera, screen}]}`; sala inexistente = `200` com lista vazia.
- Servidor é o par *impolite*; o cliente é o *polite* (faz rollback em colisão de ofertas).
- PLI: ao entrar um assinante e a cada ~3 s, o SFU pede keyframe ao publicador; PLI/FIR dos assinantes são repassados.

### Cliente

Fluxo: `POST /v1/rooms` (criar) ou `GET /v1/rooms` (listar) → `POST /v1/rooms/{id}/join` → abre `media.url` → `join` → `publish` + `offer` por track local → renderiza as tracks remotas pelo msid. Atualiza a lista de participantes consultando `GET /v1/rooms/{id}` a cada 2 s (o contrato não tem evento de presença no WebSocket de mídia).

## Como rodar

**Local, sem Swarm** (um terminal por serviço; `<LAN>` = `ipconfig getifaddr en0`):
```
cd experimentos/sfu     && KURZ_SECRET=dev SFU_UDP_PORT=30100 SFU_PUBLIC_IP=<LAN> go run .
cd experimentos/manager && KURZ_SECRET=dev SFU_PUBLIC_WS_URL=ws://<LAN>:8081/v1/media SFU_INTERNAL_URL=http://127.0.0.1:8082 go run .
```
**Docker Swarm** (nó único; `docker swarm init` se ainda não estiver ativo): `experimentos/deploy.sh`. Constrói as imagens `kurz/sfu` e `kurz/manager` e sobe o stack `videochat` (`stack.yml`). O segredo nasce em `experimentos/.env` (fora do git). Manager em `:30080`, SFU em `:30081` (WebSocket) e `:30100/udp`. Parar: `docker stack rm videochat`. O nome `videochat` evita colidir com a rede `kurz_default` de outro projeto Compose.

**Cliente** (`cd client`): `./gradlew :app:run` abre a app; na barra lateral, "Salas" → URL do manager (`http://<LAN>:30080` ou `:8080` local), nome, "Criar sala". Sem janela nem hardware: `./gradlew :app:voiceCheck -Pmanager=http://localhost:8080` entra com 3 clientes sintéticos, troca vídeo/áudio/tela e imprime `OK`.

## Limites conhecidos

- **O SFU não anuncia extensões de cabeçalho RTP** (nem mid/rid nem transport-cc) e limpa as do publicador ao repassar. O libwebrtc numera as extensões de forma diferente do pion e a renegociação falha com `codec collision for header extension id`. Custo: sem TWCC o publicador não estima banda pelo servidor.
- **1 réplica de SFU**: salas ficam em memória. Para escalar, o manager teria de escolher o SFU da sala.
- TURN só via Cloudflare Realtime TURN, e só se `TURN_KEY_ID` e `TURN_KEY_API_TOKEN` estiverem em `experimentos/.env`. Com eles, o manager entrega credenciais temporárias no `join` e o SFU sai do ICE lite e aloca relay também, então um cliente de fora conecta sem porta aberta no nó. Sem eles: fora da rede do nó não conecta.
- Sem simulcast: uma camada de vídeo por publicador (como no contrato 0.1).
- Android/iOS: continuam compilando, mas sem mídia real (Android: `io.github.webrtc-sdk:android`; iOS: WebRTC.framework via cinterop).
- Sem autenticação de usuário e API interna sem credencial (isolada só pela porta).
