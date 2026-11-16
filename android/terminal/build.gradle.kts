// Vendored Termux terminal-emulator + terminal-view (Apache License 2.0), adapted
// to drive a remote terminal instead of a local pty. See NOTICE.
plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "com.termux.view"
    compileSdk {
        version = release(37) {
            minorApiLevel = 2
        }
    }
    defaultConfig {
        minSdk = 28
        consumerProguardFiles("consumer-rules.pro")
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    lint {
        abortOnError = false
    }
}

dependencies {
    implementation("androidx.annotation:annotation:1.9.1")
}
