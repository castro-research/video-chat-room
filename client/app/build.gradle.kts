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
            packageName = "kurz-community"
        }
    }
}

// Renderiza um painel para PNG: ./gradlew :app:renderPane -Ppane=chat -Pdark=true -Pout=/tmp/chat.png [-Pscale=2]
tasks.register<JavaExec>("renderPane") {
    val main = kotlin.targets.getByName("desktop").compilations.getByName("main")
    dependsOn("desktopMainClasses")
    classpath(main.output.allOutputs, configurations.getByName("desktopRuntimeClasspath"))
    mainClass = "PreviewKt"
    jvmArgs("-Djava.awt.headless=true")
    args(
        project.findProperty("pane") ?: "app", project.findProperty("dark") ?: "false",
        project.findProperty("w") ?: "1440", project.findProperty("h") ?: "900",
        project.findProperty("out") ?: "build/preview.png", project.findProperty("scale") ?: "1",
    )
}
