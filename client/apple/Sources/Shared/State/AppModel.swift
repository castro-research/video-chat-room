import Combine
import Foundation

/// O estado da app: junta servidores, salas, a sala ligada e a captura local, e guarda o estado dos botões (mudo, câmara,
/// partilha). Cada peça vive no seu módulo; aqui só se ligam entre si. As mudanças das peças propagam-se para a UI.
@MainActor
final class AppModel: ObservableObject {
    let guest = Guest()
    let servers = ServerList()
    let rooms = RoomList()
    let local = LocalMedia()
    let session: RoomSession

    /// A sala real ligada (ou a ligar).
    @Published private(set) var roomId: String?
    @Published private(set) var muted = false
    @Published private(set) var camOn = false
    @Published private(set) var sharing = false
    /// Aviso de falha de captura (permissões, dispositivos); os da sala estão em [session].
    @Published var notice: String?

    @Published private(set) var screenQuality = ScreenQuality.normal
    @Published private(set) var screenSourceId: String?
    @Published private(set) var screenList: [ScreenSource] = []
    @Published private(set) var screenThumbs: [String: PlatformImage] = [:]
    @Published private(set) var micId: String?
    @Published private(set) var cameraId: String?

    private var roomTask: Task<Void, Never>?
    private var micTask: Task<Void, Never>?
    private var cameraTask: Task<Void, Never>?
    private var screenTask: Task<Void, Never>?
    private var bag = Set<AnyCancellable>()

    init() {
        session = RoomSession(userId: guest.userId, local: local)
        // a UI observa só o AppModel: as mudanças das peças passam por aqui
        for child in [servers.objectWillChange, rooms.objectWillChange, session.objectWillChange] {
            child.sink { [weak self] _ in self?.objectWillChange.send() }.store(in: &bag)
        }
        servers.$selected.dropFirst().sink { [weak self] url in
            guard let self else { return }
            if self.inVoice { self.leaveVoice() }  // as salas são de cada servidor
            self.rooms.follow(url)
        }.store(in: &bag)
#if os(macOS)
        local.screenSources.onChange = { [weak self] list, thumbs in self?.screenList = list; self?.screenThumbs = thumbs }
        local.screen.onStopped = { [weak self] in self?.stopSharing() }
#endif
        micId = local.devices.inputId
        cameraId = local.camera.rememberedId
        servers.startChecks()
        rooms.follow(servers.selected)
    }

    var inVoice: Bool { roomId != nil }

    /// Matiz do avatar de [userId]; o próprio usa o seu.
    func hue(of userId: String) -> Double {
        userId == guest.userId ? guest.hue : Double(userId.unicodeScalars.reduce(0) { ($0 &* 31 &+ Int($1.value)) & 0x7fffffff } % 360)
    }

    // --- salas (UI otimista: a UI mostra a sala clicada já, com o que se sabe, e o próprio lá dentro) ---

    /// A sala ligada como a UI a mostra: a do SFU se já respondeu, senão a da listagem; nil se o código nem está listado.
    var shownRoom: Room? {
        guard let id = roomId else { return nil }
        return session.room.flatMap { $0.id == id ? $0 : nil } ?? rooms.rooms.first { $0.id == id }
    }

    /// Quem a UI mostra em [room]: na sala ligada o próprio entra já (com o estado dos botões), nas outras sai já.
    func people(in room: Room) -> [Participant] {
        let known = roomId == room.id ? shownRoom ?? room : room
        let others = known.participants.filter { $0.userId != guest.userId }
        guard roomId == room.id else { return others }
        return [Participant(userId: guest.userId, name: guest.name, mic: !muted, camera: camOn, screen: sharing)] + others
    }

