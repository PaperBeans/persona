package persona.feature.machines

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ComputerCode
import persona.core.contracts.api.ComputerOs
import persona.core.contracts.api.ComputerPairing
import persona.core.contracts.api.CurrentAssistant
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.ManagerRoster

/**
 * A new computer is the current assistant's (docs/assistant/managers.md "Clients", docs/protocol/protocol.md "Computer pairing
 * codes"): its id goes as `add.manager`, the page says when the hub put it elsewhere, and an offline assistant gets none.
 */
class AddComputerAssistantTest {
    private class Refused(override val status: Int?, override val code: String?) : Exception("refused"), HubCodedFailure

    private class Hub(var answer: String? = null) : ComputerPairing {
        override val canAdd = flowOf(true)
        val asked = mutableListOf<String?>()
        val rejected = mutableListOf<String>()
        var refuse: Exception? = null
        override suspend fun add(name: String, os: ComputerOs) = add(name, os, null)
        override suspend fun add(name: String, os: ComputerOs, manager: String?): ComputerCode {
            asked += manager
            refuse?.let { throw it }
            return ComputerCode("macbook", name, "persona://link?machine=macbook", 600_000, manager = answer)
        }
        override suspend fun again(machineId: String) = ComputerCode(machineId, "", "persona://link?2", 1_200_000, manager = answer)
        override suspend fun machines(machineId: String) = emptyList<Machine>()
        override suspend fun confirm(machineId: String) = Unit
        override suspend fun reject(machineId: String) { rejected += machineId }
    }

    private val roster = ManagerRoster(listOf(
        HubManager("assistant", "助手", online = true, isDefault = true, builtin = true),
        HubManager("m-3fa21c", "工作", online = true),
        HubManager("m-8b04d2", "家里", online = false),
    ), "assistant")

    private fun current(id: String?, roster: ManagerRoster? = this.roster) =
        CurrentAssistant(roster, AssistantManagerChoice.Choice("hub", id, "assistant"))

    private fun model(hub: Hub) = AddComputerModel(hub, MutableStateFlow(emptyList())) { 0L }.also { it.setName("MacBook") }

    @Test fun theCurrentAssistantsIdGoesWithTheAdd() = runBlocking {
        val hub = Hub(answer = "m-3fa21c")
        val work = model(hub)
        work.add(current("m-3fa21c"))
        // Where it was asked for: nothing to say.
        assertNull(work.state.value.placedUnder())
        // The default by its real id, not none.
        hub.answer = "assistant"
        model(hub).add(current(null))
        // No roster (a hub from before it, or not read yet): none named, unless this device kept another pick for the hub.
        model(hub).add(current(null, roster = null))
        model(hub).add(null)
        model(hub).add(current("m-3fa21c", roster = null))
        assertEquals(listOf("m-3fa21c", "assistant", null, null, "m-3fa21c"), hub.asked)
    }

    @Test fun thePageSaysWhereTheHubPutIt() = runBlocking {
        // The hub put it under another assistant than the one asked for.
        val elsewhere = model(Hub(answer = "assistant")).also { it.add(current("m-3fa21c")) }
        assertEquals("助手", elsewhere.state.value.placedUnder())
        // A hub from before add.manager names none: a new computer is its own assistant's.
        val older = model(Hub(answer = null)).also { it.add(current("m-3fa21c")) }
        assertEquals("助手", older.state.value.placedUnder())
        // One the roster doesn't know reads as its id.
        val unknown = model(Hub(answer = "m-gone")).also { it.add(current("m-3fa21c")) }
        assertEquals("m-gone", unknown.state.value.placedUnder())
        // A new code for the same computer keeps what it was asked for.
        elsewhere.again()
        assertEquals("助手", elsewhere.state.value.placedUnder())
        // One assistant: nothing to tell apart.
        val single = ManagerRoster(listOf(roster.managers.first()), "assistant")
        val one = model(Hub(answer = null)).also { it.add(current(null, roster = single)) }
        assertNull(one.state.value.placedUnder())
    }

