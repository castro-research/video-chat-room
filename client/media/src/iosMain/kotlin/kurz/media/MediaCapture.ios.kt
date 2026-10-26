package kurz.media

import WebRTC.RTCMTLVideoView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.UIKitView
import com.shepeliev.webrtckmp.AudioTrack
import com.shepeliev.webrtckmp.MediaDevices
import com.shepeliev.webrtckmp.MediaStream
import com.shepeliev.webrtckmp.VideoTrack
import kotlinx.cinterop.BetaInteropApi
import kotlinx.cinterop.ExperimentalForeignApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import platform.AVFAudio.AVAudioSession
import platform.AVFAudio.AVAudioSessionCategoryOptionDefaultToSpeaker
import platform.AVFAudio.AVAudioSessionCategoryPlayAndRecord
import platform.AVFAudio.AVAudioSessionModeDefault
import platform.AVFAudio.setActive
import platform.AVFoundation.AVAuthorizationStatusAuthorized
import platform.AVFoundation.AVAuthorizationStatusNotDetermined
import platform.AVFoundation.AVCaptureDevice
import platform.AVFoundation.AVMediaTypeAudio
import platform.AVFoundation.AVMediaTypeVideo
import platform.AVFoundation.authorizationStatusForMediaType
import platform.AVFoundation.requestAccessForMediaType
import platform.CoreFoundation.CFAbsoluteTimeGetCurrent
import platform.CoreMedia.CMSampleBufferGetImageBuffer
import platform.Foundation.NSProcessInfo
import platform.ReplayKit.RPSampleBufferTypeVideo
import platform.ReplayKit.RPScreenRecorder
import platform.UIKit.UIImage
import platform.UIKit.UIImageView
import platform.UIKit.UIViewContentMode
import platform.darwin.dispatch_async
import platform.darwin.dispatch_get_main_queue
import platform.CoreImage.CIImage
import platform.CoreImage.CIImage.Companion.imageWithCVPixelBuffer
import kotlin.coroutines.resume

@Composable
actual fun rememberMediaCapture(): MediaCapture {
    val c = remember { IosCapture() }
    DisposableEffect(c) { onDispose(c::release) }
    return c
}

@OptIn(ExperimentalForeignApi::class, BetaInteropApi::class)
internal class IosCapture : MediaCapture() {
    private var micStream: MediaStream? = null
    private var cameraStream: MediaStream? = null
    private var micTrack: AudioTrack? = null
    private var cameraTrack by mutableStateOf<VideoTrack?>(null)
    private var screenView: UIImageView? = null
    private var lastFrame = 0.0
    internal var onTrack: ((source: String, track: com.shepeliev.webrtckmp.MediaStreamTrack?) -> Unit)? = null
    internal val localTracks: Map<String, com.shepeliev.webrtckmp.MediaStreamTrack>
        get() = buildMap {
            micTrack?.let { put("mic", it) }
            cameraTrack?.let { put("camera", it) }
        }

    /** Nível de áudio (0..1, RMS) medido pelo WebRTC na sessão; o que tapava o AVAudioEngine competia com ele pelo microfone. */
    internal fun pushLevel(level: Double) = pushRms(level)

    private suspend fun authorized(type: String?): Boolean = suspendCancellableCoroutine { k ->
        when (AVCaptureDevice.authorizationStatusForMediaType(type)) {
            AVAuthorizationStatusAuthorized -> k.resume(true)
            AVAuthorizationStatusNotDetermined -> AVCaptureDevice.requestAccessForMediaType(type) { ok -> k.resume(ok) }
            else -> k.resume(false)
        }
    }

    private suspend fun prepareAudioSession(): Boolean = withContext(Dispatchers.Default) {
        val audio = AVAudioSession.sharedInstance()
        audio.setCategory(AVAudioSessionCategoryPlayAndRecord, AVAudioSessionModeDefault, AVAudioSessionCategoryOptionDefaultToSpeaker, null)
        audio.setActive(true, null)
    }

