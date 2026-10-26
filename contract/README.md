# Kurz server contract (v0.1, draft)

What every Kurz server implements, in any language. One server = one community (tenant); the client is given a base URL
and talks only to what `openapi.yaml` describes. Draft: 0.x may still break between minors.

`openapi.yaml` is the single source of truth: REST paths, plus the WebSocket frames as schemas (`Gateway*`, `Media*`).
Per-endpoint rules and error codes are in it. This file has what a schema can't say: the flows.

## Connecting

1. The user types a base URL. The client `GET {base}/.well-known/kurz`, refuses a different `contract` major, hides what
   `capabilities` lacks, and shows the sign-up form according to `registration`.
2. `POST /v1/auth/register` or `/login` gives a `Session`. Refresh with `/v1/auth/refresh`; on 401 `token_expired` refresh once and retry.
3. Load the UI: `GET /v1/channels`, `GET /v1/members?role=admin`, `GET /v1/members`, `GET /v1/channels/{id}/messages`.
4. Open `{base}/v1/gateway` (WebSocket, `ws`/`wss` from the base scheme), send `identify` with the access token, wait for `ready`.
   From then on apply events: `message.create`, `member.update`/`remove`, `voice.state`, `voice.speaking`.
   No resume: after a reconnect, refetch the open channel's messages and the channel list.
5. Send messages with `POST /v1/channels/{id}/messages`, with a `nonce`. The `message.create` that comes back carries the
   same `nonce`, which is how the sender swaps its optimistic copy for the real one.

## Voice

1. `POST /v1/channels/{id}/voice/join` returns `media.url`, `media.token`, `iceServers` and who is already there.
2. Open `media.url` (WebSocket), send `join` with `media.token`, wait for `joined`.
3. One `RTCPeerConnection` with those `iceServers`. For each local track: send `publish { trackId, source }`, then `offer`.
   The server answers; ICE candidates trickle both ways as `candidate`.
4. When someone else starts or stops publishing, the server sends an `offer`; the client answers. On a collision (both
   offer at once) the client is the polite peer: it rolls back and answers the server's offer.
5. Every forwarded track has msid stream id = the publisher's user id and track id = `<userId>:<source>`
   (`mic`, `camera`, `screen`). That is how a client attaches a remote track to a tile.
6. Mute, camera and share: stop or start the track and send `voice.state.set` on the gateway. Everyone receives `voice.state`.
7. Leave with `POST .../voice/leave` or by closing the media socket.

Servers MUST support Opus for audio and VP8 for video (H.264 optional).

## Left out of 0.1 on purpose

- Creating or editing channels, roles and bans: the server owner does it however the implementation allows. Add when a client needs it.
- Editing and deleting messages, reactions, threads, typing indicators.
- Federation between servers, and any login other than username and password.
- Resuming the gateway and simulcast/layer selection: clients refetch; one video layer per publisher.

## Tooling

- Browse: `docker compose up api-docs` (from the repo root), then http://localhost:8081. The `base` server variable is editable for "Try it out".
- Validate: `npx @redocly/cli lint openapi.yaml` (the unused-component and 4XX warnings are expected: frames are only referenced from prose).
- Go: `oapi-codegen` drops schemas no path references, which removes the WebSocket frames. Use `output-options: { skip-prune: true }`
  with `generate: { models: true }`. It emits `GatewayServerMessage` etc. with `AsReadyEvent()`, `Discriminator()` and friends.