    @Test fun anOfflineAssistantGetsNoComputer() = runBlocking {
        val hub = Hub(answer = "m-8b04d2")
        val home = current("m-8b04d2")
        assertEquals("家里", home.offlineForAdding()?.name)
        model(hub).add(home)
        assertTrue("nothing asked of the hub", hub.asked.isEmpty())
        // Online, or alone: as before.
        assertNull(current("m-3fa21c").offlineForAdding())
        val alone = ManagerRoster(listOf(HubManager("assistant", "助手", online = false, isDefault = true)), "assistant")
        assertNull(current(null, roster = alone).offlineForAdding())
    }

    @Test fun theComputersInTheWayAreEveryAssistantsAndCanBeRemoved() = runBlocking {
        val hub = Hub().apply { refuse = Refused(429, "too_many_pending") }
        val model = model(hub)
        model.add(current("m-3fa21c"))
        assertTrue(model.state.value.tooMany)
        // The hub's list, every assistant's: the waiting ones, whoever's they are, are in the way.
        val listed = listOf(
            Machine("a", "server", trust = "pending", errorCode = "never_connected"),
            Machine("b", "MacBook", trust = "pending", online = true, manager = "m-3fa21c"),
            Machine("c", "NAS", trust = "pending", manager = "m-8b04d2"),
            Machine("d", "Desk", online = true),
        )
        assertEquals(listOf("a", "b", "c"), inTheWay(listed).map { it.id })
        hub.refuse = null
        model.remove("c")
        assertEquals(listOf("c"), hub.rejected)
        assertFalse(model.state.value.tooMany)
        assertNull(model.state.value.error)
        // The hub's words for the assistant asked for.
        for (code in listOf("no_manager", "installing", "assistant_offline", "owners_unknown")) {
            hub.refuse = Refused(409, code)
            model.add(current("m-3fa21c"))
            assertFalse(model.state.value.tooMany)
        }
    }

    @Test fun theDefaultAskedForAndAnOlderHubsReplyIsWhereItWas() = runBlocking {
        // Several assistants, an older hub, the current one is the hub's own: the computer is there, nothing to say.
        val own = model(Hub(answer = null)).also { it.add(current(null)) }
        assertNull(own.state.value.placedUnder())
        // A reused pending computer (added = false) stays with whoever has it: the machines list says who.
        val reused = model(Hub(answer = null)).also { it.add(current("m-3fa21c")) }
        val listed = listOf(Machine("macbook", "MacBook", trust = "pending", manager = "m-8b04d2"))
        assertEquals("家里", reused.state.value.placedUnder(listed))
        // Listed under the asked-for assistant: nothing to say.
        assertNull(reused.state.value.placedUnder(listOf(Machine("macbook", "MacBook", trust = "pending", manager = "m-3fa21c"))))
        // The roster the page has now, when the one the add left with was none.
        val cold = model(Hub(answer = "assistant")).also { it.add(current("m-3fa21c", roster = null)) }
        assertNull(cold.state.value.placedUnder())
        assertEquals("助手", cold.state.value.placedUnder(current = roster))
    }

    @Test fun aKeptPickOrDefaultIsSentBeforeTheRosterIsRead() = runBlocking {
        val hub = Hub()
        // The default is another assistant than the hub's own (only a roster ever said so): it is named.
        val other = CurrentAssistant(null, AssistantManagerChoice.Choice("hub", null, "m-3fa21c"))
        model(hub).add(other)
        // The hub's own as the default, or nothing known: none, as before.
        model(hub).add(CurrentAssistant(null, AssistantManagerChoice.Choice("hub", null, "assistant")))
        assertEquals(listOf("m-3fa21c", null), hub.asked)
    }

    @Test fun onlyTheSelectedGroupsWaitingComputersAreInTheWay() {
        val listed = listOf(
            Machine("a", "server", trust = "pending"),
            Machine("b~g0000abcd", "NAS", trust = "pending"),
            Machine("c", "MacBook"),
        )
        assertEquals(listOf("a"), inTheWay(listed, null).map { it.id })
        assertEquals(listOf("b~g0000abcd"), inTheWay(listed, "g0000abcd").map { it.id })
    }

    @Test fun theHubsWordsShowForTheFourRefusals() {
        for ((status, code) in listOf(404 to "no_manager", 409 to "installing", 503 to "assistant_offline", 503 to "owners_unknown")) {
            val refused = persona.core.hub.HubFailure(persona.core.hub.HubError.Server, "hub words $code", status, code)
            assertEquals(persona.core.designsystem.UiText.Plain("hub words $code"), addErrorText(refused))
        }
    }
}
