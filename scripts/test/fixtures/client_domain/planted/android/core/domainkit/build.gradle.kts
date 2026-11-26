// The planted domain kit: a JVM library with no dependency.
plugins {
    id("persona.jvm.library")
}

kotlin {
    explicitApi()
    compilerOptions {
        allWarningsAsErrors.set(true)
        freeCompilerArgs.add("-Xreturn-value-checker=full")
    }
}
