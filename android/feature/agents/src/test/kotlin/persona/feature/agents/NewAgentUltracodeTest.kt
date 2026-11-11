package persona.feature.agents

import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.*
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.api.*

/** New Agent's ultracode switch (#582), as iOS `UltracodeTests`' start cases. */
@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class NewAgentUltracodeTest {
    @Test fun offeredForClaudeWithAnEffortOnAHubThatHasIt() {
        assertTrue(NewAgentUltracode.offered(setOf("ultracode"), "claude", null))
        assertTrue(NewAgentUltracode.offered(setOf("ultracode"), "claude", listOf("high")))
        assertFalse(NewAgentUltracode.offered(setOf("ultracode"), "claude", emptyList()))
        assertFalse(NewAgentUltracode.offered(setOf("ultracode"), "codex", null))
        assertFalse(NewAgentUltracode.offered(emptySet(), "claude", null))
        assertTrue(NewAgentUltracode.tooOld("2.1.283"))
        assertTrue(NewAgentUltracode.tooOld("v1.9.999"))
        assertFalse(NewAgentUltracode.tooOld("2.1.284"))
        assertFalse(NewAgentUltracode.tooOld("2.2.0-beta"))
        assertFalse(NewAgentUltracode.tooOld("unknown"))
        assertFalse(NewAgentUltracode.tooOld(null))
    }

    @Test fun theStartCarriesItAndARefusalTurnsItOff() = runTest {
        Dispatchers.setMain(StandardTestDispatcher(testScheduler))
        val service = Service()
        val model = NewAgentViewModel(service, Store(), MutableStateFlow(listOf(Machine("first", online = true))))
        try {
            advanceUntilIdle()
            model.selection(model.state.value.selection.copy(cwd = "/code", launcher = "claude"))
            model.ultracode(true)
            model.submit()
            advanceUntilIdle()
            assertEquals(true, service.request?.ultracode)
            assertFalse(model.state.value.ultracode)
            assertEquals(R.string.agents_new_ultracode_failed, model.state.value.error?.id)
            service.refuse = false
            model.submit()
            advanceUntilIdle()
            assertNull(service.request?.ultracode)
        } finally {
            model.viewModelScope.cancel()
            Dispatchers.resetMain()
        }
    }

    @Test fun theRowMarkIsForAClaudeAgentWithItOnThatIsNotOffline() {
        val on = persona.core.contracts.Session(machineId = "m", sessionId = "s", agent = "claude", status = "working", ultracode = true)
        assertTrue(ultracodeMark(on))
        assertFalse(ultracodeMark(on.copy(ultracode = null)))
        assertFalse(ultracodeMark(on.copy(ultracode = false)))
        assertFalse(ultracodeMark(on.copy(status = "offline", state = "offline")))
        assertFalse(ultracodeMark(on.copy(assistant = true)))
        assertFalse(ultracodeMark(on.copy(agent = "codex")))
    }

    private class Store : AgentCreationStore {
        var operation: AgentStartOperation? = null
        override suspend fun lastMachine(scope: String) = "first"
        override suspend fun defaults(scope: String, machine: String) = AgentDefaults()
        override suspend fun saveDefaults(scope: String, machine: String, defaults: AgentDefaults) = Unit
        override suspend fun pending(scope: String) = operation
        override suspend fun pending(scope: String, value: AgentStartOperation?) { operation = value }
    }

    private class Service : AgentCreationService {
        override val scope = "hub/pairing"
        var request: AgentStartRequest? = null
        var refuse = true
        override suspend fun launchers(machineId: String) = listOf(AgentLauncher("claude"))
        override suspend fun directories(machineId: String, path: String) = AgentDirectories(path.ifEmpty { "/code" })
        override suspend fun start(request: AgentStartRequest, operationId: String): AgentStartResult {
            this.request = request
            return if (refuse) AgentStartResult.Rejected(persona.core.contracts.CoreText.plain("Claude Code is too old"))
            else AgentStartResult.Unknown
        }
        override suspend fun operation(operationId: String) = AgentStartResult.Unknown
    }
}
