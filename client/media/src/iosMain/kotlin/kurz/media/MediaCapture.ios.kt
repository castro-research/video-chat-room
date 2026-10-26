package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.UIKitView
import kotlinx.cinterop.BetaInteropApi
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.cinterop.get
import kotlinx.cinterop.readValue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import platform.AVFAudio.AVAudioEngine
import platform.AVFAudio.AVAudioSession
import platform.AVFAudio.AVAudioSessionCategoryOptionDefaultToSpeaker
import platform.AVFAudio.AVAudioSessionCategoryPlayAndRecord
import platform.AVFAudio.AVAudioSessionModeDefault
import platform.AVFAudio.setActive
import platform.AVFoundation.AVAuthorizationStatusAuthorized
import platform.AVFoundation.AVAuthorizationStatusNotDetermined
import platform.AVFoundation.AVCaptureDevice
import platform.AVFoundation.AVCaptureDeviceInput
import platform.AVFoundation.AVCaptureDevicePositionFront
import platform.AVFoundation.AVCaptureDeviceTypeBuiltInWideAngleCamera
import platform.AVFoundation.AVCaptureSession
import platform.AVFoundation.AVCaptureVideoPreviewLayer
import platform.AVFoundation.AVLayerVideoGravityResizeAspectFill
import platform.AVFoundation.AVMediaTypeAudio
import platform.AVFoundation.AVMediaTypeVideo
import platform.AVFoundation.authorizationStatusForMediaType
import platform.AVFoundation.defaultDeviceWithDeviceType
import platform.AVFoundation.requestAccessForMediaType
import platform.CoreFoundation.CFAbsoluteTimeGetCurrent
import platform.CoreGraphics.CGRectZero
import platform.CoreImage.CIImage
import platform.CoreImage.CIImage.Companion.imageWithCVPixelBuffer
import platform.CoreMedia.CMSampleBufferGetImageBuffer
import platform.Foundation.NSProcessInfo
import platform.QuartzCore.CALayer
import platform.QuartzCore.CATransaction
import platform.ReplayKit.RPSampleBufferTypeVideo
import platform.ReplayKit.RPScreenRecorder
import platform.UIKit.UIImage
import platform.UIKit.UIImageView
import platform.UIKit.UIView
import platform.UIKit.UIViewContentMode
import platform.darwin.DISPATCH_QUEUE_PRIORITY_DEFAULT
import platform.darwin.dispatch_async
import platform.darwin.dispatch_get_global_queue
import platform.darwin.dispatch_get_main_queue
import kotlin.coroutines.resume
import kotlin.math.max
import kotlin.math.sqrt

@Composable
actual fun rememberMediaCapture(): MediaCapture {
    val c = remember { IosCapture() }
    DisposableEffect(c) { onDispose(c::release) }
    return c
}

/** UIView que mantém [inner] do tamanho da própria vista (as sublayers não se redimensionam sozinhas). */
@OptIn(BetaInteropApi::class, ExperimentalForeignApi::class)
private class LayerHost(private val inner: CALayer) : UIView(frame = CGRectZero.readValue()) {
    init { layer.addSublayer(inner) }

    override fun layoutSubviews() {
        super.layoutSubviews()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        inner.frame = bounds
        CATransaction.commit()
    }
}

@OptIn(ExperimentalForeignApi::class)
private class IosCapture : MediaCapture() {
    private var engine: AVAudioEngine? = null
    private var session: AVCaptureSession? = null
    private var screenView: UIImageView? = null
    private var lastFrame = 0.0

    private suspend fun authorized(type: String?): Boolean = suspendCancellableCoroutine { k ->
        when (AVCaptureDevice.authorizationStatusForMediaType(type)) {
            AVAuthorizationStatusAuthorized -> k.resume(true)
            AVAuthorizationStatusNotDetermined -> AVCaptureDevice.requestAccessForMediaType(type) { ok -> k.resume(ok) }
            else -> k.resume(false)
        }
    }

    // --- microfone: tap no AVAudioEngine só para medir o volume ---
    override suspend fun startMic(): Boolean {
        if (mic != Capture.Off) return mic == Capture.On
        mic = Capture.Starting
        // No simulador o áudio passa pelo coreaudiod do Mac e pode bloquear até o CoreAudio abortar a app; por omissão fica desligado.
        val env = NSProcessInfo.processInfo.environment
        if (env["SIMULATOR_DEVICE_NAME"] != null && env["KURZ_SIM_MIC"] == null) { mic = Capture.Off; notice = Msg.NO_MIC_SIMULATOR; return false }
        if (!authorized(AVMediaTypeAudio)) return failMic()
        // a primeira utilização do áudio demora: fora da thread principal para não congelar a interface
        val e = withContext(Dispatchers.Default) {
            val audio = AVAudioSession.sharedInstance()
            audio.setCategory(AVAudioSessionCategoryPlayAndRecord, AVAudioSessionModeDefault, AVAudioSessionCategoryOptionDefaultToSpeaker, null)
            audio.setActive(true, null)
            val engine = AVAudioEngine()
            val input = engine.inputNode
            val format = input.outputFormatForBus(0u)
            if (format.sampleRate <= 0.0 || format.channelCount == 0u) return@withContext null  // sem entrada de áudio
            input.installTapOnBus(0u, 1024u, format) { buffer, _ ->
                val n = buffer?.frameLength?.toInt() ?: 0
                val samples = buffer?.floatChannelData?.get(0)
                if (samples != null && n > 0) {
                    var sum = 0.0
                    for (i in 0 until n) sum += samples[i] * samples[i].toDouble()
                    pushRms(sqrt(sum / max(n, 1)))
                }
            }
            engine.prepare()
            if (engine.startAndReturnError(null)) engine else { input.removeTapOnBus(0u); null }
        } ?: return failMic()
        engine = e
        mic = Capture.On
        return true
    }

