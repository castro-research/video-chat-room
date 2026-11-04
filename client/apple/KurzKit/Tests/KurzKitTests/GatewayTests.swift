import Testing
@testable import KurzKit

final class WeakBox<T: AnyObject>: @unchecked Sendable { weak var value: T? }

@Suite("Gateway com WebSocket falso")
struct GatewayTests {
    static let fast = Backoff(base: 0.02, cap: 0.05)

    func make(_ server: FakeGatewayServer, tokens: TokenLog = TokenLog()) -> Gateway {
        Gateway(url: URL(string: "ws://fake/v1/gateway")!, backoff: Self.fast, connect: { server.connect($0) }) { force in
            try tokens.record(force)
        }
    }

    final class TokenLog: @unchecked Sendable {
        private let lock = NSLock()
        private var forces: [Bool] = []
        var failWith: KurzError?
        func record(_ force: Bool) throws -> String {
            lock.lock(); defer { lock.unlock() }
            forces.append(force)
            if let failWith { throw failWith }
            return "tok\(forces.count)"
        }
        var calls: [Bool] { lock.lock(); defer { lock.unlock() }; return forces }
    }

    func dispatch(_ seq: Int, _ t: String, _ d: String) -> String { #"{"op":"dispatch","t":"\#(t)","seq":\#(seq),"d":\#(d)}"# }

    @Test func handshakeIdentifyReadyEDespacho() async throws {
        let server = FakeGatewayServer()
        let gw = make(server)
        let events = Collector(gw.events())
        let states = Collector(gw.connectionStates())
        await gw.start()
        await eventually("ready") { events.all.contains { if case .ready = $0 { return true } else { return false } } }
        let s0 = server.socket(0)
        #expect(s0.sent(op: "identify").count == 1)
        #expect((s0.sent(op: "identify")[0]["d"] as? [String: Any])?["token"] as? String == "tok1")
        guard case .ready(let r)? = events.all.first else { Issue.record("sem ready"); return }
        #expect(r.sessionId == "sess1" && r.userId == "100" && r.guilds.first?.name == "Casa" && r.heartbeatInterval == 30)
        s0.push(dispatch(1, "message.create", Sample.message))
        s0.push(dispatch(2, "typing.start", #"{"a":1}"#))
        await eventually("dois eventos") { events.all.count == 3 }
        if case .messageCreate(let m) = events.all[1] { #expect(m.text == "olá") } else { Issue.record("message.create") }
        if case .unknown(let n, _) = events.all[2] { #expect(n == "typing.start") } else { Issue.record("unknown") }
        #expect(await gw.currentSeq == 2)
        #expect(states.all == [.disconnected, .connecting, .connected])
        await gw.stop()
    }

    @Test func seqRepetidoEIgnorado() async throws {
        let server = FakeGatewayServer()
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        let s = server.socket(0)
        s.push(dispatch(1, "message.delete", #"{"id":"1","channelId":"2"}"#))
        s.push(dispatch(1, "message.delete", #"{"id":"1","channelId":"2"}"#))
        s.push(dispatch(2, "message.delete", #"{"id":"3","channelId":"2"}"#))
        await eventually("2 dispatches") { events.all.count == 3 }
        try await Task.sleep(nanoseconds: 100_000_000)
        #expect(events.all.count == 3)
        await gw.stop()
    }

    @Test func quedaDoSocketReconectaEFazResumeComSeq() async throws {
        let server = FakeGatewayServer()
        let tokens = TokenLog()
        let gw = make(server, tokens: tokens)
        let events = Collector(gw.events())
        let states = Collector(gw.connectionStates())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        server.socket(0).push(dispatch(1, "message.delete", #"{"id":"1","channelId":"2"}"#))
        server.socket(0).push(dispatch(2, "message.delete", #"{"id":"2","channelId":"2"}"#))
        await eventually("seq 2") { await gw.currentSeq == 2 }
        server.socket(0).drop(code: 4010)
        await eventually("segunda conexão") { server.count == 2 }
        await eventually("resume enviado") { !server.socket(1).sent(op: "resume").isEmpty }
        let d = server.socket(1).sent(op: "resume")[0]["d"] as! [String: Any]
        #expect(d["sessionId"] as? String == "sess1" && d["seq"] as? Int == 2 && d["token"] as? String == "tok2")
        #expect(server.socket(1).sent(op: "identify").isEmpty)
        await eventually("resumed") { events.all.contains(.resumed) }
        #expect(states.all.contains(.reconnecting) && states.all.last == .connected)
        #expect(!events.all.contains(.invalidSession))
        // depois do resume o seq continua de onde estava
        server.socket(1).push(dispatch(3, "message.delete", #"{"id":"3","channelId":"2"}"#))
        await eventually("evento 3") { events.all.count == 5 }
        await gw.stop()
    }

    @Test func invalidSessionFazIdentifyESinalizaParaRefazerOFetch() async throws {
        let server = FakeGatewayServer()
        server.resumeAccepted = false
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        server.socket(0).drop(code: 1001)
        await eventually("invalid_session + novo ready") { events.all.count == 3 }
        #expect(events.all[1] == .invalidSession)
        if case .ready(let r) = events.all[2] { #expect(r.sessionId == "sess2") } else { Issue.record("sem novo ready") }
        #expect(server.socket(1).sent(op: "resume").count == 1 && server.socket(1).sent(op: "identify").count == 1)
        #expect(await gw.currentSeq == 0)
        await gw.stop()
    }

    @Test func fechamento4001RenovaOTokenEReconecta() async throws {
        let server = FakeGatewayServer()
        let tokens = TokenLog()
        let gw = make(server, tokens: tokens)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        server.socket(0).drop(code: 4001)
        await eventually("reconectou") { server.count == 2 }
        await eventually("token") { tokens.calls.count >= 2 }
        #expect(tokens.calls == [false, true])
        await gw.stop()
    }

    @Test func fechamento4009NaoReconecta() async throws {
        let server = FakeGatewayServer()
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        server.socket(0).drop(code: 4009)
        await eventually("desconectado") { gw.state == .disconnected }
        try await Task.sleep(nanoseconds: 200_000_000)
        #expect(server.count == 1)
    }

    @Test func sessaoPerdidaNoRefreshParaDeVez() async throws {
        let server = FakeGatewayServer()
        let tokens = TokenLog()
        tokens.failWith = .unauthenticated()
        let gw = make(server, tokens: tokens)
        await gw.start()
        await eventually("desconectado") { gw.state == .disconnected && server.count == 1 }
        try await Task.sleep(nanoseconds: 150_000_000)
        #expect(server.count == 1 && server.socket(0).sent(op: "identify").isEmpty)
    }

    @Test func heartbeatPeriodico() async throws {
        let server = FakeGatewayServer()
        server.heartbeatMs = 30
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        await eventually("3 heartbeats") { server.socket(0).sent(op: "heartbeat").count >= 3 }
        await gw.stop()
    }

    @Test func servidorMudoSemAckForcaReconexao() async throws {
        let server = FakeGatewayServer()
        server.heartbeatMs = 30
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        server.socket(0).onSend = { _, _ in } // para de responder (nem ack)
        await eventually("reconexão") { server.count >= 2 }
        await gw.stop()
    }

    @Test func voiceStateSetEnviaOQuadro() async throws {
        let server = FakeGatewayServer()
        let gw = make(server)
        let events = Collector(gw.events())
        await gw.start()
        await eventually("ready") { events.all.count == 1 }
        try await gw.setVoiceState(muted: true, camOn: false, sharing: true)
        let f = server.socket(0).sent(op: "voice.state.set")
        #expect(f.count == 1)
        let d = f[0]["d"] as? [String: Any]
        #expect(d?["muted"] as? Bool == true && d?["camOn"] as? Bool == false && d?["sharing"] as? Bool == true)
        await gw.stop()
        do { try await gw.setVoiceState(muted: false, camOn: false, sharing: false); Issue.record("devia falhar") } catch {}
    }

    @Test func stopFechaSocketEncerraStreamsELiberaMemoria() async throws {
        let server = FakeGatewayServer()
        let weakGw = WeakBox<Gateway>()
        let events: Collector<GatewayEvent>
        do {
            let gw = make(server)
            weakGw.value = gw
            events = Collector(gw.events())
            await gw.start()
            await eventually("ready") { events.all.count == 1 }
            await gw.stop()
            #expect(server.socket(0).closed)
            #expect(gw.state == .disconnected)
        }
        await eventually("gateway liberado (sem retain cycle)") { weakGw.value == nil }
        events.cancel()
    }

    @Test func servidorInalcancavelNaoVazaENaoTravaOStop() async throws {
        let weakGw = WeakBox<Gateway>()
        do {
            let gw = Gateway(url: URL(string: "ws://127.0.0.1:9/v1/gateway")!, backoff: Self.fast, connect: { url in
                URLSessionWebSocket(session: .shared, url: url)
            }) { _ in "t" }
            weakGw.value = gw
            await gw.start()
            try await Task.sleep(nanoseconds: 300_000_000)
            #expect(gw.state == .reconnecting || gw.state == .connecting)
            await gw.stop()
        }
        await eventually("liberado") { weakGw.value == nil }
    }
}
