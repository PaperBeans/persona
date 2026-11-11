plugins {
    id("com.android.library")
}

val libs = extensions.getByType<VersionCatalogsExtension>().named("libs")
android {
    buildToolsVersion = libs.findVersion("buildTools").get().requiredVersion
    compileSdk = libs.findVersion("compileSdk").get().requiredVersion.toInt()
    defaultConfig {
        minSdk = libs.findVersion("minSdk").get().requiredVersion.toInt()
    }
    // Robolectric tests run in Simplified Chinese unless they say otherwise (build-logic/robolectric).
    sourceSets.getByName("test").resources.srcDir(rootProject.file("build-logic/robolectric"))
    // The core modules' words in Simplified Chinese for code with no resources at hand (CoreTextFallback).
    sourceSets.getByName("test").resources.srcDir(rootProject.file("core/designsystem/src/main/res/values-zh"))
    // The shared fixtures (spec/fixtures) for JVM unit tests, as persona.jvm.library sets it.
    testOptions.unitTests.all { test ->
        val repositoryRoot = providers.gradleProperty("personaRepoRoot")
            .getOrElse(rootProject.layout.projectDirectory.dir("..").asFile.absolutePath)
        test.systemProperty("persona.repo.root", repositoryRoot)
        test.inputs.files(fileTree(File(repositoryRoot, "spec/fixtures")) { include("**/*.json") })
            .withPropertyName("sharedFixtures")
            .withPathSensitivity(PathSensitivity.RELATIVE)
        // Robolectric under JDK 21 accesses raw FileDescriptor internals and the internal access bridge.
        test.jvmArgs(
            "--add-opens=java.base/java.io=ALL-UNNAMED",
            "--add-exports=java.base/jdk.internal.access=ALL-UNNAMED"
        )
    }
    compileOptions {
        sourceCompatibility = JavaVersion.toVersion(libs.findVersion("jvm").get().requiredVersion)
        targetCompatibility = JavaVersion.toVersion(libs.findVersion("jvm").get().requiredVersion)
    }
}
