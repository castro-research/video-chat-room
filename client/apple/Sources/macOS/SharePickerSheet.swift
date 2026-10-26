import SwiftUI

/// Escolher o que partilhar (ecrã inteiro ou janela) e a qualidade.
struct SharePickerSheet: View {
    @EnvironmentObject var app: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var windows = false
    @State private var picked: String?

    var body: some View {
        let items = app.screenList.filter { $0.window == windows }
        VStack(alignment: .leading, spacing: 12) {
            Text("Partilhar ecrã").font(.system(size: 16, weight: .semibold)).foregroundColor(P.text)
            Picker("", selection: $windows) { Text("Ecrã inteiro").tag(false); Text("Aplicações").tag(true) }.pickerStyle(.segmented).labelsHidden()
            ScrollView {
                LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 10)], spacing: 10) {
                    ForEach(items) { s in
                        VStack(spacing: 4) {
                            ZStack {
                                P.input
                                if let img = app.screenThumbs[s.id] { Image(nsImage: img).resizable().scaledToFit() }
                            }.frame(height: 100).clipShape(RoundedRectangle(cornerRadius: 6))
                            Text(s.name).font(.system(size: 11.5)).foregroundColor(P.text).lineLimit(1)
                        }
                        .padding(6).overlay(RoundedRectangle(cornerRadius: 8).stroke(picked == s.id ? P.accent : P.border, lineWidth: picked == s.id ? 2 : 1))
                        .contentShape(Rectangle()).onTapGesture { picked = s.id }
                    }
                }
            }.frame(height: 260)
            HStack {
                Button("Pair coding") { app.setScreenQuality(.pairCoding) }.buttonStyle(.bordered)
                Button("Normal") { app.setScreenQuality(.normal) }.buttonStyle(.bordered)
                Spacer()
                Picker("", selection: Binding(get: { app.screenQuality.resolution }, set: { var q = app.screenQuality; q.resolution = $0; app.setScreenQuality(q) })) {
                    ForEach(ScreenResolution.allCases) { Text($0.label).tag($0) }
                }.labelsHidden().frame(width: 90)
                Picker("", selection: Binding(get: { app.screenQuality.fps }, set: { var q = app.screenQuality; q.fps = $0; app.setScreenQuality(q) })) {
                    ForEach(ScreenQuality.fpsOptions, id: \.self) { Text("\($0) fps").tag($0) }
                }.labelsHidden().frame(width: 90)
            }
            HStack {
                Spacer()
                Button("Cancelar") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Partilhar") {
                    if let s = app.screenList.first(where: { $0.id == picked }) { app.share(s); dismiss() }
                }.keyboardShortcut(.defaultAction).disabled(picked == nil)
            }
        }.padding(20).frame(width: 640).background(P.main)
        .onAppear { picked = picked ?? app.screenList.first { !$0.window }?.id }
        .onChange(of: app.screenList) { l in if picked == nil { picked = l.first { !$0.window }?.id } }
    }
}
