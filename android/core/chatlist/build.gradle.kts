plugins {
    id("persona.android.library")
    id("persona.compose")
}

android { namespace = "persona.core.chatlist" }

dependencies {
    api(libs.compose.material3)
    implementation(libs.androidx.lifecycle.compose)
    implementation(libs.coroutines.android)
    testImplementation(libs.junit)
    // A real composition counts recompositions (ChatListStateTest); Compose calls android.os.Trace.
    testImplementation("org.robolectric:robolectric:4.17")
}
