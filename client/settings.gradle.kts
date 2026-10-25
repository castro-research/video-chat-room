rootProject.name = "client"

pluginManagement {
    repositories { gradlePluginPortal(); google(); mavenCentral() }
}
dependencyResolutionManagement {
    repositories { google(); mavenCentral() }
}

// core: estado e modelo (sem UI) · ui: ecrãs Compose · app: janela desktop · androidApp: app Android (iOS: ver iosApp/)
include(":core", ":ui", ":app", ":androidApp")
