# KurzKit

Núcleo Swift do cliente Apple do Kurz: modelos do contrato 0.6, REST, sessão com refresh automático e gateway (WebSocket).
Só Foundation + URLSession (+ Security para o Keychain). macOS 13 / iOS 16, Swift 5 mode, sem dependências. Sem UI.

## Testes

Sem Xcode (só Command Line Tools) o XCTest não existe e o Swift Testing não está no caminho padrão; use o script, que ajusta isso:

```bash
cd client/apple
make test-kit-unit   # KurzKit/Scripts/swift-test.sh: 59 unitários (URLProtocol e WebSocket falsos); o e2e aparece como SKIPPED
make test-kit        # KurzKit/Scripts/e2e.sh: builda o servidor Go, sobe Postgres (docker compose, banco descartável), roda TUDO com KURZ_E2E_URL
```

Com Xcode, `swift test` puro também funciona. O e2e (`E2ETests.swift`) só roda com `KURZ_E2E_URL=http://localhost:PORTA` apontando para um
servidor de pé; sem a variável os 4 testes ficam SKIPPED com a razão. O script sobe o servidor com `KURZ_RATELIMIT=off` e voz ligada
(`KURZ_SECRET` + `KURZ_SFU_URL=ws://localhost:9999/...`, SFU de mentira: só o REST/gateway de voz é exercitado). `KURZ_E2E_NOVOICE=1 make test-kit`
sobe sem SFU e o teste de voz passa a esperar 501 `not_supported`. Precisa de Docker e Go.

## API pública

```swift
let api = KurzAPI(baseURL: "http://localhost:8080")          // store padrão: KeychainTokenStore(); InMemoryTokenStore() em testes
let info = try await api.wellKnown()                          // ServerInfo: contract, registration, capabilities
let s = try await api.login(username: "ana", password: "…")   // ou register(...); guarda access+refresh por baseURL
for await state in api.authStates() { … }                     // .authenticated / .unauthenticated(.sessionExpired|.loggedOut|.noCredentials)

try await api.guilds(); createGuild(name:); guild(_:)
try await api.createInvite(guildId:maxUses:expiresIn:); previewInvite(code:); joinInvite(code:)
try await api.members(guildId:after:limit:); roles(guildId:); channels(guildId:); createChannel(guildId:name:kind:parentId:)
try await api.messages(channelId:before:after:around:limit:)  // MessagePage, sempre do mais antigo ao mais novo
try await api.sendMessage(channelId:text:replyToId:nonce:); editMessage(...); deleteMessage(...)
try await api.addReaction(...); removeReaction(...); markRead(channelId:messageId:); readStates(guildId:)
try await api.joinVoice(channelId:) -> VoiceSession; leaveVoice(channelId:)

let gw = api.makeGateway()
let events = gw.events(); let conn = gw.connectionStates()    // assine ANTES de start()
await gw.start()
for await e in events { switch e { case .ready: /* refaça o fetch por REST */ case .resumed: break
                                    case .messageCreate(let m): …  case .unknown(let name, _): … } }
try await gw.setVoiceState(muted:camOn:sharing:); await gw.stop()
```

- Erros são `KurzError` (`code` do servidor, `status`, `message` em português, `retryAfter` no 429).
- Refresh: 401 `token_expired` ou token a menos de 30 s de expirar renova e repete UMA vez; vários pedidos simultâneos disparam UM refresh
  (o refresh é rotativo). Se o servidor recusar o refresh, o store é limpo e `authStates()` emite `.unauthenticated(.sessionExpired)`;
  rede fora ou 5xx mantêm os tokens.
- Gateway: hello, identify, ready; heartbeat com detecção de servidor mudo; `seq` por sessão (repetidos são ignorados); em queda tenta
  `resume` (sessionId + seq); `invalid_session` vira `.invalidSession` seguido de novo `.ready`; 4001 renova o token e reconecta uma vez;
  4009 e 4002 não reconectam; backoff exponencial com jitter (`Backoff`). `stop()` é final e encerra os streams.
- `VoiceSession` tem os mesmos campos que o do app (`media.url/token`, `iceServers`) mais `participants`; `IceServer` idem.