    override suspend fun startMic(): Boolean {
        if (mic != Capture.Off) return mic == Capture.On
        mic = Capture.Starting
        val env = NSProcessInfo.processInfo.environment
        if (env["SIMULATOR_DEVICE_NAME"] != null && env["KURZ_SIM_MIC"] == null) {
            mic = Capture.Off
            notice = Msg.NO_MIC_SIMULATOR
            return false
        }
        if (!authorized(AVMediaTypeAudio)) return failMic()
        try {
            prepareAudioSession()
            val stream = MediaDevices.getUserMedia(audio = true)
            val track = stream.tracks.filterIsInstance<AudioTrack>().firstOrNull() ?: run {
                stream.release()
                return failMic()
            }
            micStream = stream
            micTrack = track
            onTrack?.invoke("mic", track)
            mic = Capture.On
            return true
        } catch (e: CancellationException) {
            mic = Capture.Off
            throw e
        } catch (_: Exception) {
            return failMic()
        }
    }

    private fun failMic(): Boolean {
        mic = Capture.Off
        notice = Msg.NO_MIC
        return false
    }

    override fun stopMic() {
        onTrack?.invoke("mic", null)
        micTrack?.stop()
        micTrack = null
        micStream?.release()
        micStream = null
        mic = Capture.Off
        micLevel = 0f
    }

    override suspend fun startCamera(): Boolean {
        if (camera != Capture.Off) return camera == Capture.On
        camera = Capture.Starting
        if (!authorized(AVMediaTypeVideo)) {
            camera = Capture.Off
            notice = Msg.NO_CAMERA
            return false
        }
        return try {
            val stream = MediaDevices.getUserMedia(video = true)
            val track = stream.tracks.filterIsInstance<VideoTrack>().firstOrNull()
            if (track == null) {
                stream.release()
                camera = Capture.Off
                notice = Msg.NO_CAMERA_DEVICE
                false
            } else {
                cameraStream = stream
                cameraTrack = track
                onTrack?.invoke("camera", track)
                camera = Capture.On
                true
            }
        } catch (e: CancellationException) {
            camera = Capture.Off
            throw e
        } catch (_: Exception) {
            camera = Capture.Off
            notice = Msg.NO_CAMERA_DEVICE
            false
        }
    }

    override fun stopCamera() {
        onTrack?.invoke("camera", null)
        cameraTrack?.stop()
        cameraTrack = null
        cameraStream?.release()
        cameraStream = null
        camera = Capture.Off
    }

    @Composable
    override fun CameraPreview(modifier: Modifier) {
        cameraTrack?.let { IosVideoView(it, modifier) }
    }

    // ReplayKit previews the app only. Full-device broadcast requires a separate Broadcast Upload Extension.
    override suspend fun startScreen(): Boolean {
        if (screen != Capture.Off) return screen == Capture.On
        val recorder = RPScreenRecorder.sharedRecorder()
        if (!recorder.isAvailable()) { notice = Msg.NO_SCREEN; return false }
        screen = Capture.Starting
        val ok = suspendCancellableCoroutine { k ->
            recorder.startCaptureWithHandler({ sample, type, _ ->
                val buffer = if (type == RPSampleBufferTypeVideo && sample != null) CMSampleBufferGetImageBuffer(sample) else null
                val now = CFAbsoluteTimeGetCurrent()
                if (buffer != null && now - lastFrame >= 0.1) {
                    lastFrame = now
                    val image = UIImage.imageWithCIImage(CIImage.imageWithCVPixelBuffer(buffer))
                    dispatch_async(dispatch_get_main_queue()) { screenView?.image = image }
                }
            }, { error -> k.resume(error == null) })
        }
        if (!ok) { screen = Capture.Off; return false }
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

    override fun release() {
        onTrack = null
        stopMic()
        stopCamera()
        stopScreen()
    }
}

@OptIn(ExperimentalForeignApi::class)
@Composable
internal fun IosVideoView(track: VideoTrack, modifier: Modifier) {
    key(track) {  // faixa nova = vista nova (o renderer fica preso à faixa da factory)
        UIKitView(
            factory = {
                RTCMTLVideoView().apply {
                    videoContentMode = UIViewContentMode.UIViewContentModeScaleAspectFit
                    track.addRenderer(this)
                }
            },
            modifier = modifier,
            onRelease = { track.removeRenderer(it) },
        )
    }
}
