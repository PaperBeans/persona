package persona.core.domain.models

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.PendingSwitch
import persona.core.contracts.Session
import persona.core.contracts.api.AgentLauncher
import persona.core.contracts.api.AgentModel
import persona.core.contracts.api.LauncherModels

/**
 * An Agent chat's pill and panel (the web's `lib/session-panel.test.ts`, #5055): the providers, the switch to another
 * launcher only on a hub with `launcher_switch`, what a pick sends and the pending switch the pill and the panel show.
 */
class AgentModelPanelTest {
    private val agent = Session(machineId = "m1", sessionId = "s1", agent = "claude", launcher = "example", model = "claude-opus-5-5",
        effort = "high", status = "idle", state = "idle", paneId = "w1:p1")
    private val launchers = listOf(AgentLauncher("example"), AgentLauncher("work", displayName = "Work"), AgentLauncher("cx", agentType = "codex"))
    private val lists = mapOf(
        "example" to LauncherModels("claude", listOf(AgentModel("default", "Default", resolved = "claude-opus-5-5", isDefault = true),
            AgentModel("opus", "Opus 5.5", resolved = "claude-opus-5-5", efforts = listOf("low", "high")), AgentModel("sonnet", "Sonnet 4.5"))),
        "work" to LauncherModels("claude", listOf(AgentModel("default", "Default", isDefault = true), AgentModel("glm-5.3", "GLM 5.3"))),
        "cx" to LauncherModels("codex", listOf(AgentModel("gpt-5.5", "GPT-5.5"))),
    )
    private fun read(name: String) = ModelList(read = true, list = lists[name])
    private val switching = setOf(AgentModelPanel.LAUNCHER_SWITCH)

    @Test fun anotherLauncherIsOffWithoutTheSwitch() {
        val panel = AgentModelPanel.panel(agent, launchers, ::read, emptySet())
        assertEquals(listOf("example", "work", "cx"), panel.providers.map { it.id })
        assertNull(panel.providers[0].unavailable)
        assertEquals(PanelReason.OtherProvider, panel.providers[1].unavailable)
        assertEquals(PanelReason.OtherEngine("claude", "codex"), panel.providers[2].unavailable)
        assertEquals("Work", panel.providers[1].name)
        // what runs: the alias row that stands for it, not the default entry
        assertEquals(PanelConfig("example", "opus", "high"), panel.state.current)
        assertEquals(listOf("example"), panel.state.recommended)
        assertNull(AgentModelPanel.pickPlan(agent, PanelConfig("work", "glm-5.3"), { lists[it] }, emptySet()))
    }

    @Test fun withTheSwitchAnotherLauncherOfTheEngineIsAPickForTheNextMessage() {
        val panel = AgentModelPanel.panel(agent, launchers, ::read, switching)
        assertNull(panel.providers[1].unavailable)
        assertTrue(panel.providers[1].typable)
        assertEquals("another engine still needs a new conversation", PanelReason.OtherEngine("claude", "codex"), panel.providers[2].unavailable)
        assertEquals(listOf(ModeStep.Launcher("work", from = "example", model = "glm-5.3", effort = "high")),
            AgentModelPanel.pickPlan(agent, PanelConfig("work", "glm-5.3", "high"), { lists[it] }, switching))
        // its default entry sends no model
        assertEquals(listOf(ModeStep.Launcher("work", from = "example")),
            AgentModelPanel.pickPlan(agent, PanelConfig("work", "default"), { lists[it] }, switching))
        // not for an Agent whose model can't be switched (offline, a Persona chat, Codex)
        assertNull(AgentModelPanel.pickPlan(agent.copy(status = "offline", state = "offline"), PanelConfig("work", "glm-5.3"), { lists[it] }, switching))
        assertFalse(AgentModelPanel.switchesLauncher(agent.copy(assistant = true), switching))
        assertFalse(AgentModelPanel.switchesLauncher(agent.copy(agent = "codex"), switching))
    }

    @Test fun aLauncherThatFixesItsModelTakesOnlyItsDefault() {
        val locked = lists + ("work" to lists.getValue("work").copy(locked = "--model is set"))
        val panel = AgentModelPanel.panel(agent, launchers, { ModelList(read = true, list = locked[it]) }, switching)
        assertEquals(listOf("default"), panel.providers[1].models?.map { it.id })
        assertNull(AgentModelPanel.pickPlan(agent, PanelConfig("work", "glm-5.3"), { locked[it] }, switching))
    }

