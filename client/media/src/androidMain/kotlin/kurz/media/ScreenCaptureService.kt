package kurz.media

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeout

/** Serviço em primeiro plano que o Android exige enquanto o MediaProjection está activo. Não faz mais nada. */
class ScreenCaptureService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (Build.VERSION.SDK_INT >= 26) {
            getSystemService(NotificationManager::class.java)
                .createNotificationChannel(NotificationChannel(CHANNEL, "Partilha de ecrã", NotificationManager.IMPORTANCE_LOW))
        }
        val n: Notification = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_menu_share).setContentTitle("A partilhar o ecrã").setOngoing(true).build()
        if (Build.VERSION.SDK_INT >= 29) startForeground(ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION) else startForeground(ID, n)
        ready?.complete(Unit)
        return START_NOT_STICKY
    }

    companion object {
        private const val CHANNEL = "screen-share"
        private const val ID = 1
        @Volatile private var ready: CompletableDeferred<Unit>? = null

        /** Arranca o serviço e espera que esteja em primeiro plano (o MediaProjection só pode ser obtido depois). */
        suspend fun start(ctx: Context) {
            val d = CompletableDeferred<Unit>().also { ready = it }
            ContextCompat.startForegroundService(ctx, Intent(ctx, ScreenCaptureService::class.java))
            withTimeout(5_000) { d.await() }
        }

        fun stop(ctx: Context) { ctx.stopService(Intent(ctx, ScreenCaptureService::class.java)) }
    }
}
