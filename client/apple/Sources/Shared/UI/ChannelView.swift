import KurzKit
import SwiftUI

/// O canal de texto aberto: mensagens (paginação para trás ao rolar para cima), composer, resposta, edição e reações.
struct ChannelView: View {
    @EnvironmentObject var wm: WorkspaceModel
    let channel: Channel
    /// Mac: mostra/esconde o painel de membros. No iPhone, nil (os membros abrem numa folha).
    var membersToggle: Binding<Bool>?
    var onMembers: (() -> Void)?
    @State private var text = ""

    private static let quick = ["👍", "❤️", "😂", "🎉", "😮", "🙏"]

    var body: some View {
        VStack(spacing: 0) {
#if os(macOS)
            header
            Divider().overlay(P.border)
#endif
            ConnectionBanner(state: wm.usesGateway ? wm.connection : .connected)
            messageList
            composer
        }
        .background(P.main)
#if os(iOS)
        .navigationTitle("# " + channel.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .navigationBarTrailing) { Button { onMembers?() } label: { Image(systemName: "person.2") }.accessibilityLabel("Membros") } }
        .onDisappear { wm.closeChannel() }
#endif
    }

#if os(macOS)
    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "number").foregroundColor(P.textMuted)
            Text(channel.name).font(.system(size: 15, weight: .semibold)).foregroundColor(P.text)
            Spacer()
            if let membersToggle {
                Button { membersToggle.wrappedValue.toggle() } label: {
                    Image(systemName: "person.2").foregroundColor(membersToggle.wrappedValue ? P.text : P.textMuted).frame(width: 28, height: 28)
                }.buttonStyle(.plain).help("Membros")
            }
        }.padding(.horizontal, 16).frame(height: 48)
    }
#endif

    // MARK: Mensagens

    private var messageList: some View {
        let list = wm.messages
        let rows = list.rows
        return ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if list.hasMore {
                        ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(8)
                            .onAppear { loadOlder(proxy, firstId: rows.first?.id) }
                    } else if list.loaded {
                        Text(rows.isEmpty ? "Ainda não há mensagens em #\(channel.name). Escreve a primeira." : "Início de #\(channel.name)")
                            .font(.system(size: 12)).foregroundColor(P.textFaint).frame(maxWidth: .infinity).padding(16)
                    }
                    ForEach(Array(rows.enumerated()), id: \.element.id) { i, row in
                        switch row {
                        case .sent(let m):
                            MessageItem(message: m, head: showHead(rows, i), reply: m.replyToId.flatMap(list.message), quick: Self.quick,
                                        onEdit: { wm.editing = m; wm.replyTo = nil; text = m.text })
                        case .sending(let p):
                            PendingItem(pending: p, head: showHead(rows, i))
                        }
                    }
                    // sentinela: visível = a lista está no fim (as mensagens novas contam como lidas)
                    Color.clear.frame(height: 1).id("bottom")
                        .onAppear { wm.setAtBottom(true) }
                        .onDisappear { wm.setAtBottom(false) }
                }.padding(.vertical, 8)
            }
            .onChange(of: rows.last?.id) { _ in
                // mensagem nova: segue-a se já estávamos no fim ou se fomos nós a enviar
                var sending = false
                if case .sending? = rows.last { sending = true }
                if wm.ws.viewing == channel.id || sending { proxy.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: list.loaded) { _ in proxy.scrollTo("bottom", anchor: .bottom) }
            .onAppear { proxy.scrollTo("bottom", anchor: .bottom) }
        }
    }

    /// Mostra autor e hora quando muda o autor ou passaram 5 min (as respostas também levam cabeçalho).
    private func showHead(_ rows: [MessageRow], _ i: Int) -> Bool {
        guard i > 0 else { return true }
        func info(_ r: MessageRow) -> (String, Date, Bool) {
            switch r {
            case .sent(let m): return (m.author.id, m.createdAt, m.replyToId != nil)
            case .sending(let p): return (wm.meId, p.createdAt, p.replyToId != nil)
            }
        }
        let (a, ta, _) = info(rows[i - 1]), (b, tb, replying) = info(rows[i])
        return a != b || replying || tb.timeIntervalSince(ta) > 300
    }

    private func loadOlder(_ proxy: ScrollViewProxy, firstId: String?) {
        Task {
            await wm.loadOlder()
            if let firstId { proxy.scrollTo(firstId, anchor: .top) }  // mantém o ponto onde estava
        }
    }

    // MARK: Composer

    private var composer: some View {
        VStack(spacing: 0) {
            if let r = wm.replyTo {
                bar("arrowshape.turn.up.left", "A responder a \(r.author.name)", r.text) { wm.replyTo = nil }
            }
            if let e = wm.editing {
                bar("pencil", "A editar a mensagem", e.text) { wm.editing = nil; text = "" }
            }
            HStack(alignment: .bottom, spacing: 8) {
                TextField(wm.editing == nil ? "Mensagem para #\(channel.name)" : "Editar mensagem", text: $text, axis: .vertical)
                    .lineLimit(1...5).textFieldStyle(.plain).padding(10)
                    .background(P.input, in: RoundedRectangle(cornerRadius: 10))
                    .onSubmit(submit)
                Button(action: submit) {
                    Image(systemName: wm.editing == nil ? "paperplane.fill" : "checkmark").foregroundColor(.white).frame(width: 38, height: 38)
                        .background(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? P.textFaint : P.accent, in: Circle())
                }.buttonStyle(.plain).disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty).accessibilityLabel("Enviar")
            }.padding(12)
        }
        .overlay(alignment: .top) { Divider().overlay(P.border) }
    }

    private func bar(_ icon: String, _ title: String, _ detail: String, close: @escaping () -> Void) -> some View {
        HStack(spacing: 8) {
            Image(systemName: icon).font(.system(size: 12)).foregroundColor(P.textMuted)
            Text(title).font(.system(size: 12, weight: .semibold)).foregroundColor(P.text)
            Text(detail).font(.system(size: 12)).foregroundColor(P.textFaint).lineLimit(1)
            Spacer()
            Button(action: close) { Image(systemName: "xmark").font(.system(size: 11)).foregroundColor(P.textMuted).frame(width: 24, height: 24) }.buttonStyle(.plain)
        }.padding(.horizontal, 16).padding(.top, 8)
    }

    private func submit() {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        if let m = wm.editing { wm.edit(m, text: t) } else { wm.send(t) }
        text = ""
    }
}