    @Test fun theOwnLauncherSwitchesAtOnceInOrder() {
        assertEquals(listOf(ModeStep.Model("sonnet")),
            AgentModelPanel.pickPlan(agent, PanelConfig("example", "sonnet"), { lists[it] }, emptySet()))
        assertEquals(listOf(ModeStep.Model("sonnet"), ModeStep.Effort("low")),
            AgentModelPanel.pickPlan(agent, PanelConfig("example", "sonnet", "low"), { lists[it] }, emptySet()))
        // the model that runs, picked again with its effort: nothing to send
        assertEquals(emptyList<ModeStep>(), AgentModelPanel.pickPlan(agent, PanelConfig("example", "opus", "high"), { lists[it] }, emptySet()))
        // a waiting switch is dropped first, by naming the own launcher
        val waiting = agent.copy(pendingSwitch = PendingSwitch("work", "glm-5.3"))
        assertEquals(listOf(ModeStep.Launcher("example", "example")),
            AgentModelPanel.pickPlan(waiting, PanelConfig("example", "opus", "high"), { lists[it] }, switching))
        // an id typed by hand goes as it is; one the hub would refuse doesn't
        assertEquals(listOf(ModeStep.Model("claude-opus-5-6")),
            AgentModelPanel.pickPlan(agent, PanelConfig("example", "claude-opus-5-6"), { lists[it] }, emptySet()))
        assertNull(AgentModelPanel.pickPlan(agent, PanelConfig("example", "two words"), { lists[it] }, emptySet()))
    }

    @Test fun theSameSwitchAgainSendsNothing() {
        val waiting = agent.copy(pendingSwitch = PendingSwitch("work", "glm-5.3", "high"))
        assertEquals(emptyList<ModeStep>(), AgentModelPanel.pickPlan(waiting, PanelConfig("work", "glm-5.3", "high"), { lists[it] }, switching))
    }

    @Test fun aWaitingSwitchIsThePanelsPendingSetupAndItsNote() {
        val waiting = agent.copy(pendingSwitch = PendingSwitch("work", effort = "max"))
        val state = AgentModelPanel.panel(waiting, launchers, ::read, switching).state
        assertEquals(PanelConfig("work", "default", "max"), state.pending)
        assertEquals(PanelNote.Pending("Work"), state.note)
        val pill = AgentModelPanel.pendingPill(waiting, ::read, launchers, "Default model")
        assertEquals(PillSetup("claude", "Work", "Default", "max"), pill)
        assertNull("its own launcher is no switch", AgentModelPanel.pendingPill(agent.copy(pendingSwitch = PendingSwitch("example")), ::read, launchers, "x"))
    }

    @Test fun picksWaitWhileTheAgentWorks() {
        val working = agent.copy(status = "working", state = "working")
        assertEquals(PanelReason.Busy, AgentModelPanel.panel(working, launchers, ::read, switching).state.busy)
        assertNull(AgentModelPanel.panel(agent, launchers, ::read, switching).state.busy)
    }

    @Test fun aCodexThreadKeepsItsModel() {
        val codex = agent.copy(agent = "codex", launcher = "cx", model = "gpt-5.5", efforts = listOf("low", "medium", "high"))
        val panel = AgentModelPanel.panel(codex, launchers, ::read, switching)
        assertEquals(PanelReason.ModelFixed("codex"), panel.providers.first { it.id == "cx" }.unavailable)
        assertEquals("its own levels", listOf("low", "medium", "high"), panel.providers.first { it.id == "cx" }.models?.single()?.efforts)
        assertTrue(panel.state.recommended.isEmpty())
    }

    @Test fun thePillNamesWhatRuns() {
        assertEquals(PillView(PillSetup("claude", "example", "Opus 5.5", "high")), AgentModelPanel.pill(agent, read("example"), launchers))
        // before the list: a short form of the id
        assertEquals("Opus 5.5", AgentModelPanel.pill(agent, ModelList(loading = true), null).setup.model)
        val unknown = agent.copy(model = null)
        assertTrue(AgentModelPanel.pill(unknown, ModelList(loading = true), null).loading)
        assertTrue(AgentModelPanel.pill(unknown, ModelList(error = "offline"), null).failed)
    }

    @Test fun aModelTheListLacksStaysListed() {
        val odd = agent.copy(model = "vendor/glm-5.1")
        val own = AgentModelPanel.panel(odd, launchers, ::read, emptySet()).providers.first()
        assertEquals("vendor/glm-5.1", own.models?.last()?.id)
        // while its list is read, what runs is there already
        val reading = AgentModelPanel.panel(agent, launchers, { ModelList(loading = true) }, emptySet()).providers.first()
        assertEquals(listOf("claude-opus-5-5"), reading.models?.map { it.id })
        assertTrue(reading.loading)
    }

    @Test fun switchRefusalsAreTheSwitches() {
        assertTrue(AgentModelPanel.switchRefused(409, "switch_failed"))
        assertFalse(AgentModelPanel.switchRefused(409, "input_draft"))
        assertFalse(AgentModelPanel.switchRefused(400, "switch_stale"))
    }
}
