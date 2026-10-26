import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.painter.BitmapPainter
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.res.loadImageBitmap
import androidx.compose.ui.res.useResource
import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import androidx.compose.ui.window.rememberWindowState
import java.awt.Taskbar
import kurz.ui.App

fun main() {
    // nome na barra de menus do macOS quando corre sem empacotar (tem de ser definido antes de o AWT arrancar)
    System.setProperty("apple.awt.application.name", "Kurz Community")
    window()
}

private fun window() = application {
    val icon = remember { useResource("icon.png", ::loadImageBitmap) }
    // Dock do macOS em desenvolvimento (empacotado vem do .icns); sem suporte noutros sistemas
    remember { runCatching { Taskbar.getTaskbar().iconImage = icon.toAwtImage() } }
    Window(
        onCloseRequest = ::exitApplication,
        title = "Kurz Community",
        icon = BitmapPainter(icon),
        state = rememberWindowState(size = DpSize(1440.dp, 900.dp)),
    ) { App() }
}
