import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("multiplatform")
    id("com.android.library")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
}

kotlin {
    jvm("desktop")
    androidTarget { compilerOptions { jvmTarget.set(JvmTarget.JVM_17) } }
    iosArm64()
    iosSimulatorArm64()
    sourceSets {
        commonMain.dependencies {
            implementation(compose.foundation)
            implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.10.2")
            api("org.jetbrains.kotlinx:kotlinx-serialization-json:1.9.0")
        }
        androidMain.dependencies {
            implementation("androidx.activity:activity-compose:1.12.2")
            val camerax = "1.5.1"
            implementation("androidx.camera:camera-core:$camerax")
            implementation("androidx.camera:camera-camera2:$camerax")
            implementation("androidx.camera:camera-lifecycle:$camerax")
            implementation("androidx.camera:camera-view:$camerax")
        }
        // webrtc-java (JNI sobre libwebrtc): captura de mic/câmara/ecrã, PeerConnection e frames recebidos.
        // O POM escolhe o nativo por perfil de SO, que o Gradle ignora: os classifiers vão explícitos.
        val desktopMain by getting {
            dependencies {
                val webrtc = "0.20.0"
                api("dev.onvoid.webrtc:webrtc-java:$webrtc")
                listOf("macos-aarch64", "macos-x86_64", "linux-x86_64", "windows-x86_64")
                    .forEach { runtimeOnly("dev.onvoid.webrtc:webrtc-java:$webrtc:$it") }
            }
        }
    }
}

android {
    namespace = "kurz.media"
    compileSdk = 36
    defaultConfig { minSdk = 24 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
