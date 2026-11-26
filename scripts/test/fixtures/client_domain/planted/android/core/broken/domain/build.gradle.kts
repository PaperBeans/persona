// A planted domain module breaking every build rule once. expected.txt lists the findings.
plugins {
    id("persona.jvm.library")
    id("org.jetbrains.kotlin.plugin.serialization")
}

dependencies {
    implementation(project(":core:domainkit"))
    implementation(project(":core:contracts"))
    implementation(project(":core:planted:domain"))
    testImplementation(libs.junit)
}
