package persona.feature.settings

import persona.core.contracts.api.*
import persona.core.designsystem.Status
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.junit.Assert.*
import org.junit.Test
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config
import java.time.LocalDate
import java.time.ZoneId

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class EndpointEditorStateTest {
    private val res = RuntimeEnvironment.getApplication().resources
    private val manual = EndpointEntry("https://hub.example.com:18662", "name", EndpointSource.Manual)
    private val hub = EndpointEntry("https://[2001:db8::1]:18662", "ipv6", EndpointSource.Hub)
    private val lan = EndpointEntry("http://127.0.0.1:18660", "lan", EndpointSource.Pairing)
    private val v4 = EndpointEntry("https://192.0.2.10:18662", "ipv4", EndpointSource.Hub)
    /** The connection's order in the ordering tests: hub, manual, lan, v4. */
    private val four = listOf(hub, manual, lan, v4)

    private fun urls(vararg entries: EndpointEntry) = entries.map { it.url }
    private fun EndpointEditorState.shownUrls() = shown().map { it.url }

    @Test fun invalidInputKeepsSaveDisabledAndShowsTheMessage() = runBlocking {
        val endpoints = FakeEndpoints()
        val editor = EndpointEditorState(endpoints)
        editor.startAdding()
        assertEquals(EndpointSheet(), editor.ui.value.sheet)
        assertFalse(editor.ui.value.sheet!!.canSave)
        assertNull(editor.ui.value.sheet!!.hint(res))

        editor.updateAddress("hub.example.com")
        val sheet = editor.ui.value.sheet!!
        assertTrue(sheet.check is EndpointCheck.Invalid)
        assertEquals("地址要以 https:// 或 http:// 开头", sheet.hint(res))
        assertFalse(sheet.canSave)
        editor.save()
        assertTrue(endpoints.added.isEmpty())
        assertTrue(endpoints.tested.isEmpty())
        assertNotNull(editor.ui.value.sheet)

        editor.updateAddress("   ")
        assertNull(editor.ui.value.sheet!!.check)
        assertNull(editor.ui.value.sheet!!.hint(res))
        assertFalse(editor.ui.value.sheet!!.canSave)
    }

    @Test fun validInputSavesClosesTheSheetAndTestsTheSavedUrl() = runBlocking {
        val endpoints = FakeEndpoints()
        val adding = CompletableDeferred<Unit>()
        val testing = CompletableDeferred<Unit>()
        endpoints.addGate = adding
        endpoints.testGate = testing
        val editor = EndpointEditorState(endpoints)
        editor.startAdding()
        editor.updateAddress("  https://HUB.example.com:18662/ ")
        assertEquals("https://HUB.example.com:18662/" to null, endpoints.checks.last())
        assertEquals("将保存为 https://hub.example.com:18662（域名）", editor.ui.value.sheet!!.hint(res))
        assertTrue(editor.ui.value.sheet!!.canSave)

        val saving = launch { editor.save() }
        repeat(3) { yield() }
        // While saving the sheet stays put: no edits, no dismissal, no second save.
        assertTrue(editor.ui.value.sheet!!.saving)
        assertTrue(editor.ui.value.busy)
        assertFalse(editor.ui.value.sheet!!.canSave)
        editor.dismissSheet()
        editor.updateAddress("https://other.example.com:18662")
        assertEquals("  https://HUB.example.com:18662/ ", editor.ui.value.sheet!!.address)

        adding.complete(Unit)
        repeat(3) { yield() }
        assertEquals(listOf("https://HUB.example.com:18662/"), endpoints.added)
        assertNull(editor.ui.value.sheet)
        assertFalse(editor.ui.value.busy)
        assertEquals(listOf("https://hub.example.com:18662"), endpoints.tested)
        assertEquals(setOf("https://hub.example.com:18662"), editor.ui.value.testing)

        testing.complete(Unit)
        saving.join()
        assertTrue(editor.ui.value.testing.isEmpty())
        assertEquals(85L, endpoints.state.value.tests.getValue("https://hub.example.com:18662").elapsedMillis)
        assertNull(editor.ui.value.message)
    }

    @Test fun editingPrefillsTheUrlChecksItAgainstItselfAndReplacesIt() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val editor = EndpointEditorState(endpoints)
        editor.startEditing(hub.url)
        assertNull("Hub addresses can only be disabled", editor.ui.value.sheet)

        editor.openActions(manual.url)
        editor.startEditing(manual.url)
        var sheet = editor.ui.value.sheet!!
        assertNull(editor.ui.value.actions)
        assertEquals(manual.url, sheet.editing)
        assertEquals(manual.url, sheet.address)
        assertEquals(manual.url to manual.url, endpoints.checks.last())
        assertTrue(sheet.unchanged)
        assertFalse(sheet.canSave)
        assertEquals("地址没有改动", sheet.hint(res))

        editor.updateAddress("https://hub.example.com:443")
        sheet = editor.ui.value.sheet!!
        assertEquals("https://hub.example.com:443" to manual.url, endpoints.checks.last())
        assertTrue(sheet.canSave)
        editor.save()
        assertEquals(listOf(manual.url to "https://hub.example.com:443"), endpoints.replaced)
        assertTrue(endpoints.added.isEmpty())
        assertNull(editor.ui.value.sheet)
        assertEquals(listOf("https://hub.example.com:443"), endpoints.tested)
    }

    @Test fun deletingNeedsConfirmationAndOnlyManualAddressesCanBeDeleted() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val editor = EndpointEditorState(endpoints)
        editor.askDelete(hub.url)
        assertNull(editor.ui.value.deleting)

        editor.openActions(manual.url)
        editor.askDelete(manual.url)
        assertEquals(manual.url, editor.ui.value.deleting)
        assertNull(editor.ui.value.actions)
        assertTrue(endpoints.removed.isEmpty())

        editor.cancelDelete()
        assertNull(editor.ui.value.deleting)
        editor.confirmDelete()
        assertTrue("Nothing waits for confirmation", endpoints.removed.isEmpty())

        editor.askDelete(manual.url)
        editor.confirmDelete()
        assertEquals(listOf(manual.url), endpoints.removed)
        assertNull(editor.ui.value.deleting)
        assertFalse(editor.ui.value.busy)
        assertEquals(listOf(hub), endpoints.state.value.entries)
    }

    @Test fun refusedChangesShowTheConnectionsMessageUntilTheNextAction() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val editor = EndpointEditorState(endpoints)
        endpoints.refusal = "至少要留一个能用的地址"
        editor.setEnabled(hub.url, false)
        assertEquals("至少要留一个能用的地址", editor.ui.value.message?.resolve(res))
        assertFalse(editor.ui.value.busy)

        // A refused save keeps the sheet open with the reason and tests nothing.
        endpoints.refusal = "这个地址已经在列表里"
        editor.startAdding()
        editor.updateAddress("https://hub.example.com:18663")
        editor.save()
        assertEquals("这个地址已经在列表里", editor.ui.value.sheet!!.error?.resolve(res))
        assertFalse(editor.ui.value.sheet!!.saving)
        assertTrue(endpoints.tested.isEmpty())
        editor.updateAddress("https://hub.example.com:18664")
        assertNull("Typing clears the refusal", editor.ui.value.sheet!!.error)
        editor.dismissSheet()
        assertNull(editor.ui.value.sheet)

        endpoints.refusal = null
        editor.moveDown(manual.url)
        assertNull("The next action replaces the message", editor.ui.value.message)

        endpoints.failure = IllegalStateException("storage unavailable")
        editor.setEnabled(hub.url, false)
        assertEquals("没有改成，请重试。", editor.ui.value.message?.resolve(res))
        endpoints.failure = null

        endpoints.testFailure = EndpointEditException(persona.core.contracts.CoreText.plain("这个地址已经不在列表里"))
        editor.test(manual.url)
        assertEquals("这个地址已经不在列表里", editor.ui.value.message?.resolve(res))
        endpoints.testFailure = IllegalStateException("socket closed")
        editor.test(manual.url)
        assertEquals("测试没有完成，请重试。", editor.ui.value.message?.resolve(res))
        assertTrue(editor.ui.value.testing.isEmpty())
    }

    @Test fun enablingCallsTheConnectionOnlyForRealChanges() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val editor = EndpointEditorState(endpoints)
        editor.setEnabled(hub.url, true)
        assertTrue("Already enabled", endpoints.toggled.isEmpty())
        editor.setEnabled(hub.url, false)
        editor.setEnabled(hub.url, true)
        editor.setEnabled(manual.url, false)
        assertEquals(listOf(hub.url to false, hub.url to true, manual.url to false), endpoints.toggled)
        assertFalse(endpoints.state.value.entries.first { it.url == manual.url }.enabled)
        assertEquals("Disabling keeps the place", urls(manual, hub), endpoints.state.value.entries.map { it.url })
    }

    @Test fun changesRunOneAtATimeInTheOrderAsked() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val gate = CompletableDeferred<Unit>()
        endpoints.changeGate = gate
        val editor = EndpointEditorState(endpoints)
        val disabling = launch { editor.setEnabled(hub.url, false) }
        yield()
        assertTrue(editor.ui.value.busy)
        val moving = launch { editor.moveDown(manual.url) }
        repeat(3) { yield() }
        assertTrue("Waits for the running change instead of being dropped", endpoints.reordered.isEmpty())
        assertEquals("The move shows at once", urls(hub, manual), editor.shownUrls())
        gate.complete(Unit)
        disabling.join()
        moving.join()
        assertEquals(listOf(hub.url to false), endpoints.toggled)
        assertEquals(listOf(urls(hub, manual)), endpoints.reordered)
        assertFalse(editor.ui.value.busy)
        assertNull(editor.ui.value.message)
    }

    @Test fun anAddressIsTestedOnceAtATime() = runBlocking {
        val endpoints = FakeEndpoints(listOf(manual, hub))
        val gate = CompletableDeferred<Unit>()
        endpoints.testGate = gate
        val editor = EndpointEditorState(endpoints)
        val first = launch { editor.test(manual.url) }
        yield()
        assertEquals(setOf(manual.url), editor.ui.value.testing)
        editor.test(manual.url)
        endpoints.state.update { it.copy(testing = setOf(hub.url)) }
        editor.test(hub.url)
        assertEquals("The connection already tests the hub address", listOf(manual.url), endpoints.tested)
        gate.complete(Unit)
        first.join()
        assertTrue(editor.ui.value.testing.isEmpty())
    }

    @Test fun moveUpAndDownTradePlacesWithTheNeighbourAndStopAtTheEnds() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        editor.moveUp(hub.url)
        editor.moveDown(v4.url)
        editor.moveUp("https://gone.example.com:18662")
        assertTrue("Nothing above the first, below the last, or outside the list", endpoints.reordered.isEmpty())
        val ends = endpointRows(res, endpoints.state.value, emptyList())
        assertEquals("上移 is off at the top", listOf(false, true, true, true), ends.map { it.canMoveUp })
        assertEquals("下移 is off at the bottom", listOf(true, true, true, false), ends.map { it.canMoveDown })

        editor.moveDown(hub.url)
        assertEquals(listOf(urls(manual, hub, lan, v4)), endpoints.reordered)
        editor.moveUp(v4.url)
        assertEquals(urls(manual, hub, v4, lan), endpoints.reordered.last())
        assertEquals(urls(manual, hub, v4, lan), endpoints.state.value.entries.map { it.url })
        assertEquals(urls(manual, hub, v4, lan), editor.shownUrls())
        assertNull("Reported, so nothing is pending", editor.ui.value.pending)
        assertTrue(endpoints.state.value.customOrder)
    }

    @Test fun aDropStoresTheDraggedOrder() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        editor.openActions(lan.url)
        editor.startDrag(lan.url)
        assertEquals(EndpointDrag(lan.url, urls(hub, manual, lan, v4)), editor.ui.value.drag)
        assertNull("Picking a row up closes its actions", editor.ui.value.actions)
        editor.dragTo(0)
        assertEquals("The list follows the drag", urls(lan, hub, manual, v4), editor.shownUrls())
        editor.startDrag(hub.url)
        assertEquals("One row at a time", lan.url, editor.ui.value.drag!!.url)
        editor.dragTo(1)
        editor.dragTo(9)
        assertEquals("Clamped at the end", urls(hub, manual, v4, lan), editor.ui.value.drag!!.urls)
        assertTrue("Nothing is stored while the finger is down", endpoints.reordered.isEmpty())

        editor.endDrag()
        assertNull(editor.ui.value.drag)
        assertEquals(listOf(urls(hub, manual, v4, lan)), endpoints.reordered)
        assertEquals(urls(hub, manual, v4, lan), editor.shownUrls())
        assertTrue(endpoints.state.value.customOrder)
        assertNull(editor.ui.value.message)
    }

    @Test fun aDropWithoutAChangeStoresNothing() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        editor.startDrag(manual.url)
        editor.dragTo(3)
        editor.dragTo(1)
        editor.endDrag()
        editor.startDrag(hub.url)
        editor.endDrag()
        assertTrue("Moved back, or picked up and put down", endpoints.reordered.isEmpty())
        assertNull(editor.ui.value.drag)
        assertNull(editor.ui.value.pending)

        editor.startDrag(hub.url)
        editor.dragTo(2)
        editor.cancelDrag()
        assertTrue("A cancelled drag stores nothing", endpoints.reordered.isEmpty())
        assertEquals(urls(hub, manual, lan, v4), editor.shownUrls())
        editor.startDrag("https://gone.example.com:18662")
        assertNull("Only rows in the list can be picked up", editor.ui.value.drag)
    }

    @Test fun aFailedCommitFallsBackToTheConnectionsOrderAndSaysWhy() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        endpoints.refusal = "没有保存成功，请重试"
        editor.startDrag(v4.url)
        editor.dragTo(0)
        editor.endDrag()
        assertEquals("没有保存成功，请重试", editor.ui.value.message?.resolve(res))
        assertNull(editor.ui.value.pending)
        assertEquals(urls(hub, manual, lan, v4), editor.shownUrls())

        endpoints.refusal = null
        endpoints.failure = IllegalStateException("disk full")
        editor.moveDown(hub.url)
        assertEquals("顺序没有保存，请重试。", editor.ui.value.message?.resolve(res))
        assertEquals(urls(hub, manual, lan, v4), editor.shownUrls())
        assertFalse(editor.ui.value.busy)
        assertFalse(endpoints.state.value.customOrder)
    }

    @Test fun theDroppedOrderHoldsUntilTheConnectionReportsIt() = runBlocking {
        val endpoints = FakeEndpoints(four)
        endpoints.reportLater = true
        val editor = EndpointEditorState(endpoints)
        editor.startDrag(v4.url)
        editor.dragTo(1)
        val dropping = launch { editor.endDrag() }
        repeat(3) { yield() }
        assertEquals(listOf(urls(hub, v4, manual, lan)), endpoints.reordered)
        assertEquals("The connection still reports the old order", urls(hub, manual, lan, v4), endpoints.state.value.entries.map { it.url })
        assertEquals("The list does not flick back meanwhile", urls(hub, v4, manual, lan), editor.shownUrls())
        // Whatever else the connection reports first (a test starting) does not end the wait.
        endpoints.state.update { it.copy(testing = setOf(hub.url)) }
        repeat(3) { yield() }
        assertNotNull(editor.ui.value.pending)
        assertEquals(urls(hub, v4, manual, lan), editor.shownUrls())

        endpoints.report()
        dropping.join()
        assertNull(editor.ui.value.pending)
        assertEquals(urls(hub, v4, manual, lan), editor.shownUrls())
    }

    @Test fun anOrderTheConnectionNeverReportsGivesWayAfterAWhile() = runBlocking {
        val endpoints = FakeEndpoints(four)
        endpoints.reportLater = true
        val editor = EndpointEditorState(endpoints, reportTimeoutMillis = 50)
        editor.moveDown(hub.url)
        assertEquals(listOf(urls(manual, hub, lan, v4)), endpoints.reordered)
        assertNull(editor.ui.value.pending)
        assertEquals("The list follows the connection again", urls(hub, manual, lan, v4), editor.shownUrls())
    }

    @Test fun aNewerOrderOutlivesTheAnswerToAnOlderOne() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val gate = CompletableDeferred<Unit>()
        endpoints.reorderGate = gate
        val editor = EndpointEditorState(endpoints)
        val first = launch { editor.moveDown(hub.url) }
        yield()
        editor.startDrag(v4.url)
        assertEquals("A drag starts from the order shown", urls(manual, hub, lan, v4), editor.ui.value.drag!!.urls)
        editor.dragTo(0)
        val second = launch { editor.endDrag() }
        yield()
        gate.complete(Unit)
        first.join()
        second.join()
        assertEquals(listOf(urls(manual, hub, lan, v4), urls(v4, manual, hub, lan)), endpoints.reordered)
        assertEquals(urls(v4, manual, hub, lan), editor.shownUrls())
        assertNull(editor.ui.value.pending)
    }

    @Test fun anAddressTheHubAddsDuringADragGoesToTheEnd() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        editor.startDrag(v4.url)
        editor.dragTo(0)
        val added = EndpointEntry("https://[2001:db8::2]:18662", "ipv6", EndpointSource.Hub)
        endpoints.state.update { it.copy(entries = listOf(added) + it.entries.filterNot { e -> e.url == lan.url }) }
        assertEquals("New ones at the end, gone ones gone", urls(v4, hub, manual, added), editor.shownUrls())
        editor.endDrag()
        assertEquals(listOf(urls(v4, hub, manual, added)), endpoints.reordered)
    }

    @Test fun resettingTheOrderNeedsConfirmation() = runBlocking {
        val endpoints = FakeEndpoints(four)
        val editor = EndpointEditorState(endpoints)
        editor.askResetOrder()
        assertFalse("Nothing to reset while the default rule applies", editor.ui.value.resetting)

        editor.moveDown(hub.url)
        assertTrue(endpoints.state.value.customOrder)
        editor.openActions(manual.url)
        editor.askResetOrder()
        assertTrue(editor.ui.value.resetting)
        assertNull(editor.ui.value.actions)
        editor.cancelResetOrder()
        assertFalse(editor.ui.value.resetting)
        editor.confirmResetOrder()
        assertEquals("Cancelled: nothing waits for confirmation", 0, endpoints.resets)

        editor.askResetOrder()
        editor.confirmResetOrder()
        assertEquals(1, endpoints.resets)
        assertFalse(editor.ui.value.resetting)
        assertFalse(endpoints.state.value.customOrder)
        assertEquals(urls(hub, manual, lan, v4), editor.shownUrls())
        editor.askResetOrder()
        assertFalse("Back to the default rule: nothing to reset", editor.ui.value.resetting)
    }

    private class FakeEndpoints(entries: List<EndpointEntry> = emptyList()) : ConnectionEndpoints {
        /** The default rule's order in this fake: the order the entries were given in. */
        private val defaults = entries.map { it.url }
        override val state = MutableStateFlow(ConnectionEndpointsState(entries = entries, pinned = true))
        val checks = mutableListOf<Pair<String, String?>>()
        val added = mutableListOf<String>()
        val replaced = mutableListOf<Pair<String, String>>()
        val removed = mutableListOf<String>()
        val toggled = mutableListOf<Pair<String, Boolean>>()
        val reordered = mutableListOf<List<String>>()
        var resets = 0
        val tested = mutableListOf<String>()
        var refusal: String? = null
        var failure: Exception? = null
        var testFailure: Exception? = null
        var addGate: CompletableDeferred<Unit>? = null
        var changeGate: CompletableDeferred<Unit>? = null
        var reorderGate: CompletableDeferred<Unit>? = null
        var testGate: CompletableDeferred<Unit>? = null
        /** Stored orders show up in [state] only on [report], as the real connection's do a moment later. */
        var reportLater = false
        private var unreported: List<String>? = null

        override fun check(address: String, replacing: String?): EndpointCheck {
            checks += address to replacing
            return if (address.startsWith("https://", ignoreCase = true) || address.startsWith("http://", ignoreCase = true))
                EndpointCheck.Valid(canonical(address), "name") else EndpointCheck.Invalid(persona.core.contracts.CoreText.plain("地址要以 https:// 或 http:// 开头"))
        }

        override suspend fun add(address: String): String {
            addGate?.await()
            refuse()
            added += address
            return canonical(address).also { url -> state.update { it.copy(entries = it.entries + EndpointEntry(url, "name", EndpointSource.Manual)) } }
        }

        override suspend fun replace(url: String, address: String): String {
            refuse()
            replaced += url to address
            return canonical(address).also { next -> state.update { s -> s.copy(entries = s.entries.map { if (it.url == url) it.copy(url = next) else it }) } }
        }

        override suspend fun remove(url: String) {
            refuse()
            removed += url
            state.update { s -> s.copy(entries = s.entries.filterNot { it.url == url }) }
        }

        override suspend fun setEnabled(url: String, enabled: Boolean) {
            changeGate?.await()
            refuse()
            toggled += url to enabled
            state.update { s -> s.copy(entries = s.entries.map { if (it.url == url) it.copy(enabled = enabled) else it }) }
        }

        /** As the connection does: the urls given first, the entries they leave out after them in their order. */
        override suspend fun reorder(urls: List<String>) {
            reorderGate?.await()
            refuse()
            reordered += urls
            val shown = state.value.entries.map { it.url }
            val wanted = urls.filter { it in shown }.distinct()
            val order = wanted + shown.filter { it !in wanted }
            if (reportLater) unreported = order else publish(order)
        }

        fun report() {
            unreported?.let(::publish)
            unreported = null
        }

        private fun publish(order: List<String>) = state.update { s ->
            s.copy(entries = order.mapNotNull { url -> s.entries.firstOrNull { it.url == url } }, customOrder = true)
        }

        override suspend fun resetOrder() {
            refuse()
            resets++
            state.update { s -> s.copy(entries = s.entries.sortedBy { defaults.indexOf(it.url).takeIf { i -> i >= 0 } ?: Int.MAX_VALUE }, customOrder = false) }
        }

        override suspend fun test(url: String): EndpointTestResult {
            tested += url
            testFailure?.let { throw it }
            testGate?.await()
            val result = EndpointTestResult(url, passed = true, result = persona.core.contracts.CoreText.plain("已通过身份校验"), elapsedMillis = 85, testedAt = "2026-11-01T02:03:04Z")
            state.update { it.copy(tests = it.tests + (url to result)) }
            return result
        }

        private fun refuse() {
            refusal?.let { throw EndpointEditException(persona.core.contracts.CoreText.plain(it)) }
            failure?.let { throw it }
        }

        private fun canonical(address: String) = address.lowercase().trimEnd('/')
    }
}