    private fun failMic(): Boolean { mic = Capture.Off; notice = Msg.NO_MIC; return false }

    override fun stopMic() {
        engine?.let { it.inputNode.removeTapOnBus(0u); it.stop() }
        engine = null; mic = Capture.Off; micLevel = 0f
    }

    // --- câmara: AVCaptureSession mostrada por uma AVCaptureVideoPreviewLayer ---
    override suspend fun startCamera(): Boolean {
        if (camera != Capture.Off) return camera == Capture.On
        camera = Capture.Starting
        if (!authorized(AVMediaTypeVideo)) { camera = Capture.Off; notice = Msg.NO_CAMERA; return false }
        val device = AVCaptureDevice.defaultDeviceWithDeviceType(AVCaptureDeviceTypeBuiltInWideAngleCamera, AVMediaTypeVideo, AVCaptureDevicePositionFront)
            ?: AVCaptureDevice.defaultDeviceWithMediaType(AVMediaTypeVideo)
        val input = device?.let { AVCaptureDeviceInput.deviceInputWithDevice(it, null) }
        val s = AVCaptureSession()
        if (input == null || !s.canAddInput(input)) { camera = Capture.Off; notice = Msg.NO_CAMERA_DEVICE; return false }  // o simulador não tem câmara
        s.addInput(input)
        session = s
        dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT.toLong(), 0u)) { s.startRunning() }  // bloqueia: fora da main
        camera = Capture.On
        return true
    }

    override fun stopCamera() {
        session?.let { s -> dispatch_async(dispatch_get_global_queue(DISPATCH_QUEUE_PRIORITY_DEFAULT.toLong(), 0u)) { s.stopRunning() } }
        session = null; camera = Capture.Off
    }

    @Composable
    override fun CameraPreview(modifier: Modifier) {
        val s = session ?: return
        val layer = remember(s) { AVCaptureVideoPreviewLayer.layerWithSession(s).also { it.videoGravity = AVLayerVideoGravityResizeAspectFill } }
        UIKitView(factory = { LayerHost(layer) }, modifier = modifier)
    }

    // --- ecrã: ReplayKit só apanha a própria app; o ecrã inteiro do sistema exigiria uma Broadcast Upload Extension ---
    // O simulador arranca o ReplayKit mas nunca entrega frames: a pré-visualização só se vê num dispositivo real.
    override suspend fun startScreen(): Boolean {
        if (screen != Capture.Off) return screen == Capture.On
        val recorder = RPScreenRecorder.sharedRecorder()
        if (!recorder.isAvailable()) { notice = Msg.NO_SCREEN; return false }
        screen = Capture.Starting
        val ok = suspendCancellableCoroutine { k ->
            recorder.startCaptureWithHandler({ sample, type, _ ->
                val buffer = if (type == RPSampleBufferTypeVideo && sample != null) CMSampleBufferGetImageBuffer(sample) else null
                val now = CFAbsoluteTimeGetCurrent()
                if (buffer != null && now - lastFrame >= 0.1) {  // pré-visualização a ~10 fps
                    lastFrame = now
                    val image = UIImage.imageWithCIImage(CIImage.imageWithCVPixelBuffer(buffer))
                    dispatch_async(dispatch_get_main_queue()) { screenView?.image = image }
                }
            }, { error -> k.resume(error == null) })
        }
        if (!ok) { screen = Capture.Off; return false }  // recusado no pedido do sistema: sem aviso
        screen = Capture.On
        return true
    }

    override fun stopScreen() {
        if (screen != Capture.Off) RPScreenRecorder.sharedRecorder().stopCaptureWithHandler(null)
        screen = Capture.Off
    }

    @Composable
    override fun ScreenPreview(modifier: Modifier) {
        UIKitView(
            factory = { UIImageView().also { it.contentMode = UIViewContentMode.UIViewContentModeScaleAspectFit; screenView = it } },
            modifier = modifier,
            onRelease = { if (screenView === it) screenView = null },
        )
    }

    override fun release() { stopMic(); stopCamera(); stopScreen() }
}
