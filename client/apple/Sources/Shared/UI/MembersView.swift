import KurzKit
import SwiftUI

/// Os membros da comunidade aberta (painel à direita no Mac, folha no iPhone).
struct MembersView: View {
    @EnvironmentObject var wm: WorkspaceModel

    var body: some View {
        let members = wm.members
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 2) {
                Text("Membros · \(members.count)").font(.system(size: 12, weight: .semibold)).foregroundColor(P.textMuted).padding(.horizontal, 8).padding(.vertical, 8)
                ForEach(members) { m in
                    HStack(spacing: 10) {
                        Avatar(name: m.user.name, hue: m.user.avatarHue, size: 30)
                            .overlay(alignment: .bottomTrailing) {
                                Circle().fill(m.user.status == .online ? P.online : m.user.status == .idle ? P.idle : P.textFaint)
                                    .frame(width: 9, height: 9).overlay(Circle().stroke(P.main, lineWidth: 2))
                            }
                        VStack(alignment: .leading, spacing: 0) {
                            HStack(spacing: 4) {
                                Text(m.user.name).font(.system(size: 13, weight: .medium)).foregroundColor(P.text).lineLimit(1)
                                if m.id == wm.selectedGuild?.ownerId { Image(systemName: "crown.fill").font(.system(size: 9)).foregroundColor(P.idle) }
                            }
                            if let sub = m.user.subtitle, !sub.isEmpty { Text(sub).font(.system(size: 11)).foregroundColor(P.textFaint).lineLimit(1) }
                        }
                        Spacer()
                    }.padding(.horizontal, 8).frame(height: 40)
                }
            }.padding(.horizontal, 8)
        }
        .background(P.main)
        .task(id: wm.selectedGuildId) { if let g = wm.selectedGuildId { await wm.loadMembers(g) } }
    }
}
