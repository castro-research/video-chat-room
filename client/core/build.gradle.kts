import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("multiplatform")
    id("com.android.library")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
}

// Só modelo e estado (compose.runtime para o estado observável); sem foundation/material.
kotlin {
    jvm("desktop")
    androidTarget { compilerOptions { jvmTarget.set(JvmTarget.JVM_17) } }
    iosArm64()
    iosSimulatorArm64()
    sourceSets {
        commonMain.dependencies { api(compose.runtime) }
        commonTest.dependencies { implementation(kotlin("test")) }
    }
}

// Servidor das salas que vem de fábrica: o de produção nas versões release, o local no desenvolvimento.
// release = tarefa com "release" no nome (runRelease, packageReleaseDmg, assembleRelease), CONFIGURATION=Release do Xcode ou -Pkurz.release=true.
val release = providers.gradleProperty("kurz.release").map(String::toBoolean).getOrElse(
    gradle.startParameter.taskNames.any { "release" in it.lowercase() } || System.getenv("CONFIGURATION") == "Release",
)
val genBuildInfo = tasks.register("genBuildInfo") {
    val server = if (release) "https://cord.kurz.fyi" else "http://localhost:30080"
    val out = layout.buildDirectory.dir("generated/buildinfo")
    inputs.property("server", server)
    outputs.dir(out)
    doLast {
        out.get().file("kurz/core/BuildInfo.kt").asFile.apply {
            parentFile.mkdirs()
            writeText("package kurz.core\n\n// Gerado por :core:genBuildInfo (core/build.gradle.kts).\nobject BuildInfo { const val defaultServer = \"$server\" }\n")
        }
    }
}
kotlin.sourceSets.commonMain { kotlin.srcDir(genBuildInfo) }

android {
    namespace = "kurz.core"
    compileSdk = 36
    defaultConfig { minSdk = 24 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}
