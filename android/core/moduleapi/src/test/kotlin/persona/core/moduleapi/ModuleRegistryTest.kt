package persona.core.moduleapi

import androidx.navigation3.runtime.NavKey
import kotlinx.coroutines.flow.MutableStateFlow
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class ModuleRegistryTest {
    private object Page : NavKey

    private class Example(override val id: String, override val feature: String? = null, private val keeps: Boolean = true) : PersonaModule {
        var built = 0
        override fun services(hub: ModuleHub): ModuleServices? = if (keeps) object : ModuleServices {}.also { built++ } else null
        override fun debugPages(): Map<String, NavKey> = mapOf(id to Page)
    }

    private val hub = object : ModuleHub {
        override val identity = "pairing"
        override val features = MutableStateFlow(emptySet<String>())
        override val token = "fixture-token"
        override val http = OkHttpClient()
        override fun address(): String? = null
        override fun isCurrent() = true
    }

    private val plain = Example("plain")
    private val gated = Example("knowledge-ui", feature = ModuleRegistry.moduleFeature("knowledge-ui"))
    private val registry = ModuleRegistry(listOf(plain, gated))

    @Test fun theFlagNamesAreTheOnesEveryClientUses() {
        assertEquals("modules", ModuleRegistry.MODULES_MARKER)
        assertEquals("module.knowledge-ui", ModuleRegistry.moduleFeature("knowledge-ui"))
    }

    @Test fun beforeAHelloAndInDemoModeEveryModuleIsOn() {
        // No hello yet and demo mode both read an empty feature set.
        assertEquals(listOf(plain, gated), registry.active(emptySet()))
    }

    @Test fun anOlderHubWithoutTheMarkerLeavesEveryModuleOn() {
        assertEquals(listOf(plain, gated), registry.active(setOf("extensions", "inbox_sources")))
    }

    @Test fun aHubThatPublishesModuleFlagsTurnsAModuleOnWithItsFlag() {
        assertEquals(listOf(plain, gated), registry.active(setOf("modules", "module.knowledge-ui", "extensions")))
    }

    @Test fun aHubThatPublishesModuleFlagsTurnsAModuleOffWithoutItsFlag() {
        assertEquals(listOf(plain), registry.active(setOf("modules", "module.voice")))
        assertFalse(ModuleRegistry.isActive(gated, setOf("modules")))
        // A module that needs no feature stays on wherever it is carried.
        assertTrue(ModuleRegistry.isActive(plain, setOf("modules")))
    }

    /** The hub's recording module (#15214): no carried module, the same flag name and the same rule. */
    @Test fun theHubsRecordingModuleFollowsTheSameRule() {
        assertEquals("recording", HubModule.Recording.id)
        assertEquals("module.recording", HubModule.Recording.feature)
        // No hello yet, demo mode and an older hub without the marker: on.
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, emptySet()))
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, setOf("extensions", "inbox_sources")))
        // A flag without the marker changes nothing, and neither does its absence.
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, setOf("module.recording")))
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, setOf("module.knowledge-ui")))
        // A hub that publishes module flags: on with its flag, off without it, whatever the other modules are.
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, setOf("modules", "module.recording")))
        assertFalse(ModuleRegistry.isActive(HubModule.Recording, setOf("modules")))
        assertFalse(ModuleRegistry.isActive(HubModule.Recording, setOf("modules", "module.knowledge-ui", "module.voice")))
        // It switches no carried module: the pages that show stored recordings stay.
        assertEquals(listOf(plain, gated), registry.active(setOf("modules", "module.knowledge-ui")))
    }

    /** The hub's Lark module (#15995): the same rule under its own flag, apart from the Lark extension's `lark`. */
    @Test fun theHubsLarkModuleFollowsTheSameRule() {
        assertEquals("lark", HubModule.Lark.id)
        assertEquals("module.lark", HubModule.Lark.feature)
        // No hello yet, demo mode and an older hub without the marker: on.
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, emptySet()))
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("extensions", "lark", "lark_attachments")))
        // A flag without the marker changes nothing, and neither does its absence.
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("module.lark")))
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("module.recording")))
        // A hub that publishes module flags: on with its flag, off without it, whatever the other modules are.
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "module.lark")))
        assertFalse(ModuleRegistry.isActive(HubModule.Lark, setOf("modules")))
        assertFalse(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "module.recording", "module.voice")))
        // The Lark extension's own feature is not the module's flag: `lark` neither turns the module on nor off.
        assertFalse(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "lark", "lark_moments")))
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "module.lark")))
        // Each hub module answers for itself.
        assertTrue(ModuleRegistry.isActive(HubModule.Recording, setOf("modules", "module.recording")))
        assertFalse(ModuleRegistry.isActive(HubModule.Recording, setOf("modules", "module.lark")))
        assertEquals(listOf(HubModule.Recording, HubModule.Lark, HubModule.Mail), HubModule.entries)
    }

    /** The hub's mail module (#15995): the same rule under its own flag. */
    @Test fun theHubsMailModuleFollowsTheSameRule() {
        assertEquals("mail", HubModule.Mail.id)
        assertEquals("module.mail", HubModule.Mail.feature)
        // No hello yet, demo mode and an older hub without the marker: on.
        assertTrue(ModuleRegistry.isActive(HubModule.Mail, emptySet()))
        assertTrue(ModuleRegistry.isActive(HubModule.Mail, setOf("extensions", "inbox_sources")))
        // A flag without the marker changes nothing, and neither does its absence.
        assertTrue(ModuleRegistry.isActive(HubModule.Mail, setOf("module.mail")))
        assertTrue(ModuleRegistry.isActive(HubModule.Mail, setOf("module.lark")))
        // A hub that publishes module flags: on with its flag, off without it, whatever the other modules are.
        assertTrue(ModuleRegistry.isActive(HubModule.Mail, setOf("modules", "module.mail")))
        assertFalse(ModuleRegistry.isActive(HubModule.Mail, setOf("modules")))
        assertFalse(ModuleRegistry.isActive(HubModule.Mail, setOf("modules", "module.lark", "module.recording")))
        // Each hub module answers for itself.
        assertTrue(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "module.lark")))
        assertFalse(ModuleRegistry.isActive(HubModule.Lark, setOf("modules", "module.mail")))
    }

    @Test fun aFlagWithoutTheMarkerChangesNothing() {
        assertEquals(listOf(plain, gated), registry.active(setOf("module.voice")))
    }

    @Test fun settingsPagesComeOnlyFromActiveModules() {
        val page = ModuleSettingsPage("knowledge-ui.settings", 0, persona.core.designsystem.icons.PersonaIcons.options, Page)
        val withPage = object : PersonaModule {
            override val id = "knowledge-ui"
            override val feature = ModuleRegistry.moduleFeature(id)
            override fun settingsPages() = listOf(page)
        }
        val registry = ModuleRegistry(listOf(plain, withPage))
        assertEquals(listOf(page), registry.settingsPages(emptySet()))
        assertEquals(listOf(page), registry.settingsPages(setOf("modules", "module.knowledge-ui")))
        assertTrue(registry.settingsPages(setOf("modules")).isEmpty())
        assertTrue(ModuleRegistry.Empty.settingsPages(emptySet()).isEmpty())
    }

    @Test fun anEmptyListIsPersonaCoreAlone() {
        assertTrue(ModuleRegistry.Empty.active(setOf("anything")).isEmpty())
        assertTrue(ModuleRegistry.Empty.services(hub).isEmpty())
        assertTrue(ModuleRegistry.Empty.debugPages().isEmpty())
    }

    @Test fun servicesAreBuiltOncePerPairingAndOnlyForModulesThatKeepSome() {
        val keeps = Example("voice")
        val none = Example("glasses", keeps = false)
        val services = ModuleRegistry(listOf(keeps, none)).services(hub)
        assertEquals(setOf("voice"), services.keys)
        assertEquals(1, keeps.built)
    }

    @Test fun debugPagesComeFromEveryCarriedModule() {
        assertEquals(mapOf("voice" to Page, "mail" to Page), ModuleRegistry(listOf(Example("voice"), Example("mail"))).debugPages())
    }

    @Test fun idsAreLowerCaseHyphenatedAndUnique() {
        for (bad in listOf("Voice", "voice_chat", "-voice", "voice-", "", "phone extra")) {
            try { ModuleRegistry(listOf(Example(bad))); fail("'$bad' was accepted") } catch (_: IllegalArgumentException) {}
        }
        ModuleRegistry(listOf(Example("phone-extra"), Example("engines-extra2")))
        try { ModuleRegistry(listOf(Example("voice"), Example("voice"))); fail("A module listed twice was accepted") }
        catch (_: IllegalArgumentException) {}
    }

    @Test fun theContractsDefaultsAddNothing() {
        val bare = object : PersonaModule { override val id = "bare" }
        assertEquals(null, bare.feature)
        assertEquals(null, bare.services(hub))
        assertTrue(bare.settingsPages().isEmpty())
        assertTrue(bare.audioUses.isEmpty())
        assertTrue(bare.debugPages().isEmpty())
        val arguments = mutableMapOf("debug.voiceAudio" to "fake")
        bare.debugArguments(arguments)
        assertEquals(mapOf("debug.voiceAudio" to "fake"), arguments)
    }

    @Test fun audioUsesRankCallConversationRecordingPlayback() {
        assertEquals(listOf(AudioUse.Call, AudioUse.Conversation, AudioUse.Recording, AudioUse.Playback), AudioUse.entries)
        assertTrue(AudioUse.Call.outranks(AudioUse.Conversation))
        assertTrue(AudioUse.Conversation.outranks(AudioUse.Recording))
        assertFalse(AudioUse.Playback.outranks(AudioUse.Recording))
        assertFalse(AudioUse.Call.outranks(AudioUse.Call))
    }

    @Test fun theRegistryKeepsTheListsOrder() {
        val first = Example("b"); val second = Example("a")
        val registry = ModuleRegistry(listOf(first, second))
        assertSame(first, registry.modules[0])
        assertSame(second, registry.active(emptySet())[1])
    }
}
