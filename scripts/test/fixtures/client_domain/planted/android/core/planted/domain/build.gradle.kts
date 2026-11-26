// The planted context's domain module: a JVM library on the kit alone.
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

dependencies {
    implementation(project(":core:domainkit"))
}
