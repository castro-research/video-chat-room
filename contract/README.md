# Kurz server contract (v0.5, draft)

What every Kurz server implements, in any language. One server is a platform hosting many guilds (communities): accounts are
global, any user creates a guild and invites others. The client is given a base URL and talks only to what `openapi.yaml` describes. Draft: 0.x may still break between minors.

`openapi.yaml` is the single source of truth for REST. The realtime gateway protocol (frames, `seq`, resume, events,
close codes) is specified in [`gateway.md`](gateway.md); the media WebSocket frames are schemas in `openapi.yaml` (`Media*`).
Per-endpoint rules and error codes are in the OpenAPI file. This file has what a schema can't say: the flows.

## Connecting

1. The user types a base URL. The client `GET {base}/.well-known/kurz`, refuses a different `contract` major, hides what
   `capabilities` lacks, and shows the sign-up form according to `registration`.
2. `POST /v1/auth/register` or `/login` gives a `Session`. Refresh with `/v1/auth/refresh`; on 401 `token_expired` refresh once and retry.
3. Load the UI: `GET /v1/guilds`, then for the open guild `GET /v1/guilds/{id}/channels`, `GET /v1/guilds/{id}/members`, `GET /v1/guilds/{id}/roles`,
   then `GET /v1/channels/{id}/messages`. To join a guild: `GET /v1/invites/{code}` to preview, `POST /v1/invites/{code}/join`.
4. If `capabilities` has `realtime`, open `{base}/v1/gateway` (WebSocket, `ws`/`wss` from the base scheme), send `identify` with the
   access token, wait for `ready`, then load the UI over REST. Apply `dispatch` events idempotently. After a drop, `resume` with the
   last `seq`; on `invalid_session`, `identify` again and refetch. See `gateway.md`.
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
   (`mic`, `camera`, `screen`, `screenAudio`). That is how a client attaches a remote track to a tile.
6. Mute, camera and share: stop or start the track and send `voice.state.set` on the gateway. Everyone receives `voice.state`.
7. Leave with `POST .../voice/leave` or by closing the media socket.

Servers MUST support Opus for audio and VP8 for video (H.264 optional).

## Guilds, roles and permissions

- Creating a guild makes the caller its owner, with the `@everyone` role (its id is the guild id, position 0) and a text channel `geral`.
- A member's permissions are the union of `@everyone` and their roles. `administrator` means all; the owner is above every check.
- A channel can override that per role and member (`allow`/`deny`). Order: @everyone override, then roles, then the member.
  A channel the member cannot `viewChannel` is hidden: absent from lists and 404 on direct access.
- Hierarchy: roles have a `position`; you manage a role or member only if your highest role is strictly above theirs.
  You also cannot grant a permission bit you do not have.
- Invites are codes with optional expiry and use limit. Banned users get 403 `banned`; spent or expired codes get 410 `invite_expired`.

## Attachments and search (0.5)

- Attachments (capability `attachments`, only when the server has object storage): `POST /v1/channels/{id}/attachments` with name, type and size
  returns an `uploadUrl`; `PUT` the bytes to it directly; then `POST .../messages` with `attachmentIds` (text may be empty).
  `Message.attachments[].url` is a pre-signed GET that expires after 1 hour: refetch the message when it does. A server without
  object storage answers 501 `not_supported`.
- Search (capability `search`): `GET /v1/guilds/{id}/search?q=...`, newest first, cursor-paginated, limited to channels the caller can see.

## Left out of 0.5 on purpose

- Threads, typing indicators, search ranking by relevance and image dimensions on attachments.
- Federation between servers, and any login other than username and password.
- Simulcast/layer selection: one video layer per publisher. Voice events, presence and typing on the gateway.

## Tooling

- Browse: `docker compose up api-docs` (from the repo root), then http://localhost:8081. The `base` server variable is editable for "Try it out".
- Validate: `npx @redocly/cli lint openapi.yaml` (the unused-component and 4XX warnings are expected: frames are only referenced from prose).
- Go: `oapi-codegen` drops schemas no path references, which removes the WebSocket frames. Use `output-options: { skip-prune: true }`
  with `generate: { models: true }`. It emits `GatewayServerMessage` etc. with `AsReadyEvent()`, `Discriminator()` and friends.
