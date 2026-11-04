# Kurz gateway (contract 0.7)

Realtime channel: `GET {base}/v1/gateway`, upgraded to a WebSocket (`ws`/`wss` from the base scheme). The server advertises it
as the `realtime` capability in `/.well-known/kurz`. Every frame is a JSON text message:

```json
{ "op": "dispatch", "t": "message.create", "seq": 42, "d": { } }
```

`op` always; `t` and `seq` only on `dispatch`; `d` when the op has data. Ids are strings, timestamps RFC 3339 UTC.
The access token never goes in the URL: the first client frame authenticates.

## Handshake

1. Server sends `hello`: `{ "op": "hello", "d": { "heartbeatInterval": 30000 } }` (milliseconds).
2. The client has 10 s to send **`identify`** or **`resume`**, else the server closes with `4003`.
3. `identify`: `{ "op": "identify", "d": { "token": "<access token>" } }`. Reply:
   `{ "op": "ready", "d": { "sessionId", "user": { "id" }, "guilds": [ { "id", "name", "ownerId" } ], "heartbeatInterval", "voice"? } }`.
   `voice` (only on servers with the `voice` capability) is the current voice state: an array of `voice.state` payloads
   (`{ channelId, connected: true, participant }`), one per person in a voice channel of the user's guilds that the user can see.
   A bad or expired token closes with `4001`: refresh the access token and reconnect once.
4. After `ready` the server sends events. Anything committed before `ready` is read over REST *after* `ready`
   (list channels, members, messages); anything committed later arrives as a dispatch. An event may therefore arrive that
   the REST read already reflects: apply events idempotently (upsert/remove by id).

## Heartbeat

The client sends `{ "op": "heartbeat" }` every `heartbeatInterval` ms; the server answers `{ "op": "heartbeat_ack" }`.
No frame from the client for two intervals closes the connection with `4010`. The session survives for resume.
On a server with `voice`, each heartbeat also renews the sender's voice state (the server only touches storage for users that are in a voice channel): a user in a voice channel whose client sends no
heartbeat for 3 minutes is removed from it (and everyone who sees the channel gets `voice.state` with `connected: false`).

## Sequence numbers and resume

Every `dispatch` carries `seq`, a counter **per session** starting at 1. The server keeps the last 256 events of each session
(a ring buffer), including while the client is disconnected, for 2 minutes after the connection dropped.

To resume, open a new connection and, instead of `identify`, send:

```json
{ "op": "resume", "d": { "token": "<access token>", "sessionId": "...", "seq": 41 } }
```

`seq` is the last one the client processed. If the session exists, belongs to the token's user and the buffer still holds
every event after `seq`, the server answers `{ "op": "resumed" }` followed by those events in order, then live events.
Otherwise it answers `{ "op": "invalid_session" }` and keeps the connection open: the client sends `identify` on it
and refetches what it needs over REST. A resume on a session that is attached elsewhere closes the other connection with `4009`.

Delivery is at-least-once between servers; the `seq` makes it exactly-once per session for the client, which only has to
ignore a `seq` it has already processed.

## Which events a client receives

Events are filtered by the server, per user, at delivery time:

- Channel events (`message.*`, `reaction.*`, `voice.state`, `channel.create`, `channel.update`, `channel.permissions`) only reach members
  who have `viewChannel` on that channel.
- Guild events (`guild.update`, `guild.delete`, `member.*`, `role.*`, `channel.delete`) reach every member of the guild.
- `guild.create` reaches only the user who created the guild.
- A permission change (role, member roles, overrides, ownership) takes effect for the next event, on every server replica, within 1 s.
  A client that lost access to a channel because of a `channel.permissions` or `channel.update` event receives a **tombstone**:
  a `channel.delete` `{ id, guildId }` for that channel (it carries no name or override, so nothing leaks). Treat it as "remove the channel
  from the list"; if access returns later, a `channel.permissions` arrives and the channel is refetched. Users who already could not see the channel
  get nothing. When access is lost through `role.*` or `member.update` there is no per-channel event: on those, and on `channel.permissions`,
  refetch the channel list.