    func joinRoom(_ id: String) {
        guard roomId != id else { return }
        roomId = id
        // cada sala espera que a anterior acabe de sair (senão a saída da velha apanhava a sessão da nova)
        let previous = roomTask
        previous?.cancel()
        roomTask = Task { [weak self] in
            await previous?.value
            guard let self, self.roomId == id else { return }
            await self.session.run(base: self.servers.selected, roomId: id, name: self.guest.name)
            if self.roomId == id { self.leaveVoice() }  // falhou a entrada ou perdeu-se a ligação de vez
        }
        syncCapture()
    }

    func leaveVoice() {
        roomId = nil; muted = false; camOn = false; sharing = false
        roomTask?.cancel(); roomTask = nil
        syncCapture()
    }

    /// Cria o canal e entra nele; nil (com aviso) se falhou.
    func createRoom(_ name: String) async -> Bool {
        do {
            let room = try await rooms.create(name)
            joinRoom(room.id)
            return true
        } catch {
            notice = error.localizedDescription
            return false
        }
    }

    // --- botões: cada um liga/desliga a captura respectiva; um pedido recusado repõe o botão ---

    func setMuted(_ on: Bool) { muted = on; syncMic() }
    func setCamera(_ on: Bool) {
        camOn = on
#if os(iOS)
        cameraId = nil  // ao religar volta a abrir a frontal
#endif
        syncCamera()
    }
    func stopSharing() { sharing = false; syncScreen() }

    /// Liga a partilha com [source] (ou troca, se já estava a partilhar).
    func share(_ source: ScreenSource) {
        screenSourceId = source.id
        sharing = true
        syncScreen()
    }

    func setScreenQuality(_ q: ScreenQuality) {
        guard q != screenQuality else { return }
        screenQuality = q
        if sharing { syncScreen() }
    }

    func refreshScreenSources() {
#if os(macOS)
        local.screenSources.refresh()
#endif
    }

    func selectMic(_ id: String?) {
        guard local.devices.select(input: id) else { notice = "Não foi possível mudar de microfone."; return }
        micId = id
    }

    func selectSpeaker(_ id: String?) {
        if !local.devices.select(output: id) { notice = "Não foi possível mudar a saída de áudio." }
    }

    /// Alterna entre a câmara frontal e a traseira (a frontal é a que abre por defeito).
    func flipCamera() {
        let all = CameraCapture.cameras
        let current = all.first { $0.id == cameraId } ?? all.first { $0.front } ?? all.first
        guard let next = all.first(where: { $0.front != current?.front }) else { return }
        selectCamera(next.id)
    }

    func selectCamera(_ id: String) {
        cameraId = id
        guard camOn else { return }
        serial(&cameraTask) { [self] in
            if await !local.camera.switchTo(deviceId: id) { notice = Msg.noCamera }
        }
    }

    private func syncCapture() { syncMic(); syncCamera(); syncScreen() }

    private func syncMic() {
        serial(&micTask) { [self] in
            if inVoice && !muted {
                if await !local.startMic(), !Task.isCancelled { muted = true; notice = Msg.noMic }
            } else {
                local.stopMic()
            }
        }
    }

    private func syncCamera() {
        serial(&cameraTask) { [self] in
            if inVoice && camOn {
                if await !local.startCamera(), !Task.isCancelled { camOn = false; notice = Msg.noCamera }
            } else {
                local.stopCamera()
            }
        }
    }

    private func syncScreen() {
        serial(&screenTask) { [self] in
            if inVoice && sharing {
                if await !local.startScreen(source: screenSourceId, quality: screenQuality), !Task.isCancelled {
                    sharing = false; screenSourceId = nil; notice = Msg.noScreen  // a janela pode ter fechado: volta ao ecrã
                }
            } else {
                local.stopScreen()
            }
        }
    }

    /// Corre [body] depois de a tarefa anterior da mesma fonte acabar (cancelando-a): as ordens de ligar/desligar não se cruzam.
    private func serial(_ slot: inout Task<Void, Never>?, _ body: @escaping @MainActor () async -> Void) {
        let previous = slot
        previous?.cancel()
        slot = Task { await previous?.value; await body() }
    }
}
