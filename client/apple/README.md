# Kurz Community (app Apple)

App SwiftUI para macOS e iOS (alvos `KurzMac` e `KurzIOS`). Fala com um servidor Kurz (contrato 0.6, `contract/`) pelo pacote local
[`KurzKit`](KurzKit/README.md): login/registo, comunidades (guilds), canais de texto e de voz, mensagens em tempo real (gateway),
não-lidos e membros. A voz/vídeo/partilha de ecrã vai por WebRTC contra o SFU de `experimentos/sfu`.

## Subir o servidor local

```bash
cd server
make db-up                      # Postgres do docker compose (porta 5433)
KURZ_DATABASE_URL='postgres://kurz:kurz@localhost:5433/kurz?sslmode=disable' \
KURZ_JWT_SECRET='dev-secret-dev-secret-dev-secret-32' \
  go run ./cmd/kurz             # ou: make run   (escuta em :8080)
```

Variáveis úteis (tabela completa em `server/README.md`): `KURZ_REGISTRATION=open|closed`, `KURZ_RATELIMIT=off`.
Para ter canais de voz o servidor precisa de `KURZ_SECRET` (o mesmo do SFU) e `KURZ_SFU_URL` (`ws://<IP-da-LAN>:8081/v1/media`);
o SFU sobe com `cd experimentos/sfu && KURZ_SECRET=... SFU_UDP_PORT=30100 SFU_PUBLIC_IP=<LAN> go run .`.
Sem essas variáveis o servidor não anuncia a capability `voice` e o app mostra os canais de voz desativados, com uma nota.

O servidor de fábrica do app é `http://localhost:8080` (Info.plist já permite rede local). No iPhone físico, `localhost` é o
próprio telemóvel: adiciona o servidor pelo IP do Mac na LAN (botão do servidor no ecrã de entrada). Cada servidor tem a sua sessão
(tokens no Keychain por endereço); trocar de servidor troca de sessão.

## Gerar o projeto e abrir no Xcode

```bash
brew install xcodegen cocoapods
cd client/apple
xcodegen            # gera Kurz.xcodeproj a partir de project.yml; o postGenCommand corre `pod install` (WebRTC-SDK)
open Kurz.xcworkspace
```

Escolhe o esquema `KurzMac` ou `KurzIOS`. O `KurzKit` entra como pacote local (sem passo extra). A assinatura do `KurzMac` usa a
identidade fixa do `project.yml`; troca `DEVELOPMENT_TEAM`/`CODE_SIGN_IDENTITY` pelos teus.

## Testes

```bash
make test-kit-unit   # KurzKit sem Docker: 82 unitários (os 5 e2e aparecem como SKIPPED)
make test-kit        # sobe Postgres + servidor Go reais e roda tudo, incluindo o e2e
```

A lógica de estado (reducers de mensagens, canais, não-lidos, voz e eventos do gateway) vive no KurzKit (`Sources/KurzKit/State`)
e é testada lá; o app só a embrulha em `ObservableObject` (`WorkspaceModel`, `AuthModel`, `AppModel`).

## O que NÃO foi verificado

- **O app não foi compilado no Xcode** nem executado. Só há Command Line Tools nesta máquina (sem `xcodebuild`, `xcodegen`, Pods).
- **macOS**: typecheck com `swiftc -typecheck` (SDK das CLT) dos 29 ficheiros de `Sources/Shared` + `Sources/macOS`, contra um stub
  aproximado do módulo `WebRTC` e o KurzKit compilado à parte. Prova só tipos/APIs de SwiftUI/Foundation; não prova que o WebRTC real
  tenha as mesmas assinaturas do stub (os ficheiros `Media/*` não mudaram, exceto `MediaSession`, que só trocou o tipo `VoiceSession`).
- **iOS**: sem SDK, não há typecheck. `Sources/iOS/RootView.swift` foi conferido contra o SDK do macOS (único erro: `.toolbar(.hidden, for: .navigationBar)`,
  que é API de iOS); os ramos `#if os(iOS)` dos ficheiros partilhados e `iOS/VoiceRoomView.swift` só foram revistos à mão.
- **UI**: nenhuma tela foi vista a correr. Layout, scroll para o fim/paginação (`ScrollViewReader` + sentinela) e o menu de contexto são o
  maior risco de comportamento.
- **Voz de ponta a ponta com SFU real** (microfone, câmara, partilha, reconexão do SFU) só no Xcode, com o SFU de `experimentos/`.
  O REST/gateway de voz é coberto pelo e2e do KurzKit contra o servidor Go real (com SFU de mentira).
- Anexos (upload/preview), busca, cargos/permissões na UI, presença e `voice.speaking` não estão no app.