/** Robolectric for the resources: the texts are the Chinese ones (robolectric.properties). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class EndpointTextTest {
    private val res = RuntimeEnvironment.getApplication().resources
    private val zone = ZoneId.of("Asia/Shanghai")
    private val today = LocalDate.of(2026, 11, 1)
    private val url = "https://[2001:db8::1]:18662"

    @Test fun aTestIsLabelledAsASingleSampleWithItsElapsedTime() {
        val passed = EndpointTestResult(url, passed = true, result = persona.core.contracts.CoreText.plain("已通过身份校验"), elapsedMillis = 85, testedAt = "2026-11-01T02:03:04Z")
        assertEquals(EndpointOutcome("已通过身份校验，85\u00A0毫秒（单次）", failed = false, label = "测试 10:03:04"), endpointOutcome(res, passed, null, zone, today))
        val timedOut = passed.copy(passed = false, result = persona.core.contracts.CoreText.plain("连接超时"), elapsedMillis = 8003, testedAt = "2026-10-31T01:00:00Z")
        assertEquals(EndpointOutcome("连接超时，8003\u00A0毫秒（单次）", failed = true, label = "测试 10月31日 09:00:00"), endpointOutcome(res, timedOut, null, zone, today))
    }

    @Test fun theNewerOfTestAndAutomaticProbeIsShown() {
        val probe = ConnectionCandidate(url, "ipv6", persona.core.contracts.CoreText.plain("已通过身份校验"), "2026-11-01T02:00:00Z", probeDurationMillis = 40, sampleCount = 1)
        assertEquals(EndpointOutcome("已通过身份校验，40\u00A0毫秒（单次）", failed = false, label = "自动探测 10:00:00"), endpointOutcome(res, null, probe, zone, today))
        val test = EndpointTestResult(url, passed = false, result = persona.core.contracts.CoreText.plain("连接超时"), elapsedMillis = 5000, testedAt = "2026-11-01T02:03:04Z")
        assertEquals("测试 10:03:04", endpointOutcome(res, test, probe, zone, today)!!.label)
        val later = probe.copy(result = persona.core.contracts.CoreText.plain("Timeout"), checkedAt = "2026-11-01T02:05:00Z", probeDurationMillis = 5001)
        assertEquals(EndpointOutcome("Timeout，5001\u00A0毫秒（单次）", failed = true, label = "自动探测 10:05:00"), endpointOutcome(res, test, later, zone, today))
        assertEquals("A test wins when the probe time is unreadable", "测试", endpointOutcome(res, test, later.copy(checkedAt = "刚才"), zone, today)!!.label.take(2))
        assertNull("Never probed", endpointOutcome(res, null, ConnectionCandidate(url, "ipv6", persona.core.contracts.CoreText.plain("未实测")), zone, today))
        assertEquals("已通过身份校验，40\u00A0毫秒（样本数\u00A012）",
            endpointOutcome(res, null, probe.copy(sampleCount = 12), zone, today)!!.text)
        assertEquals("已通过身份校验",
            endpointOutcome(res, null, probe.copy(probeDurationMillis = null), zone, today)!!.text)
    }

    @Test fun labelsSayWhatKindOfAddressAndWhereItCameFrom() {
        // Built at runtime: the privacy check rejects private dotted quads in the repository.
        val lan = "http://" + listOf(10, 0, 0, 2).joinToString(".") + ":18660"
        assertEquals("内网，手动添加", EndpointEntry(lan, "lan", EndpointSource.Manual).origin(res))
        assertEquals("公网 IPv6，来自 Hub", EndpointEntry(url, "ipv6", EndpointSource.Hub).origin(res))
        assertEquals("域名，配对时添加", EndpointEntry("https://hub.example.com:18662", "name", EndpointSource.Pairing).origin(res))
        assertEquals("公网 IPv4，来自 Hub", EndpointEntry("https://192.0.2.10:18662", "ipv4", EndpointSource.Hub).origin(res))
        assertEquals("未知类型，手动添加", EndpointEntry("https://hub.example.com:18662", "unknown", EndpointSource.Manual).origin(res))
    }

    @Test fun rowsMarkTheAddressInUseDisabledOnesAndFailures() {
        val name = EndpointEntry("https://hub.example.com:18662", "name", EndpointSource.Pairing, active = true)
        val ipv6 = EndpointEntry(url, "ipv6", EndpointSource.Hub)
        val ipv4 = EndpointEntry("https://192.0.2.10:18662", "ipv4", EndpointSource.Manual, enabled = false)
        val routes = ConnectionEndpointsState(
            entries = listOf(name, ipv6, ipv4), activeUrl = name.url, pinned = true,
            tests = mapOf(ipv6.url to EndpointTestResult(ipv6.url, false, persona.core.contracts.CoreText.plain("连接超时"), 5000, "2026-11-01T02:03:04Z")),
            testing = setOf(ipv4.url),
        )
        val rows = endpointRows(res, routes, emptyList(), zone = zone, today = today)
        assertEquals(listOf(name.url, ipv6.url, ipv4.url), rows.map { it.url })
        assertEquals(listOf(EndpointTag.Active), rows[0].tags)
        assertEquals(Status.Unread, rows[0].status)
        assertEquals(Status.NeedsYou, rows[1].status)
        assertEquals("连接超时，5000\u00A0毫秒（单次）", rows[1].outcome!!.text)
        assertEquals("测试 10:03:04", rows[1].outcome!!.label)
        assertEquals(listOf(EndpointTag.Disabled), rows[2].tags)
        assertEquals(Status.Idle, rows[2].status)
        assertTrue(rows[2].testing)
        assertFalse(rows[1].testing)
        assertTrue("A test started here counts before the connection reports it",
            endpointRows(res, routes, emptyList(), testing = setOf(ipv6.url), zone = zone, today = today)[1].testing)
        val probed = endpointRows(res, routes.copy(tests = emptyMap()), listOf(ConnectionCandidate(ipv6.url, "ipv6", persona.core.contracts.CoreText.plain("Timeout"), "2026-11-01T02:00:00Z", 5000, 1)), zone = zone, today = today)
        assertEquals(Status.NeedsYou, probed[1].status)
        assertNull(probed[0].outcome)
    }

    @Test fun disabledRowsKeepTheirPlaceAndOnlyTheOnesTriedAreNumbered() {
        val first = EndpointEntry(url, "ipv6", EndpointSource.Hub)
        val off = EndpointEntry("https://hub.example.com:18662", "name", EndpointSource.Pairing, enabled = false)
        val second = EndpointEntry("http://127.0.0.1:18660", "lan", EndpointSource.Manual)
        val alsoOff = EndpointEntry("https://192.0.2.10:18662", "ipv4", EndpointSource.Hub, enabled = false)
        val third = EndpointEntry("https://192.0.2.11:18662", "ipv4", EndpointSource.Hub)
        val entries = listOf(first, off, second, alsoOff, third)
        val rows = endpointRows(res, ConnectionEndpointsState(entries = entries), emptyList(), zone = zone, today = today)
        assertEquals("In the connection's order, disabled ones in place", entries.map { it.url }, rows.map { it.url })
        assertEquals(listOf(1, null, 2, null, 3), rows.map { it.rank })
        assertEquals(listOf(EndpointTag.Disabled), rows[1].tags)
        assertTrue(rows[2].tags.isEmpty())
        assertEquals("Disabled rows can still move", listOf(true, true), listOf(rows[1].canMoveUp, rows[1].canMoveDown))
    }

    @Test fun theListShowsADragOrAnOrderOnItsWayBeforeTheConnectionsOwn() {
        val a = EndpointEntry(url, "ipv6", EndpointSource.Hub)
        val b = EndpointEntry("https://hub.example.com:18662", "name", EndpointSource.Pairing)
        val c = EndpointEntry("http://127.0.0.1:18660", "lan", EndpointSource.Manual, enabled = false)
        val entries = listOf(a, b, c)
        assertEquals(listOf(c, a, b), arranged(entries, listOf(c.url, a.url, b.url)))
        assertEquals("Gone urls are skipped, unlisted entries follow in order", listOf(c, a, b),
            arranged(entries, listOf(c.url, "https://gone.example.com:18662", c.url)))
        assertEquals(entries, shownEntries(entries, EndpointEditorUi()))
        val pending = EndpointEditorUi(pending = PendingOrder(listOf(b.url, a.url, c.url), 1))
        assertEquals(listOf(b, a, c), shownEntries(entries, pending))
        assertEquals("A drag wins over an order on its way", listOf(c, b, a),
            shownEntries(entries, pending.copy(drag = EndpointDrag(c.url, listOf(c.url, b.url, a.url)))))
        val rows = endpointRows(res, ConnectionEndpointsState(entries = entries), emptyList(), entries = shownEntries(entries, pending), zone = zone, today = today)
        assertEquals(listOf(b.url, a.url, c.url), rows.map { it.url })
        assertEquals(listOf(1, 2, null), rows.map { it.rank })
        assertEquals(listOf(false, true, true), rows.map { it.canMoveUp })
    }

    @Test fun currentRouteFallsBackToTheObservedPath() {
        val entry = EndpointEntry(url, "ipv6", EndpointSource.Hub, active = true)
        val routes = ConnectionEndpointsState(entries = listOf(entry), activeUrl = url)
        assertEquals(ActiveRoute(url, "公网 IPv6"), activeRoute(res, routes, null, ConnectionState.Online))
        val observed = "name · https://hub.example.com:18662"
        assertEquals(ActiveRoute("https://hub.example.com:18662", "域名"), activeRoute(res, ConnectionEndpointsState(), observed, ConnectionState.Online))
        assertEquals(ActiveRoute(null, "正在选路…"), activeRoute(res, ConnectionEndpointsState(), null, ConnectionState.Connecting))
        assertEquals(ActiveRoute(null, "未连接"), activeRoute(res, ConnectionEndpointsState(), null, ConnectionState.WaitingForNetwork))
    }
}

class EndpointDragMathTest {
    private val order = listOf("a", "b", "c", "d")
    /** Rows of 100 px laid out from 0, no gaps. */
    private val rows = order.mapIndexed { index, url -> RowSpan(url, index * 100, 100) }

    @Test fun aRowMovesDownOnceItsMiddlePassesTheNextRowsMiddle() {
        assertNull(dragMove(order, rows, "b", 100f))
        assertNull("Its middle is still above c's", dragMove(order, rows, "b", 199f))
        assertEquals(2, dragMove(order, rows, "b", 201f))
        assertEquals("Past two rows at once", 3, dragMove(order, rows, "b", 301f))
    }

    @Test fun aRowMovesUpOnceItsMiddlePassesThePreviousRowsMiddle() {
        assertNull(dragMove(order, rows, "c", 101f))
        assertEquals(1, dragMove(order, rows, "c", 99f))
        assertEquals(0, dragMove(order, rows, "c", -1f))
    }

    @Test fun itStopsAtTheEnds() {
        assertEquals(3, dragMove(order, rows, "a", 5000f))
        assertEquals(0, dragMove(order, rows, "d", -5000f))
        assertNull("Nowhere further down", dragMove(order, rows, "d", 5000f))
        assertNull("Nowhere further up", dragMove(order, rows, "a", -5000f))
    }

    @Test fun afterASwapItTakesItsOwnHeightToSwapBack() {
        // b went below c: c is laid out at 100 and b at 200, b still drawn where the finger is (201).
        val swapped = listOf("a", "c", "b", "d")
        val laidOut = swapped.mapIndexed { index, url -> RowSpan(url, index * 100, 100) }
        assertNull(dragMove(swapped, laidOut, "b", 201f))
        assertNull(dragMove(swapped, laidOut, "b", 101f))
        assertEquals(1, dragMove(swapped, laidOut, "b", 99f))
    }

    @Test fun rowsOfDifferentHeightsDoNotFlickerBetweenEachOther() {
        // A short row over a tall one: it passes when its middle passes the tall row's middle...
        assertNull(dragMove(listOf("a", "b"), listOf(RowSpan("a", 0, 60), RowSpan("b", 60, 180)), "a", 119f))
        assertEquals(1, dragMove(listOf("a", "b"), listOf(RowSpan("a", 0, 60), RowSpan("b", 60, 180)), "a", 121f))
        // ...and once laid out below it, it stays there until it goes back past that middle.
        val after = listOf(RowSpan("b", 0, 180), RowSpan("a", 180, 60))
        assertNull(dragMove(listOf("b", "a"), after, "a", 121f))
        assertEquals(0, dragMove(listOf("b", "a"), after, "a", 59f))
    }

    @Test fun nothingMovesWhileTheLayoutStillShowsAnOlderOrder() {
        assertNull(dragMove(listOf("a", "c", "b", "d"), rows, "b", 301f))
        assertNull("Not on screen", dragMove(order, rows.drop(2), "b", 0f))
    }

    @Test fun onlyTheRowsOnScreenCount() {
        val visible = rows.drop(1)
        assertEquals("An index of the whole order", 1, dragMove(order, visible, "c", 99f))
        assertNull("Rows above the screen are out of reach until they scroll in", dragMove(order, visible, "b", -5000f))
    }

    @Test fun theListScrollsOnlyNearAnEdgeTowardsRowsOutOfSight() {
        // A 1000 px viewport, a 100 px band, 2000 px a second at the edge; the dragged row is 100 px tall.
        fun velocity(top: Float, direction: Int, groupTop: Float? = null, groupBottom: Float? = null) =
            edgeScrollVelocity(top, top + 100f, 0f, 1000f, 100f, 2000f, direction, groupTop, groupBottom)
        assertEquals(0f, velocity(400f, 1), 0f)
        assertEquals("Halfway into the bottom band", 1000f, velocity(850f, 1), 0.01f)
        assertEquals("Full speed at the edge and past it", 2000f, velocity(990f, 1), 0.01f)
        assertEquals("Only the way it was last moved", 0f, velocity(850f, -1), 0f)
        assertEquals("Not when just picked up", 0f, velocity(850f, 0), 0f)
        assertEquals(-1000f, velocity(50f, -1), 0.01f)
        assertEquals(-2000f, velocity(-80f, -1), 0.01f)
        assertEquals("The last row is already in sight", 0f, velocity(850f, 1, groupBottom = 980f), 0f)
        assertEquals(1000f, velocity(850f, 1, groupBottom = 1200f), 0.01f)
        assertEquals("The first row is already in sight", 0f, velocity(50f, -1, groupTop = 10f), 0f)
        assertEquals(0f, edgeScrollVelocity(850f, 950f, 0f, 1000f, 0f, 2000f, 1, null, null), 0f)
    }
}
