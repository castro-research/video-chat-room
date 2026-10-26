package kurz.media

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.ImageReader
import android.media.MediaRecorder
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import android.os.SystemClock
import androidx.activity.compose.ManagedActivityResultLauncher
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.ActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.camera.core.CameraSelector
import androidx.camera.core.Preview
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import androidx.compose.foundation.Image
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import androidx.lifecycle.LifecycleOwner
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.max
import kotlin.math.min

@Composable
actual fun rememberMediaCapture(): MediaCapture {
    val app = LocalContext.current.applicationContext
    val c = remember { AndroidCapture(app) }
    c.permLauncher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission(), c::onPermission)
    c.projLauncher = rememberLauncherForActivityResult(ActivityResultContracts.StartActivityForResult(), c::onProjection)
    DisposableEffect(c) { onDispose(c::release) }
    return c
}

private fun Context.lifecycleOwner(): LifecycleOwner? =
    this as? LifecycleOwner ?: (this as? ContextWrapper)?.baseContext?.lifecycleOwner()

private class AndroidCapture(private val app: Context) : MediaCapture() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val main = Handler(Looper.getMainLooper())

    // --- permissões e consentimento do ecrã: o resultado chega por launchers da Activity ---
    var permLauncher: ManagedActivityResultLauncher<String, Boolean>? = null
    var projLauncher: ManagedActivityResultLauncher<Intent, ActivityResult>? = null
    private var permWait: CompletableDeferred<Boolean>? = null
    private var projWait: CompletableDeferred<ActivityResult>? = null
    fun onPermission(ok: Boolean) { permWait?.complete(ok) }
    fun onProjection(r: ActivityResult) { projWait?.complete(r) }

    private suspend fun ask(permission: String): Boolean {
        if (ContextCompat.checkSelfPermission(app, permission) == PackageManager.PERMISSION_GRANTED) return true
        val launcher = permLauncher ?: return false
        return CompletableDeferred<Boolean>().also { permWait = it; launcher.launch(permission) }.await()
    }

    // --- microfone ---
    private var micJob: Job? = null

    @SuppressLint("MissingPermission")  // pedida em ask()
    override suspend fun startMic(): Boolean {
        if (mic != Capture.Off) return mic == Capture.On
        mic = Capture.Starting
        if (!ask(Manifest.permission.RECORD_AUDIO)) { mic = Capture.Off; notice = Msg.NO_MIC; return false }
        val rate = 16_000
        val minBuf = AudioRecord.getMinBufferSize(rate, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT)
        val rec = AudioRecord(
            MediaRecorder.AudioSource.VOICE_COMMUNICATION, rate, AudioFormat.CHANNEL_IN_MONO, AudioFormat.ENCODING_PCM_16BIT,
            max(minBuf, rate / 5 * 2),
        )
        if (rec.state != AudioRecord.STATE_INITIALIZED) { rec.release(); mic = Capture.Off; notice = Msg.NO_MIC; return false }
        rec.startRecording()
        micJob = scope.launch(Dispatchers.IO) {
            val buf = ShortArray(rate / 20)  // blocos de 50 ms
            try {
                while (isActive) {
                    val n = rec.read(buf, 0, buf.size)
                    if (n <= 0) break
                    pushRms(rms16(buf, n))
                }
            } finally {
                runCatching { rec.stop() }; rec.release()
            }
        }
        mic = Capture.On
        return true
    }

    override fun stopMic() { micJob?.cancel(); micJob = null; mic = Capture.Off; micLevel = 0f }

    // --- câmara: o CameraX só corre enquanto houver uma pré-visualização no ecrã ---
    override suspend fun startCamera(): Boolean {
        if (camera != Capture.Off) return camera == Capture.On
        camera = Capture.Starting
        if (!ask(Manifest.permission.CAMERA)) { camera = Capture.Off; notice = Msg.NO_CAMERA; return false }
        camera = Capture.On
        return true
    }

    override fun stopCamera() { camera = Capture.Off }

    @Composable
    override fun CameraPreview(modifier: Modifier) {
        val ctx = LocalContext.current
        val owner = ctx.lifecycleOwner()
        val view = remember { PreviewView(ctx).apply { scaleType = PreviewView.ScaleType.FILL_CENTER } }
        DisposableEffect(view, owner) {
            val future = ProcessCameraProvider.getInstance(ctx)
            val preview = Preview.Builder().build().also { it.surfaceProvider = view.surfaceProvider }
            var provider: ProcessCameraProvider? = null
            future.addListener({
                val p = future.get().also { provider = it }
                val selector = listOf(CameraSelector.DEFAULT_FRONT_CAMERA, CameraSelector.DEFAULT_BACK_CAMERA).firstOrNull { p.hasCamera(it) }
                when {
                    selector == null || owner == null -> { camera = Capture.Off; notice = Msg.NO_CAMERA_DEVICE }
                    else -> runCatching { p.bindToLifecycle(owner, selector, preview) }.onFailure { camera = Capture.Off; notice = Msg.NO_CAMERA_DEVICE }
                }
            }, ContextCompat.getMainExecutor(ctx))
            onDispose { provider?.unbind(preview) }
        }
        AndroidView({ view }, modifier)
    }

    // --- ecrã: MediaProjection → VirtualDisplay → ImageReader → bitmap de pré-visualização ---
    private var projection: MediaProjection? = null
    private var display: VirtualDisplay? = null
    private var reader: ImageReader? = null
    private var frameThread: HandlerThread? = null
    private var screenFrame by mutableStateOf<ImageBitmap?>(null)

    override suspend fun startScreen(): Boolean {
        if (screen != Capture.Off) return screen == Capture.On
        screen = Capture.Starting
        val mpm = app.getSystemService(MediaProjectionManager::class.java)
        val launcher = projLauncher
        val consent = if (launcher == null) null
        else CompletableDeferred<ActivityResult>().also { projWait = it; launcher.launch(mpm.createScreenCaptureIntent()) }.await()
        val data = consent?.data
        if (consent == null || consent.resultCode != Activity.RESULT_OK || data == null) { screen = Capture.Off; return false }  // cancelado: sem aviso
        return try {
            ScreenCaptureService.start(app)
            val p = mpm.getMediaProjection(consent.resultCode, data) ?: error("sem MediaProjection")
            projection = p
            // obrigatório no Android 14+ antes de criar o VirtualDisplay
            p.registerCallback(object : MediaProjection.Callback() {
                override fun onStop() { if (projection != null) { teardown(); onScreenStopped?.invoke() } }
            }, main)
            val thread = HandlerThread("kurz-screen").also { it.start(); frameThread = it }
            val dm = app.resources.displayMetrics
            val k = min(1f, 720f / max(dm.widthPixels, dm.heightPixels))
            val w = (dm.widthPixels * k).toInt() and 1.inv()
            val h = (dm.heightPixels * k).toInt() and 1.inv()
            val r = ImageReader.newInstance(w, h, PixelFormat.RGBA_8888, 2).also { reader = it }
            var last = 0L
            r.setOnImageAvailableListener({
                it.acquireLatestImage()?.use { img ->
                    val now = SystemClock.elapsedRealtime()
                    if (now - last < 100) return@use  // pré-visualização a ~10 fps
                    last = now
                    val plane = img.planes[0]
                    val stride = plane.rowStride / plane.pixelStride
                    val bmp = Bitmap.createBitmap(stride, img.height, Bitmap.Config.ARGB_8888)
                    bmp.copyPixelsFromBuffer(plane.buffer)
                    screenFrame = (if (stride == img.width) bmp else Bitmap.createBitmap(bmp, 0, 0, img.width, img.height)).asImageBitmap()
                }
            }, Handler(thread.looper))
            display = p.createVirtualDisplay("kurz-screen", w, h, dm.densityDpi, DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, r.surface, null, null)
            screen = Capture.On
            true
        } catch (e: Exception) {
            teardown(); notice = Msg.NO_SCREEN; false
        }
    }

    override fun stopScreen() = teardown()

    private fun teardown() {
        val p = projection
        projection = null  // o onStop() de p.stop() já não repete o aviso
        display?.release(); display = null
        reader?.close(); reader = null
        frameThread?.quitSafely(); frameThread = null
        runCatching { p?.stop() }
        ScreenCaptureService.stop(app)
        screenFrame = null
        screen = Capture.Off
    }

    @Composable
    override fun ScreenPreview(modifier: Modifier) {
        screenFrame?.let { Image(it, null, modifier, contentScale = ContentScale.Fit) }
    }

    override fun release() { stopMic(); stopCamera(); teardown(); scope.cancel() }
}
