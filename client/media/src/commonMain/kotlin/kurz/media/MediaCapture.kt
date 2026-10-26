package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import kotlin.math.log10
import kotlin.math.max
import kotlin.math.sqrt

/** Estado de uma fonte. Se o pedido falhar volta a [Off] e a razão fica em [MediaCapture.notice]. */
enum class Capture { Off, Starting, On }

/** Um microfone ou uma câmara. [id] é o descritor do sistema; [name] o que se mostra. */
data class Device(val id: String, val name: String)

/**
 * Captura local de microfone, câmara e ecrã: permissões, arranque/paragem, pré-visualização e nível do microfone.
 * Não envia nada para lado nenhum — é o ponto de ligação ao LiveKit (que fará a captura por si, ver AppState.joinVoice).
 * Uma instância por janela: [rememberMediaCapture].
 */
abstract class MediaCapture {
    var mic by mutableStateOf(Capture.Off); protected set
    var camera by mutableStateOf(Capture.Off); protected set
    var screen by mutableStateOf(Capture.Off); protected set

    /** Volume do microfone, 0..1 (escala em dB, com decaimento suave). */
    var micLevel by mutableFloatStateOf(0f); protected set

    /** Última razão de falha, em português, para mostrar ao utilizador. */
    var notice by mutableStateOf<String?>(null)

    /** Chamado quando a partilha de ecrã termina por iniciativa do sistema (ex.: "Parar partilha" na barra de estado). */
    var onScreenStopped: (() -> Unit)? = null

    /** Dispositivos de entrada ([refreshDevices] actualiza). Vazios onde a plataforma não deixa escolher: a UI esconde o seletor. */
    var micDevices by mutableStateOf<List<Device>>(emptyList()); protected set
    var cameraDevices by mutableStateOf<List<Device>>(emptyList()); protected set

    /** Dispositivo escolhido; null = o padrão do sistema (na câmara, a primeira). */
    var micDeviceId by mutableStateOf<String?>(null); protected set
    var cameraDeviceId by mutableStateOf<String?>(null); protected set

    open fun refreshDevices() {}

    /** Muda de dispositivo; com a fonte ligada troca em quente, sem desligar o botão. */
    open fun selectMic(id: String?) {}
    open fun selectCamera(id: String) {}

    /** Cada start devolve true se a fonte ficou activa; pede a permissão se for preciso. */
    abstract suspend fun startMic(): Boolean
    abstract suspend fun startCamera(): Boolean
    abstract suspend fun startScreen(): Boolean
    abstract fun stopMic()
    abstract fun stopCamera()
    abstract fun stopScreen()

    /** Mostra a câmara/ecrã enquanto a respectiva fonte estiver [Capture.On]. */
    @Composable abstract fun CameraPreview(modifier: Modifier)
    @Composable abstract fun ScreenPreview(modifier: Modifier)

    /** Pára tudo e liberta recursos; a instância não volta a ser usada. */
    abstract fun release()

    /** Actualiza [micLevel] com o RMS (0..1 da escala total) de um bloco de áudio. */
    protected fun pushRms(rms: Double) {
        val db = 20 * log10(max(rms, 1e-6))
        micLevel = max(((db + 60) / 60).toFloat().coerceIn(0f, 1f), micLevel * 0.8f)
    }
}

@Composable
expect fun rememberMediaCapture(): MediaCapture

internal object Msg {
    const val NO_MIC = "Sem acesso ao microfone. Ativa-o nas definições do sistema."
    const val NO_MIC_SIMULATOR = "O microfone está desligado no simulador iOS (o áudio do simulador pode bloquear a app)."
    const val NO_MIC_SWITCH = "Não foi possível mudar de microfone."
    const val NO_CAMERA = "Sem acesso à câmara. Ativa-a nas definições do sistema."
    const val NO_CAMERA_DEVICE = "Não foi encontrada nenhuma câmara."
    const val NO_SCREEN = "Não foi possível partilhar o ecrã neste dispositivo."
}

/** RMS de [n] amostras PCM de 16 bits, em 0..1 da escala total. */
internal fun rms16(s: ShortArray, n: Int): Double {
    var sum = 0.0
    for (i in 0 until n) sum += s[i] * s[i].toDouble()
    return sqrt(sum / max(n, 1)) / 32768.0
}
