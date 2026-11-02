# Kurz gateway (contract 0.4)

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
   `{ "op": "ready", "d": { "sessionId", "user": { "id" }, "guilds": [ { "id", "name", "ownerId" } ], "heartbeatInterval" } }`.
   A bad or expired token closes with `4001`: refresh the access token and reconnect once.
4. After `ready` the server sends events. Anything committed before `ready` is read over REST *after* `ready`
   (list channels, members, messages); anything committed later arrives as a dispatch. An event may therefore arrive that
   the REST read already reflects: apply events idempotently (upsert/remove by id).

## Heartbeat

The client sends `{ "op": "heartbeat" }` every `heartbeatInterval` ms; the server answers `{ "op": "heartbeat_ack" }`.
No frame from the client for two intervals closes the connection with `4010`. The session survives for resume.

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

- Channel events (`message.*`, `reaction.*`, `channel.create`, `channel.update`, `channel.permissions`) only reach members
  who have `viewChannel` on that channel.
- Guild events (`guild.update`, `guild.delete`, `member.*`, `role.*`, `channel.delete`) reach every member of the guild.
- `guild.create` reaches only the user who created the guild.
- A permission change (role, member roles, overrides, ownership) takes effect for the next event, on every server replica, within 1 s.
  A client that lost access to a channel is not told: on `role.*`, `member.update` and `channel.permissions` it should refetch the channel list.

## Events (`t` and `d`)

| `t` | `d` |
|---|---|
| `message.create` | `Message` (as in REST). `nonce` is present only for the sender, so it can replace its optimistic copy |
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

`voice.state`, presence and typing are not implemented yet.

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

Reconnect with exponential backoff plus jitter. Several server replicas may serve the same user; a resume must reach the
replica that holds the session, otherwise the client gets `invalid_session` and refetches.
