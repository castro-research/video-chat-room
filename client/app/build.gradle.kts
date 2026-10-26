import org.jetbrains.compose.desktop.application.dsl.TargetFormat

plugins {
    kotlin("multiplatform")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
}

kotlin {
    jvm("desktop")
    sourceSets {
        val desktopMain by getting {
            dependencies {
                implementation(project(":ui"))
                implementation(compose.desktop.currentOs)
            }
        }
    }
}

compose.desktop {
    application {
        mainClass = "MainKt"
        nativeDistributions {
            targetFormats(TargetFormat.Dmg, TargetFormat.Msi, TargetFormat.Deb)
            packageName = "Kurz Community"
            macOS {
                iconFile.set(project.file("icons/icon.icns"))
                bundleID = "app.kurz.client"
                // sem estas chaves o macOS nega câmara/microfone em silêncio à app empacotada
                infoPlist {
                    extraKeysRawXml = """
                        <key>NSCameraUsageDescription</key><string>Para chamadas de vídeo na sala.</string>
                        <key>NSMicrophoneUsageDescription</key><string>Para chamadas de voz na sala.</string>
                    """.trimIndent()
                }
            }
            linux { packageName = "kurz-community"; iconFile.set(project.file("icons/icon.png")) }  // nomes de .deb não têm espaços
        }
    }
}

val desktopMain = kotlin.targets.getByName("desktop").compilations.getByName("main")

// Renderiza um painel para PNG: ./gradlew :app:renderPane -Ppane=chat -Pdark=true -Pout=/tmp/chat.png [-Pscale=2]
tasks.register<JavaExec>("renderPane") {
    dependsOn("desktopMainClasses")
    classpath(desktopMain.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "PreviewKt"
    jvmArgs("-Djava.awt.headless=true")
    args(
        project.findProperty("pane") ?: "app", project.findProperty("dark") ?: "false",
        project.findProperty("w") ?: "1440", project.findProperty("h") ?: "900",
        project.findProperty("out") ?: "build/preview.png", project.findProperty("scale") ?: "1",
    )
}

// Abre microfone, câmara e ecrã reais, imprime o que apanhou e grava um frame de cada: ./gradlew :app:captureCheck [-Pout=/tmp/captura]
// No macOS a primeira execução pede permissões ao terminal (Microfone, Câmara, Gravação do ecrã).
tasks.register<JavaExec>("captureCheck") {
    dependsOn("desktopMainClasses")
    classpath(desktopMain.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "CaptureCheckKt"
    args(project.findProperty("out") ?: "build/capture")
}
