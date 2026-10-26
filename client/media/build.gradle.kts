import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("multiplatform")
    id("com.android.library")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
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
        }
        androidMain.dependencies {
            implementation("androidx.activity:activity-compose:1.12.2")
            val camerax = "1.5.1"
            implementation("androidx.camera:camera-core:$camerax")
            implementation("androidx.camera:camera-camera2:$camerax")
            implementation("androidx.camera:camera-lifecycle:$camerax")
            implementation("androidx.camera:camera-view:$camerax")
        }
        // OpenCV empacotado com as bibliotecas nativas de macOS/Linux/Windows (câmara web no desktop)
        val desktopMain by getting { dependencies { implementation("org.openpnp:opencv:4.9.0-0") } }
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
