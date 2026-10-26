package kurz.media

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import kotlin.math.log10
import kotlin.math.max
import kotlin.math.sqrt

/** Estado de uma fonte. Se o pedido falhar volta a [Off] e a razão fica em [MediaCapture.notice]. */
enum class Capture { Off, Starting, On }

/** Um microfone ou uma câmara. [id] é o descritor do sistema; [name] o que se mostra. */
data class Device(val id: String, val name: String)

/** Uma coisa que se pode partilhar: um ecrã inteiro ou uma janela. [id] é do sistema; [name] o que se mostra. */
data class ScreenSource(val id: String, val name: String, val window: Boolean)

/** Resolução da partilha de ecrã: altura máxima em px; [Source] não reduz (a da própria fonte). */
enum class ScreenResolution(val label: String, val height: Int) { P720("720p", 720), P1080("1080p", 1080), P1440("1440p", 1440), Source("Fonte", 0) }

/** Qualidade da partilha de ecrã: resolução e quadros por segundo. */
data class ScreenQuality(val resolution: ScreenResolution, val fps: Int) {
    val label get() = "${resolution.label} · ${fps}fps"

    companion object {
        val fpsOptions = listOf(5, 15, 30, 60)
        /** Os dois atalhos, com o recomendado: código (texto nítido, pouco movimento) e partilha normal. */
        val PairCoding = ScreenQuality(ScreenResolution.P1440, 15)
        val Normal = ScreenQuality(ScreenResolution.P1080, 30)
    }
}

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

    /** A plataforma deixa escolher o que partilhar (desktop). Onde o sistema pergunta sozinho (Android/iOS) a UI liga a partilha logo. */
    open val canPickScreen get() = false
    /** O que se pode partilhar ([refreshScreenSources] actualiza, só quando se vai escolher: no macOS listar janelas pede a permissão de gravação do ecrã). */
    var screenSources by mutableStateOf<List<ScreenSource>>(emptyList()); protected set
    /** Escolhido; null = o primeiro ecrã. */
    var screenSourceId by mutableStateOf<String?>(null); protected set
    var screenQuality by mutableStateOf(ScreenQuality.Normal); protected set
    /** Miniatura de cada [screenSources] por id; chegam uma a uma depois de [refreshScreenSources] (as que o sistema não dá ficam de fora). */
    var screenThumbs by mutableStateOf<Map<String, ImageBitmap>>(emptyMap()); protected set

    open fun refreshDevices() {}
    open fun refreshScreenSources() {}
    /** Escolhe o que partilhar; com a partilha ligada troca em quente. */
    open fun selectScreen(id: String) {}
    /** Muda a qualidade da partilha; com ela ligada reabre a fonte em quente. */
    open fun selectScreenQuality(q: ScreenQuality) {}

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