/// Uma mensagem: autor/hora/editado, resposta, texto, anexos e reações; o menu de contexto (clique direito / toque longo) tem as ações.
private struct MessageItem: View {
    @EnvironmentObject var wm: WorkspaceModel
    let message: Message
    let head: Bool
    let reply: Message?
    let quick: [String]
    let onEdit: () -> Void

    private var mine: Bool { message.author.id == wm.meId }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            if head { Avatar(name: message.author.name, hue: message.author.avatarHue, size: 36) } else { Color.clear.frame(width: 36, height: 1) }
            VStack(alignment: .leading, spacing: 2) {
                if message.replyToId != nil { replyLine }
                if head {
                    HStack(spacing: 6) {
                        Text(message.author.name).font(.system(size: 13, weight: .semibold)).foregroundColor(P.text)
                        Text(message.createdAt.formatted(date: .omitted, time: .shortened)).font(.system(size: 11)).foregroundColor(P.textFaint)
                    }
                }
                if !message.text.isEmpty {
                    (Text(message.text) + (message.editedAt != nil ? Text("  (editado)").font(.system(size: 11)).foregroundColor(P.textFaint) : Text("")))
                        .font(.system(size: 14)).foregroundColor(P.text).textSelection(.enabled)
                }
                ForEach(message.attachments) { a in
                    Label(a.name ?? "anexo", systemImage: "paperclip").font(.system(size: 12)).foregroundColor(P.accent)
                }
                if !message.reactions.isEmpty { reactions }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 16).padding(.top, head ? 8 : 1).padding(.bottom, 1)
        .contentShape(Rectangle())
        .contextMenu {
            Button { wm.replyTo = message; wm.editing = nil } label: { Label("Responder", systemImage: "arrowshape.turn.up.left") }
            Menu {
                ForEach(quick, id: \.self) { e in Button(e) { wm.toggleReaction(message, emoji: e) } }
            } label: { Label("Reagir", systemImage: "face.smiling") }
            Button { copyToClipboard(message.text) } label: { Label("Copiar texto", systemImage: "doc.on.doc") }
            if mine {
                Button(action: onEdit) { Label("Editar", systemImage: "pencil") }
                Button(role: .destructive) { wm.delete(message) } label: { Label("Apagar", systemImage: "trash") }
            }
        }
    }

    private var replyLine: some View {
        HStack(spacing: 4) {
            Image(systemName: "arrowshape.turn.up.left.fill").font(.system(size: 9))
            if let r = reply {
                Text(r.author.name).font(.system(size: 11.5, weight: .semibold))
                Text(r.text).font(.system(size: 11.5)).lineLimit(1)
            } else {
                Text("mensagem anterior").font(.system(size: 11.5)).italic()
            }
        }.foregroundColor(P.textFaint)
    }

    private var reactions: some View {
        HStack(spacing: 4) {
            ForEach(message.reactions, id: \.emoji) { r in
                Button { wm.toggleReaction(message, emoji: r.emoji) } label: {
                    HStack(spacing: 4) {
                        Text(r.emoji).font(.system(size: 13))
                        Text("\(r.count)").font(.system(size: 11.5, weight: .semibold)).foregroundColor(r.me ? P.accent : P.textMuted)
                    }.padding(.horizontal, 7).frame(height: 22)
                    .background(r.me ? P.accent.opacity(0.18) : P.input, in: Capsule())
                    .overlay(Capsule().stroke(r.me ? P.accent : .clear, lineWidth: 1))
                }.buttonStyle(.plain)
            }
        }.padding(.top, 2)
    }
}

/// A mensagem ainda sem confirmação do servidor (ou que falhou): meio apagada, com tentar de novo / descartar.
private struct PendingItem: View {
    @EnvironmentObject var app: AppModel
    @EnvironmentObject var wm: WorkspaceModel
    let pending: PendingMessage
    let head: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            if head {
                Avatar(name: app.meName, hue: app.auth.user?.avatarHue ?? 0, size: 36)
            } else {
                Color.clear.frame(width: 36, height: 1)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text(pending.text).font(.system(size: 14)).foregroundColor(pending.failed ? P.danger : P.textMuted)
                if pending.failed {
                    HStack(spacing: 12) {
                        Text("Não foi enviada.").font(.system(size: 11.5)).foregroundColor(P.danger)
                        Button("Tentar de novo") { wm.retry(pending) }.buttonStyle(.plain).foregroundColor(P.accent).font(.system(size: 11.5))
                        Button("Descartar") { wm.discard(pending) }.buttonStyle(.plain).foregroundColor(P.accent).font(.system(size: 11.5))
                    }
                } else {
                    Text("A enviar…").font(.system(size: 11)).foregroundColor(P.textFaint)
                }
            }
            Spacer(minLength: 0)
        }.padding(.horizontal, 16).padding(.top, head ? 8 : 1).padding(.bottom, 1)
    }
}
