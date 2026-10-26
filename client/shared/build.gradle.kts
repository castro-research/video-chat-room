plugins {
    kotlin("multiplatform")
}

// Framework para a UI nativa do iOS (SwiftUI): core + media, sem Compose UI. O Swift só vê o KurzController (ver iosApp/).
kotlin {
    listOf(iosArm64(), iosSimulatorArm64()).forEach {
        it.binaries.framework {
            baseName = "KurzShared"
            isStatic = true
        }
    }
    sourceSets {
        iosMain.dependencies {
            api(project(":core"))
            api(project(":media"))
            implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.10.2")
        }
    }
}
