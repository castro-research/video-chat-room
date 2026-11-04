# Experimentos: SFU em Go

Objetivo: usar **áudio, vídeo e compartilhamento de tela** numa sala, com o SFU em Go (sobe em Docker Swarm) e o cliente Apple. A sala e o token vêm do servidor Kurz (`server/`).

```
 cliente Apple ──HTTP──▶ servidor Kurz  (voice/join: checa permissão, emite o token)
       │
       └──WS /v1/media + UDP──▶ sfu  (recebe RTP de cada um e repassa aos outros)
```

## Levantamento

| Peça | Escolha | Por quê |
|---|---|---|
| SFU | Go + `pion/webrtc/v4` | Encaminha RTP sem decodificar. 1 `PeerConnection` por cliente, renegociada quando alguém publica ou sai. |
| Sinalização | WebSocket `/v1/media`, frames de `contract/openapi.yaml` (`join`, `publish`, `offer`, `answer`, `candidate`) | O contrato já existe; o cliente não muda se trocarmos o SFU por LiveKit. |
| Token | JWT HS256, 60 s, uso único, segredo compartilhado (`KURZ_SECRET`) | O servidor Kurz assina, o SFU valida. Sem chamada entre os dois no caminho quente. |
| Cliente | SwiftUI nativo (Mac, iPhone, iPad) com o WebRTC da Google (`WebRTC-SDK`) | Um projeto só em `client/apple`; lógica e vistas partilhadas. |
| Codecs | Opus (áudio) + VP8 (vídeo e tela) | É o mínimo que o contrato exige. |
| Rede | 1 porta UDP única (`ICEUDPMux`) + `NAT1To1` | Dá para publicar uma porta UDP no Swarm (modo `host`). Sem TURN: só funciona na mesma rede ou com o IP do nó alcançável. |

Alternativa já considerada: LiveKit. O contrato de mídia é compatível em espírito, então dá para trocar peça por peça.

## Interface do SFU

### Portas e variáveis

| Serviço | Porta | Para quê | Env |
|---|---|---|---|
| sfu | `8081` TCP (publicada em `30081`) | WebSocket `/v1/media` e `/healthz` | `KURZ_SECRET`, `SFU_PUBLIC_IP` (IP anunciado nos candidatos ICE), `SFU_UDP_PORT` (padrão `30100`) |
| sfu | `30100` UDP (publicada em `30100`, **mesmo número dentro e fora**) | RTP/ICE de todos | idem |
| sfu | `8082` TCP, não publicada | API interna (só a rede overlay do stack alcança) | — |

No Swarm `SFU_PUBLIC_IP` vem de `PUBLIC_IP`, que o `deploy.sh` passa ao `stack.yml`. Tem de ser um IP que o **cliente** alcança, e não `127.0.0.1`: o libwebrtc não forma par ICE entre o candidato da LAN e um candidato remoto em loopback (testado: com `127.0.0.1` falha, com o IP da LAN conecta). Por isso o `deploy.sh` usa o IP da LAN por padrão.

### Token (JWT HS256, `github.com/golang-jwt/jwt/v5`)

Claims: `iss="kurz-manager"`, `sub=userId`, `name`, `room`, `jti` (aleatório), `exp = agora + 60 s`.
O SFU rejeita assinatura inválida, expirado ou `jti` já usado: fecha o WebSocket com código **4001**.

### SFU

- `GET /v1/media` (WebSocket, 8081): protocolo do contrato. Primeiro frame `join {token}` em até 10 s.
  Sala é criada no primeiro `join`; some quando o último sai.
- Cada track encaminhada chega ao assinante com **msid stream id = userId do publicador** e **track id = `<userId>:<source>`** (`mic`, `camera`, `screen`, `screenAudio`).
- `GET /internal/rooms/{room}` (8082) -> `200 {participants:[{userId, name, mic, camera, screen}]}`; sala inexistente = `200` com lista vazia.
- Servidor é o par *impolite*; o cliente é o *polite* (faz rollback em colisão de ofertas).
- PLI: ao entrar um assinante e a cada ~3 s, o SFU pede keyframe ao publicador; PLI/FIR dos assinantes são repassados.

### Cliente

Fluxo: `POST /v1/channels/{id}/voice/join` (servidor Kurz) → abre `media.url` → `join` → `publish` + `offer` por track local → renderiza as tracks remotas pelo msid.

## Como rodar

**Local, sem Swarm** (`<LAN>` = `ipconfig getifaddr en0`):
```
cd experimentos/sfu && KURZ_SECRET=dev SFU_UDP_PORT=30100 SFU_PUBLIC_IP=<LAN> go run .
```
**Docker Swarm** (nó único; `docker swarm init` se ainda não estiver ativo): `experimentos/deploy.sh`. Constrói a imagem `kurz/sfu` e sobe o stack `videochat` (`stack.yml`). O segredo nasce em `experimentos/.env` (fora do git) e tem de ser o mesmo `KURZ_SECRET` do servidor Kurz. SFU em `:30081` (WebSocket) e `:30100/udp`. Parar: `docker stack rm videochat`. O nome `videochat` evita colidir com a rede `kurz_default` de outro projeto Compose.

**Cliente** (`cd client/apple`): `xcodegen generate`, abrir `Kurz.xcworkspace` e correr o esquema `KurzMac` ou `KurzIOS`.

## Limites conhecidos

- **O SFU não anuncia extensões de cabeçalho RTP** (nem mid/rid nem transport-cc) e limpa as do publicador ao repassar. O libwebrtc numera as extensões de forma diferente do pion e a renegociação falha com `codec collision for header extension id`. Custo: sem TWCC o publicador não estima banda pelo servidor.
- **1 réplica de SFU**: salas ficam em memória. Para escalar, o servidor Kurz teria de escolher o SFU da sala.
- TURN só via Cloudflare Realtime TURN, e só se `TURN_KEY_ID` e `TURN_KEY_API_TOKEN` estiverem em `experimentos/.env`. Com eles, o SFU sai do ICE lite e aloca relay também, então um cliente de fora conecta sem porta aberta no nó. Sem eles: fora da rede do nó não conecta.
- Sem simulcast: uma camada de vídeo por publicador (como no contrato 0.1).
- Android: sem cliente (o cliente Kotlin foi removido); só Apple.
- Sem autenticação de usuário e API interna sem credencial (isolada só pela porta).
