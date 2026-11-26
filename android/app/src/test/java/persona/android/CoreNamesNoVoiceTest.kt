package persona.android

import org.junit.Assert.assertEquals
import org.junit.Test
import java.io.File

/**
 * Full-duplex voice lives in `:module:voice` (#13431): the core's own sources name none of its code, so leaving the module
 * out (`-Ppersona.modules=`) leaves nothing behind. Only the module list (Modules.kt) names the module. Composer dictation
 * (speech to text) is core and is not voice-module code.
 */
class CoreNamesNoVoiceTest {
    private val voiceCode = Regex("""persona\.module\.voice|\bVoice(Orb|Status|Caption|Control|ControlBar|Session|Page|Screen|Audio|Extensions|Protocol|Prefs|Route|Module)\b""")

    @Test fun theCoreNamesNoVoiceModuleCode() {
        val root = generateSequence(File("").absoluteFile) { it.parentFile }
            .first { File(it, "settings.gradle.kts").isFile && File(it, "module").isDirectory }
        val hits = root.walkTopDown()
            .onEnter { it != File(root, "module") && it.name != "build" && it.name != "test" && it.name != "androidTest" && !it.name.startsWith(".") }
            .filter { it.isFile && (it.extension == "kt" || it.extension == "kts") && it.name != "Modules.kt" && it.name != "settings.gradle.kts" }
            .flatMap { file -> file.readLines().mapIndexedNotNull { i, line -> if (voiceCode.containsMatchIn(line)) "${file.relativeTo(root)}:${i + 1}" else null } }
            .toList()
        assertEquals(emptyList<String>(), hits)
    }
}
