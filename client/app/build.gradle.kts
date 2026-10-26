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
        jvmArgs += "--enable-native-access=ALL-UNNAMED"  // webrtc-java carrega o libwebrtc por JNI
        // ponytail: release sem ProGuard. O libwebrtc chama o Java por nome de classe/método (JNI) e o shrink tirava 147 das 209
        // classes do webrtc-java; voltar a ligá-lo pede regras -keep de dev.onvoid.webrtc.** e uma prova de que a mídia funciona.
        buildTypes.release.proguard { isEnabled.set(false) }
        nativeDistributions {
            targetFormats(TargetFormat.Dmg, TargetFormat.Msi, TargetFormat.Deb)
            packageName = "Kurz Community"
            // o runtime empacotado é uma JVM reduzida: sem java.net.http o HttpClient das salas não existe (resultado de :app:suggestRuntimeModules)
            modules("java.instrument", "java.net.http", "java.prefs", "jdk.unsupported")
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

// Troca de microfone e câmara com tudo ligado (sem ecrã): ./gradlew :app:deviceCheck
tasks.register<JavaExec>("deviceCheck") {
    dependsOn("desktopMainClasses")
    classpath(desktopMain.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "DeviceCheckKt"
}

// Abre microfone, câmara e ecrã reais, imprime o que apanhou e grava um frame de cada: ./gradlew :app:captureCheck [-Pout=/tmp/captura]
// No macOS a primeira execução pede permissões ao terminal (Microfone, Câmara, Gravação do ecrã).
tasks.register<JavaExec>("captureCheck") {
    dependsOn("desktopMainClasses")
    classpath(desktopMain.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "CaptureCheckKt"
    jvmArgs("--enable-native-access=ALL-UNNAMED")
    args(project.findProperty("out") ?: "build/capture")
}

// Sala real ponta a ponta, sem janela nem permissões (vídeo/áudio sintéticos): ./gradlew :app:voiceCheck [-Pmanager=http://localhost:30080]
// Precisa do manager e do SFU de experimentos/ a correr.
tasks.register<JavaExec>("voiceCheck") {
    dependsOn("desktopMainClasses")
    classpath(desktopMain.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "VoiceCheckKt"
    jvmArgs("-Djava.awt.headless=true", "--enable-native-access=ALL-UNNAMED")
    args(project.findProperty("manager") ?: "http://localhost:30080")
}

// O signing{} do plugin só aceita certificados "Developer ID"/"Mac Developer" (procura por esse prefixo), por isso assinamos nós,
// com identidade fixa: com a assinatura ad-hoc do jpackage o macOS repete o pedido de câmara/mic/ecrã a cada build.
// O packageDmg refaz o .app do zero, então assinamos o .app dentro do próprio .dmg (converte para RW, assina, volta a comprimir).
// ponytail: Apple Development só serve a esta máquina; distribuir pede Developer ID + notarização
tasks.matching { it.name == "packageDmg" }.configureEach {
    doLast {
        if (!System.getProperty("os.name").startsWith("Mac")) return@doLast
        fun sh(vararg c: String) = check(ProcessBuilder(*c).inheritIO().start().waitFor() == 0) { "falhou: ${c.joinToString(" ")}" }
        val dmg = layout.buildDirectory.dir("compose/binaries").get().asFile.walk().first { it.extension == "dmg" }
        val tmp = temporaryDir.resolve("rw.dmg").also { it.delete() }
        val mnt = temporaryDir.resolve("mnt").also { it.mkdirs() }
        sh("hdiutil", "convert", dmg.path, "-format", "UDRW", "-o", tmp.path, "-quiet")
        sh("hdiutil", "attach", tmp.path, "-mountpoint", mnt.path, "-nobrowse", "-quiet")
        try {
            sh("codesign", "--force", "--deep", "--sign", "Apple Development: Alexandro Castro (25NPKG5RNX)", mnt.resolve("Kurz Community.app").path)
        } finally {
            sh("hdiutil", "detach", mnt.path, "-quiet")
        }
        dmg.delete()
        sh("hdiutil", "convert", tmp.path, "-format", "UDZO", "-o", dmg.path, "-quiet")
        tmp.delete()
    }
}