## Events (`t` and `d`)

| `t` | `d` |
|---|---|
| `message.create` | `Message` (as in REST, including `attachments` with pre-signed download URLs that expire in 1 hour). `nonce` is present only for the sender, so it can replace its optimistic copy |
| `message.update` | `{ id, channelId, text, editedAt }` |
| `message.delete` | `{ id, channelId }` |
| `reaction.add` / `reaction.remove` | `{ channelId, messageId, userId, emoji }` |
| `guild.create` | `{ userId, guild: Guild }` |
| `guild.update` | `Guild` (ownership transferred) |
| `guild.delete` | `{ id }` |
| `member.join` | `{ guildId, userId, member: Member }` |
| `member.leave` | `{ guildId, userId, reason: "leave" \| "kick" \| "ban" }`. The leaver receives it too, and nothing from that guild afterwards |
| `member.update` | `{ guildId, userId, member: Member }` (roles changed) |
| `role.create` / `role.update` | `{ guildId, role: Role }`. Positions of other roles may have moved: refetch roles when order matters |
| `role.delete` | `{ guildId, roleId }` |
| `channel.create` / `channel.update` | `Channel`. Sibling positions may have moved: refetch the list when order matters |
| `channel.delete` | `{ id, guildId }` |
| `channel.permissions` | `{ channelId, guildId, override }` (an override was set, changed or removed) |
| `voice.state` | `{ channelId, connected, participant: VoiceParticipant }`. `connected: true`: joined the channel or changed mute/camera/share (upsert by `participant.user.id`); `false`: left. Moving between channels is a `false` for the old one then a `true` for the new one (each only reaches who sees that channel). Removal from the guild (leave, kick, ban) also produces `connected: false` |

Presence, typing and `voice.speaking` are not implemented yet.

## Client ops after `ready`

- `heartbeat` (above).
- `voice.state.set` `{ "muted": bool, "camOn": bool, "sharing": bool }`: the caller's own state while in a voice channel (servers with `voice` only;
  elsewhere it is an invalid frame, `4002`). No ack: the result is the `voice.state` event, which the sender receives too.
  Without `speak` in the channel, `muted` is forced to `true`. It is ignored if the caller is not in a voice channel.

## Client frame limits

Each connection has a token bucket for client frames (server defaults: 10 frames/s, burst 20; a heartbeat costs 1, `voice.state.set` costs 4).
Over the limit the server closes with `4011`. A user may hold a bounded number of sessions per server (default 20, live or waiting
for `resume`; abandoned ones are reclaimed first); beyond that `identify` closes with `4012`. Both limits are per server replica.

## Slow clients

Each connection has a bounded send queue. A client that stops reading is disconnected (`4008`, or the socket is cut if it
is not reading at all); other clients are unaffected. Reconnect and `resume`: the events it missed are still in the buffer.

## Close codes

| Code | Meaning | Client action |
|---|---|---|
| 1001 | server shutting down | reconnect with backoff and jitter, then resume |
| 4001 | invalid or expired token | refresh the token, reconnect once |
| 4002 | invalid frame or opcode | fix the client |
| 4003 | no `identify`/`resume` within 10 s | reconnect |
| 4008 | slow consumer | reconnect, resume |
| 4009 | the session was resumed by another connection | do not reconnect that session |
| 4010 | heartbeat timeout | reconnect, resume |
| 4011 | too many client frames on this connection (token bucket; `voice.state.set` counts as 4 frames) | back off, reconnect, resume |
| 4012 | too many open sessions for this user on this server | close other sessions/devices; do not retry in a loop |

Reconnect with exponential backoff plus jitter. Several server replicas may serve the same user; a resume must reach the
replica that holds the session, otherwise the client gets `invalid_session` and refetches.
