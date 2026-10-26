import CoreMedia
import Foundation
import ScreenCaptureKit

// Áudio do sistema para o stdout, para a partilha de ecrã: PCM s16le, 48 kHz, estéreo.
// Uso: kurz-audio display|window <id> <pid da app a excluir>
// Com `display` apanha tudo menos o áudio da app <pid> (senão as vozes da sala voltavam para a sala); com `window`, o da app dessa janela.
// Sai com 2 e uma razão no stderr se não conseguir (sem permissão de gravação do ecrã, macOS < 13).
// Se o pai morre o write falha com SIGPIPE e o helper cai sozinho.

func die(_ msg: String) -> Never {
    FileHandle.standardError.write(Data((msg + "\n").utf8))
    exit(2)
}

final class Out: NSObject, SCStreamOutput, SCStreamDelegate {
    func stream(_ stream: SCStream, didOutputSampleBuffer sb: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .audio, sb.isValid else { return }
        let n = CMSampleBufferGetNumSamples(sb)
        guard n > 0 else { return }
        var pcm = [Int16](repeating: 0, count: n * 2)
        try? sb.withAudioBufferList { list, _ in
            // float32; planar (um buffer por canal) ou entrelaçado (um buffer com N canais)
            let planar = list.count > 1
            for c in 0..<2 {
                let b = list[planar ? min(c, list.count - 1) : 0]
                guard let raw = b.mData?.assumingMemoryBound(to: Float.self) else { continue }
                let ch = planar ? 1 : max(Int(b.mNumberChannels), 1)
                let off = planar ? 0 : min(c, ch - 1)
                for i in 0..<min(n, Int(b.mDataByteSize) / (4 * ch)) {
                    pcm[i * 2 + c] = Int16(max(-1, min(1, raw[i * ch + off])) * 32767)
                }
            }
        }
        pcm.withUnsafeBytes { FileHandle.standardOutput.write(Data($0)) }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) { die("A captura parou: \(error.localizedDescription)") }
}

let args = CommandLine.arguments
guard args.count == 4, let id = UInt32(args[2]), let pid = Int32(args[3]) else { die("uso: kurz-audio display|window <id> <pid>") }

let out = Out()
Task {
    do {
        let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: false)
        let filter: SCContentFilter
        if args[1] == "window", let w = content.windows.first(where: { $0.windowID == id }) {
            filter = SCContentFilter(desktopIndependentWindow: w)
        } else {
            guard let d = content.displays.first(where: { $0.displayID == id }) ?? content.displays.first else { die("Sem ecrãs.") }
            filter = SCContentFilter(display: d, excludingApplications: content.applications.filter { $0.processID == pid }, exceptingWindows: [])
        }
        let cfg = SCStreamConfiguration()
        cfg.capturesAudio = true
        cfg.sampleRate = 48000
        cfg.channelCount = 2
        cfg.width = 2; cfg.height = 2                                  // o vídeo não interessa: o mínimo, a 1 fps
        cfg.minimumFrameInterval = CMTime(value: 1, timescale: 1)
        let q = DispatchQueue(label: "kurz.audio")
        let stream = SCStream(filter: filter, configuration: cfg, delegate: out)
        try stream.addStreamOutput(out, type: .audio, sampleHandlerQueue: q)
        try stream.addStreamOutput(out, type: .screen, sampleHandlerQueue: q)
        try await stream.startCapture()
    } catch {
        die("Sem acesso à gravação do ecrã ou do áudio: \(error.localizedDescription)")
    }
}
dispatchMain()
