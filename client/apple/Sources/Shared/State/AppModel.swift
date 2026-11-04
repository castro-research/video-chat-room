import Combine
import Foundation
import KurzKit

/// O estado da app: junta servidores, sessão, dados do servidor (guilds/canais/mensagens), a sala de voz ligada e a captura local,
/// e guarda o estado dos botões (mudo, câmara, partilha). Cada peça vive no seu módulo; aqui só se ligam entre si.
@MainActor
final class AppModel: ObservableObject {
    let servers = ServerList()
    let auth: AuthModel
    let local = LocalMedia()
    let session: RoomSession

    /// Os dados do servidor ligado; só existe com a sessão iniciada.
    @Published private(set) var workspace: WorkspaceModel?
    /// O canal de voz ligado (ou a ligar).
    @Published private(set) var voiceChannel: Channel?
    /// A vista principal mostra a sala de voz (senão, o canal de texto).
    @Published var showingVoice = false
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
        auth = AuthModel(baseURL: servers.selected)
        session = RoomSession(local: local)
        // a UI observa o AppModel para o essencial: as mudanças das peças passam por aqui (os dados pesados vão por EnvironmentObject)
        for child in [servers.objectWillChange, auth.objectWillChange, session.objectWillChange] {
            child.sink { [weak self] _ in self?.objectWillChange.send() }.store(in: &bag)
        }
        // trocar de servidor: sai da sala (REST e SFU) enquanto `workspace.api` ainda é a sessão do servidor antigo
        servers.$selected.dropFirst().sink { [weak self] url in self?.leaveVoice(); self?.auth.use(url) }.store(in: &bag)
        auth.$phase.removeDuplicates().sink { [weak self] phase in self?.phaseChanged(phase) }.store(in: &bag)
        // mudo/câmara/partilha refletem-se no gateway enquanto se está numa sala
        Publishers.CombineLatest3($muted, $camOn, $sharing).dropFirst().sink { [weak self] _ in self?.pushVoiceState() }.store(in: &bag)
        session.onJoined = { [weak self] in self?.pushVoiceState() }
#if os(macOS)
        local.screenSources.onChange = { [weak self] list, thumbs in self?.screenList = list; self?.screenThumbs = thumbs }
        local.screen.onStopped = { [weak self] in self?.stopSharing() }
#endif
        micId = local.devices.inputId
        cameraId = local.camera.rememberedId
        servers.startChecks()
        Task { await auth.restore() }
    }

    var inVoice: Bool { voiceChannel != nil }
    var meId: String { auth.user?.id ?? "" }
    var meName: String { auth.user?.name ?? "" }

    /// A sessão começou, acabou ou mudou de servidor: os dados e a voz são sempre da sessão atual.
    private func phaseChanged(_ phase: AuthModel.Phase) {
        if inVoice { leaveVoice() }
        workspace?.stop()
        workspace = nil
        showingVoice = false
        if phase == .signedIn, let me = auth.user {
            let w = WorkspaceModel(api: auth.api, me: me, info: auth.info)
            workspace = w
            w.start()
        }
    }

    // --- voz ---

    /// Quem a UI mostra no canal de voz [channelId]: o roster do gateway; no canal ligado o próprio entra já, com o estado dos botões.
    func people(in channelId: String) -> [VoiceParticipant] {
        let roster = workspace?.ws.voice.participants(in: channelId) ?? []
        let others = roster.filter { $0.id != meId }
        guard voiceChannel?.id == channelId, let me = auth.user else { return others }
        return [VoiceParticipant(user: me, muted: muted, camOn: camOn, sharing: sharing)] + others
    }

    /// Entra na sala de voz (move-se se estava noutra) e mostra-a. Com a capability `voice` em falta, avisa e não faz nada.
    func joinVoice(_ channel: Channel) {
        guard channel.kind == .voice else { return }
        guard workspace?.supportsVoice == true else { notice = Msg.noVoice; return }
        showingVoice = true
        guard voiceChannel?.id != channel.id, let api = workspace?.api else { return }
        voiceChannel = channel
        let id = channel.id, uid = meId
        // cada sala espera que a anterior acabe de sair (senão a saída da velha apanhava a sessão da nova)
        let previous = roomTask
        previous?.cancel()
        roomTask = Task { [weak self] in
            await previous?.value
            guard let self, self.voiceChannel?.id == id else { return }
            await self.session.run(api: api, channelId: id, userId: uid)
            if self.voiceChannel?.id == id { self.leaveVoice() }  // falhou a entrada ou perdeu-se a ligação de vez
        }
        syncCapture()
    }

    /// Terminar sessão: a sala é deixada no servidor antes de os tokens irem embora (senão o `leave` falhava e a voz só caía no varrimento).
    func logout() async {
        let left = voiceChannel
        leaveVoice(notifyServer: false)  // o estado local e o SFU acabam já
        await auth.logout(leavingVoice: left?.id)
    }

    /// [notifyServer] false: quem chama trata do `leave` REST (ver `logout`). Com a sessão expirada o `leave` é só uma tentativa: a saída local é imediata.
    func leaveVoice(notifyServer: Bool = true) {
        let left = voiceChannel
        voiceChannel = nil; muted = false; camOn = false; sharing = false; showingVoice = false
        roomTask?.cancel(); roomTask = nil
        syncCapture()
        // o servidor tira-nos do estado de voz (idempotente); sem isto só o timeout de 3 min o faria
        if notifyServer, let left, let api = workspace?.api { Task { try? await api.leaveVoice(channelId: left.id) } }
    }

    private func pushVoiceState() {
        guard inVoice else { return }
        workspace?.setVoiceState(muted: muted, camOn: camOn, sharing: sharing)
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
