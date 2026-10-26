package kurz.media

import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.toComposeImageBitmap
import androidx.compose.ui.layout.ContentScale
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.awt.GraphicsEnvironment
import java.awt.RenderingHints
import java.awt.Robot
import java.awt.image.BufferedImage
import java.awt.image.DataBufferByte
import java.nio.ByteBuffer
import java.nio.ByteOrder
import javax.sound.sampled.AudioFormat
import javax.sound.sampled.AudioSystem
import org.opencv.core.Mat
import org.opencv.videoio.VideoCapture

@Composable
actual fun rememberMediaCapture(): MediaCapture {
    val c = remember { DesktopCapture() }
    DisposableEffect(c) { onDispose(c::release) }
    return c
}

// Sem API de permissões na JVM: no macOS a negação só se nota por silêncio (mic), imagem vazia (câmara) ou só o fundo do ecrã.
private class DesktopCapture : MediaCapture() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var cameraFrame by mutableStateOf<ImageBitmap?>(null)
    private var screenFrame by mutableStateOf<ImageBitmap?>(null)
    private var micJob: Job? = null
    private var cameraJob: Job? = null
    private var screenJob: Job? = null

    override suspend fun startMic(): Boolean {
        if (mic != Capture.Off) return mic == Capture.On
        mic = Capture.Starting
        val format = AudioFormat(16_000f, 16, 1, true, false)  // PCM com sinal, little-endian
        val line = withContext(Dispatchers.IO) {
            runCatching { AudioSystem.getTargetDataLine(format).also { it.open(format); it.start() } }.getOrNull()
        }
        if (line == null) { mic = Capture.Off; notice = Msg.NO_MIC; return false }
        micJob = scope.launch {
            val bytes = ByteArray(1_600)  // 50 ms
            val samples = ShortArray(bytes.size / 2)
            try {
                while (isActive) {
                    val n = line.read(bytes, 0, bytes.size) / 2
                    if (n <= 0) break
                    ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN).asShortBuffer().get(samples, 0, n)
                    pushRms(rms16(samples, n))
                }
            } finally {
                line.stop(); line.close()
            }
        }
        mic = Capture.On
        return true
    }

    override fun stopMic() { micJob?.cancel(); micJob = null; mic = Capture.Off; micLevel = 0f }

    override suspend fun startCamera(): Boolean {
        if (camera != Capture.Off) return camera == Capture.On
        camera = Capture.Starting
        val cap = withContext(Dispatchers.IO) {
            runCatching { nu.pattern.OpenCV.loadLocally(); VideoCapture(0).takeIf { it.isOpened } }.getOrNull()
        }
        if (cap == null) { camera = Capture.Off; notice = Msg.NO_CAMERA_DEVICE; return false }
        cameraJob = scope.launch {
            val mat = Mat()
            var failures = 0
            try {
                while (isActive) {
                    if (cap.read(mat) && !mat.empty()) { failures = 0; cameraFrame = mat.toImage() }
                    else if (++failures > 100) { camera = Capture.Off; notice = Msg.NO_CAMERA; break }  // ~3 s sem imagens
                    else delay(30)
                }
            } finally {
                mat.release(); cap.release()
            }
        }
        camera = Capture.On
        return true
    }

    override fun stopCamera() { cameraJob?.cancel(); cameraJob = null; camera = Capture.Off; cameraFrame = null }

    override suspend fun startScreen(): Boolean {
        if (screen != Capture.Off) return screen == Capture.On
        val robot = if (GraphicsEnvironment.isHeadless()) null else runCatching { Robot() }.getOrNull()
        if (robot == null) { notice = Msg.NO_SCREEN; return false }
        screen = Capture.Starting
        screenJob = scope.launch {
            val bounds = GraphicsEnvironment.getLocalGraphicsEnvironment().defaultScreenDevice.defaultConfiguration.bounds
            while (isActive) {
                screenFrame = robot.createScreenCapture(bounds).scaledToWidth(960).toComposeImageBitmap()
                delay(120)  // ~8 fps
            }
        }
        screen = Capture.On
        return true
    }

    override fun stopScreen() { screenJob?.cancel(); screenJob = null; screen = Capture.Off; screenFrame = null }

    @Composable
    override fun CameraPreview(modifier: Modifier) {
        // espelhada, como é habitual na pré-visualização própria
        cameraFrame?.let { Image(it, null, modifier.graphicsLayer { scaleX = -1f }, contentScale = ContentScale.Crop) }
    }

    @Composable
    override fun ScreenPreview(modifier: Modifier) {
        screenFrame?.let { Image(it, null, modifier, contentScale = ContentScale.Fit) }
    }

    override fun release() { stopMic(); stopCamera(); stopScreen(); scope.cancel() }
}

private fun Mat.toImage(): ImageBitmap {
    val img = BufferedImage(cols(), rows(), BufferedImage.TYPE_3BYTE_BGR)  // o OpenCV entrega BGR, 8 bits
    get(0, 0, (img.raster.dataBuffer as DataBufferByte).data)
    return img.toComposeImageBitmap()
}

private fun BufferedImage.scaledToWidth(w: Int): BufferedImage {
    if (width <= w) return this
    val out = BufferedImage(w, height * w / width, BufferedImage.TYPE_INT_RGB)
    out.createGraphics().apply {
        setRenderingHint(RenderingHints.KEY_INTERPOLATION, RenderingHints.VALUE_INTERPOLATION_BILINEAR)
        drawImage(this@scaledToWidth, 0, 0, out.width, out.height, null)
        dispose()
    }
    return out
}
