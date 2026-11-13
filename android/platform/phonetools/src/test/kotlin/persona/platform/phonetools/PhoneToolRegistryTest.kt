package persona.platform.phonetools

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.*
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class PhoneToolRegistryTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private fun text(result: PhoneToolResult) = result.content.single().jsonObject.getValue("text").jsonPrimitive.content

    @Test fun registrationAndStaleCallsRespectAvailabilityPermissionsAndSources() = runTest {
        var online = true
        var foreground = true
        var permission = true
        var sources = PhoneSource.entries.toSet()
        var executions = 0
        val registry = PhoneToolRegistry({ sources }, { online }, { foreground }, { _, _ -> permission },
            { error("Reads must not ask for confirmation") }, { _, _ -> executions++; PhoneToolResult.text("ok") })
        assertEquals(12, registry.enabledSpecs().size)
        assertFalse(registry.all.any { it.name.startsWith("phone_reminders_") || it.name == "phone_health_write_sample" })
        assertTrue(registry.all.any { it.name == "phone_health_summary" })
        assertTrue(registry.all.all { it.inputSchema["type"]?.jsonPrimitive?.content == "object" })
        foreground = false
        assertFalse(registry.enabledSpecs().any { it.name == "phone_clipboard_get" || it.name == "phone_location_current" })
        val background = registry.call(PhoneToolCall("background", "phone_clipboard_get", buildJsonObject {}))
        assertTrue(background.isError)
        assertTrue(text(background).contains("open on screen"))
        sources = emptySet()
        assertTrue(registry.enabledSpecs().isEmpty())
        val off = registry.call(PhoneToolCall("disabled", "phone_device_info", buildJsonObject {}))
        assertTrue(off.isError)
        assertEquals("“设备信息” 已在手机上关闭 / The device data source is turned off on the phone. " +
            "The user can turn it on in Persona › Settings › Connectors › This phone (Persona › 设置 › 连接器 › 这台手机).", text(off))
        sources = PhoneSource.entries.toSet()
        permission = false
        assertTrue(registry.enabledSpecs().isEmpty())
        assertTrue(text(registry.call(PhoneToolCall("denied", "phone_contacts_search", buildJsonObject {}))).startsWith("没有权限：通讯录"))
        permission = true
        online = false
        assertTrue(registry.enabledSpecs().isEmpty())
        online = true
        val unknown = registry.call(PhoneToolCall("unknown", "phone_teleport", buildJsonObject {}))
        assertTrue(unknown.isError)
        assertTrue(text(unknown).startsWith("未知工具 / Unknown tool: phone_teleport."))
        // Out-of-range numbers and unknown arguments are no reason to refuse (iOS clamps and ignores them).
        assertFalse(registry.call(PhoneToolCall("clamped", "phone_contacts_search",
            buildJsonObject { put("query", "test"); put("limit", 0); put("extra", true) })).isError)
        assertFalse(registry.call(PhoneToolCall("valid", "phone_device_info", buildJsonObject {})).isError)
        assertEquals(2, executions)
        // The same call delivered twice runs once.
        assertFalse(registry.call(PhoneToolCall("valid", "phone_device_info", buildJsonObject {})).isError)
        assertEquals(2, executions)
    }

    @Test fun calendarWritesRequireOneLocalDecisionAndRecheckStateAfterApproval() = runTest {
        var allow = false
        var enabled = true
        var revokeOnConfirm = false
        var confirmations = 0
        var executions = 0
        val registry = PhoneToolRegistry({ if (enabled) setOf(PhoneSource.Calendar) else emptySet() }, { true }, { true }, { _, _ -> true },
            { render ->
                val summary = render(res)
                assertTrue(summary.startsWith("新建日程「测试日程」\n")); assertTrue(summary.contains(" → "))
                confirmations++
                if (revokeOnConfirm) enabled = false
                if (allow) PhoneConfirmation.Allowed else PhoneConfirmation.Denied
            }, { _, _ -> executions++; PhoneToolResult.text("created") })
        val args = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val refused = registry.call(PhoneToolCall("refused", "phone_calendar_create_event", args))
        assertTrue(refused.isError)
        // Word for word what the iOS app answers.
        assertEquals("The user declined on the phone.", text(refused))
        assertEquals(0, executions)
        allow = true
        val call = PhoneToolCall("accepted", "phone_calendar_create_event", args)
        assertFalse(registry.call(call).isError)
        assertFalse(registry.call(call).isError)
        assertEquals(1, executions)
        assertEquals(2, confirmations)
        revokeOnConfirm = true
        assertTrue(registry.call(PhoneToolCall("revoked", call.name, args)).isError)
        assertEquals(1, executions)
        enabled = true
        // Arguments that cannot be shown are refused without asking anyone.
        val invalid = registry.call(PhoneToolCall("invalid", call.name, JsonObject(args + ("end" to JsonPrimitive("2026-11-01T09:00:00+08:00")))))
        assertTrue(invalid.isError)
        assertEquals("参数错误 / Invalid argument: end must be after start", text(invalid))
        assertEquals(3, confirmations)
        assertTrue(text(registry.call(PhoneToolCall("untitled", call.name, JsonObject(args - "title")))).contains("title is required"))
        assertEquals(3, confirmations)
    }

    /** Only Deny is a refusal (#1875): not shown, dismissed, or withdrawn must not read as "the user declined". */
    @Test fun aConfirmationThatDidNotAnswerAllowIsNotADecline() = runTest {
        var outcome = PhoneConfirmation.NotShown
        var executions = 0
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            { outcome }, { _, _ -> executions++; PhoneToolResult.text("created") })
        val args = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val notShown = registry.call(PhoneToolCall("not-shown", "phone_calendar_create_event", args))
        assertTrue(notShown.isError)
        assertFalse(text(notShown).contains(PhoneToolText.DECLINED))
        assertTrue(text(notShown).contains("could not be shown on the phone"))
        assertTrue(text(notShown).contains("Android Settings › Apps › Persona › Notifications"))
        outcome = PhoneConfirmation.Dismissed
        val dismissed = registry.call(PhoneToolCall("dismissed", "phone_calendar_create_event", args))
        assertTrue(dismissed.isError)
        assertFalse(text(dismissed).contains(PhoneToolText.DECLINED))
        assertTrue(text(dismissed).contains("dismissed on the phone"))
        assertTrue(text(dismissed).contains("This is not a refusal"))
        outcome = PhoneConfirmation.Withdrawn
        val withdrawn = registry.call(PhoneToolCall("withdrawn", "phone_calendar_create_event", args))
        assertEquals(text(PhoneToolText.interrupted()), text(withdrawn))
        outcome = PhoneConfirmation.Denied
        assertEquals(PhoneToolText.DECLINED, text(registry.call(PhoneToolCall("denied", "phone_calendar_create_event", args))))
        assertEquals(0, executions)
        // the same call delivered again gets the same answer, without asking again
        outcome = PhoneConfirmation.Allowed
        assertEquals(text(notShown), text(registry.call(PhoneToolCall("not-shown", "phone_calendar_create_event", args))))
        assertEquals(0, executions)
    }

    @Test fun refusalsNameTheCurrentPageAndTheAndroidSetting() = runTest {
        var permitted = false
        val registry = PhoneToolRegistry({ PhoneSource.entries.toSet() }, { true }, { true }, { _, _ -> permitted },
            { PhoneConfirmation.Allowed }, { _, _ -> throw PhoneToolFailure(PhoneToolFailure.Kind.Denied, "通知 / Notifications.") })
        val contacts = text(registry.call(PhoneToolCall("contacts", "phone_contacts_search", buildJsonObject {})))
        // The page that used to be Settings › Phone data is now Settings › Connectors › This phone.
        assertFalse(contacts.contains("手机数据") || contacts.contains("Phone data"))
        assertTrue(contacts.contains("Persona › 设置 › 连接器 › 这台手机"))
        assertTrue(contacts.contains("Persona › Settings › Connectors › This phone"))
        // Android stops asking after two refusals: say which system permission to allow, and where.
        assertTrue(contacts.contains("系统设置 › 应用 › Persona › 权限 里允许「通讯录」"))
        assertTrue(contacts.contains("allow Contacts in Android Settings › Apps › Persona › Permissions"))
        val photos = text(registry.call(PhoneToolCall("photos", "phone_photos_recent", buildJsonObject {})))
        assertTrue(photos.contains("允许「照片和视频」") && photos.contains("allow Photos and videos"))
        // A refusal raised while running (notifications turned off) gets the same directions.
        permitted = true
        val notify = text(registry.call(PhoneToolCall("notify", "phone_notify", buildJsonObject {})))
        assertTrue(notify.isNotBlank())
        val calendar = text(registry.call(PhoneToolCall("calendar", "phone_calendar_events", buildJsonObject {})))
        assertTrue(calendar.contains("允许「日历」") && calendar.contains("allow Calendar"))
        val location = text(registry.call(PhoneToolCall("location", "phone_location_current", buildJsonObject {})))
        assertTrue(location.contains("允许「位置信息」") && location.contains("allow Location"))
        // Android 12 and older call the photos permission storage.
        val sdk = PhoneToolText.photosSdk
        try {
            PhoneToolText.photosSdk = { 32 }
            assertTrue(PhoneToolText.grantHint(PhoneSource.Photos).contains("allow Files and media"))
        } finally { PhoneToolText.photosSdk = sdk }
        assertTrue(PhoneToolText.grantHint(null).contains("tap Allow for that data"))
        // No Allow button exists for sources without a permission.
        assertFalse(PhoneToolText.grantHint(PhoneSource.Clipboard).contains("授权"))
        assertFalse(PhoneToolText.grantHint(PhoneSource.Clipboard).contains("Allow"))
        val health = PhoneToolText.grantHint(PhoneSource.Health)
        assertTrue(health.contains("Health Connect › App permissions › Persona"))
        assertFalse(PhoneToolText.grantHint(PhoneSource.Device).contains("系统设置"))
    }

    /**
     * #4456: while a confirmed write waits for the user, reads run concurrently to completion instead of
     * being serialised behind it. The hub owns how long the write may wait.
     */
    @Test fun aPendingConfirmationDoesNotBlockConcurrentReads() = runTest {
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val confirmationGate = CompletableDeferred<PhoneConfirmation>()
        var readExecutions = 0
        val registry = PhoneToolRegistry({ PhoneSource.entries.toSet() }, { true }, { true }, { _, _ -> true },
            { confirmationGate.await() }, { name, _ ->
                if (name == "phone_device_info") readExecutions++
                PhoneToolResult.text("ok")
            })
        val write = async { registry.call(PhoneToolCall("write-1", "phone_calendar_create_event", writeArgs)) }
        runCurrent()
        assertTrue(registry.writesInFlight.value.isEmpty()) // still in confirm, not execute
        // Reads finish even though the write is still waiting.
        val readOne = registry.call(PhoneToolCall("read-1", "phone_device_info", buildJsonObject {}))
        val readTwo = registry.call(PhoneToolCall("read-2", "phone_device_info", buildJsonObject {}))
        assertFalse(readOne.isError); assertFalse(readTwo.isError)
        assertEquals(2, readExecutions)
        assertFalse(write.isCompleted)
        // Only the user's answer lets the write proceed.
        confirmationGate.complete(PhoneConfirmation.Denied)
        assertTrue(write.await().isError)
    }

    /**
     * #4456: a slow read does not block another read; they run concurrently.
     */
    @Test fun aSlowReadDoesNotBlockAnotherRead() = runTest {
        val slowGate = CompletableDeferred<Unit>()
        var fastRan = false
        val registry = PhoneToolRegistry({ PhoneSource.entries.toSet() }, { true }, { true }, { _, _ -> true },
            { error("no confirmation in this test") }, { name, _ ->
                if (name == "phone_device_info") { slowGate.await(); PhoneToolResult.text("slow") }
                else { fastRan = true; PhoneToolResult.text("fast") }
            })
        val slow = async { registry.call(PhoneToolCall("slow", "phone_device_info", buildJsonObject {})) }
        runCurrent()
        assertFalse(slow.isCompleted)
        // phone_calendar_events is a different tool, runs without waiting for the slow read.
        val fast = registry.call(PhoneToolCall("fast", "phone_calendar_events", buildJsonObject {}))
        assertFalse(fast.isError)
        assertTrue(fastRan)
        slowGate.complete(Unit)
        assertFalse(slow.await().isError)
    }

    /**
     * #4456: two confirmed writes both reach the confirmation gate, and a duplicate call_id shares the in-flight
     * leader's outcome instead of executing a second time.
     */
    @Test fun aRepeatedCallIdSharesTheInflightResultInsteadOfReexecuting() = runTest {
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val confirmationGate = CompletableDeferred<PhoneConfirmation>()
        var executions = 0
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            { confirmationGate.await() }, { _, _ -> executions++; PhoneToolResult.text("created") })
        val call = PhoneToolCall("same-id", "phone_calendar_create_event", writeArgs)
        val leader = async { registry.call(call) }
        runCurrent()
        val follower = async { registry.call(call) }
        runCurrent()
        assertFalse(leader.isCompleted)
        assertFalse(follower.isCompleted)
        confirmationGate.complete(PhoneConfirmation.Allowed)
        assertFalse(leader.await().isError)
        assertFalse(follower.await().isError)
        // Only one execution happened, shared between leader and follower.
        assertEquals(1, executions)
        // A third delivery replays the cached result with no further execution.
        assertFalse(registry.call(call).isError)
        assertEquals(1, executions)
    }

    /**
     * #4456: a confirmed write whose local execute times out returns `uncertain`, not an ordinary timeout that
     * invites the model to immediately retry. The same call_id later returns the cached uncertain result.
     */
    @Test fun aConfirmedWriteTimeoutIsUncertainNotAnInvitationToRetry() = runTest {
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        var executions = 0
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            { PhoneConfirmation.Allowed }, { _, _ ->
                executions++
                // Hang past the local execution timeout: the phone may or may not have saved the write.
                kotlinx.coroutines.delay(60_000)
                PhoneToolResult.text("created")
            })
        val result = registry.call(PhoneToolCall("uncertain-write", "phone_calendar_create_event", writeArgs))
        assertTrue(result.isError)
        val body = text(result)
        assertTrue(body.contains("不确定") || body.contains("did not confirm"))
        // Crucially: the uncertain result must not invite an automatic retry.
        assertFalse(body.contains("retry") && !body.contains("Do not retry"))
        assertEquals(1, executions)
        // Re-delivery of the same call_id returns the cached uncertain result without re-executing.
        val replay = registry.call(PhoneToolCall("uncertain-write", "phone_calendar_create_event", writeArgs))
        assertEquals(body, text(replay))
        assertEquals(1, executions)
    }

    /**
     * #4456: a confirmed write whose execute raises an exception mid-write also returns `uncertain`, so the
     * model does not conclude "not saved" from a generic failure.
     */
    @Test fun aConfirmedWriteFailureIsUncertain() = runTest {
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val failures = listOf(
            PhoneToolFailure(PhoneToolFailure.Kind.Denied, "write failed"),
            IllegalArgumentException("invalid after approval"),
            SecurityException("permission revoked after approval"),
            IllegalStateException("socket closed mid-write"),
            RuntimeException("unexpected mid-write"),
        )
        var failure: Exception = failures.first()
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            { PhoneConfirmation.Allowed }, { _, _ -> throw failure })
        failures.forEachIndexed { index, expected ->
            failure = expected
            val result = registry.call(PhoneToolCall("uncertain-exception-$index", "phone_calendar_create_event", writeArgs))
            assertTrue("unexpected result for ${expected::class.simpleName}", result.isError)
            assertTrue("ordinary failure leaked for ${expected::class.simpleName}: ${text(result)}",
                text(result).contains("不确定") || text(result).contains("did not confirm"))
        }
    }

    @Test fun aFatalErrorIsRethrownToLeaderAndFollowerInsteadOfBecomingAToolFailure() = runTest {
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            { PhoneConfirmation.Allowed }, { _, _ -> throw LinkageError("native library missing") })
        val call = PhoneToolCall("fatal-write", "phone_calendar_create_event", writeArgs)
        supervisorScope {
            val leader = async { registry.call(call) }
            runCurrent()
            val follower = async { registry.call(call) }
            runCurrent()
            assertEquals("native library missing", assertThrows(LinkageError::class.java) { runBlocking { leader.await() } }.message)
            assertThrows(LinkageError::class.java) { runBlocking { follower.await() } }
        }
        assertTrue(registry.writesInFlight.value.isEmpty())
    }

    /** A plain read timeout keeps its existing wording (not the uncertain one). */
    @Test fun aPlainReadTimeoutKeepsTheOrdinaryTimeoutWording() = runTest {
        val registry = PhoneToolRegistry({ PhoneSource.entries.toSet() }, { true }, { true }, { _, _ -> true },
            { error("no confirmation") }, { _, _ ->
                kotlinx.coroutines.delay(60_000)
                PhoneToolResult.text("ok")
            })
        val result = registry.call(PhoneToolCall("slow-read", "phone_device_info", buildJsonObject {}))
        assertTrue(result.isError)
        assertEquals(text(PhoneToolText.timedOut()), text(result))
    }

    /**
     * An unexpected exception thrown outside the existing catch list (here: the availability check itself)
     * must not leave the call-id's in-flight slot occupied forever. The leader converts it into a failure
     * result, completes the gate, and a follower waiting on the same call-id gets that result instead of
     * hanging. The exception never escapes the runtime's launch.
     */
    @Test fun anUnexpectedExceptionReleasesTheInflightSlotAndAnswersFollowers() = runTest {
        val confirmGate = CompletableDeferred<Unit>()
        val registry = PhoneToolRegistry({ setOf(PhoneSource.Calendar) }, { true }, { true }, { _, _ -> true },
            // The confirmation itself suspends, then throws an exception type the existing catches do not
            // name. The leader must convert it, complete the gate, and wake the follower.
            { confirmGate.await(); throw RuntimeException("boom") },
            { _, _ -> PhoneToolResult.text("ok") })
        val writeArgs = buildJsonObject {
            put("title", "测试日程"); put("start", "2026-11-01T10:00:00+08:00"); put("end", "2026-11-01T11:00:00+08:00")
        }
        val call = PhoneToolCall("exploding", "phone_calendar_create_event", writeArgs)
        val leader = async { registry.call(call) }
        runCurrent()
        // The leader is suspended inside confirm: the follower joins as a Follower, not a new Leader.
        val follower = async { registry.call(call) }
        runCurrent()
        assertFalse(leader.isCompleted)
        assertFalse(follower.isCompleted)
        // Let the confirmation throw. The leader converts it into a failure result; the follower's deferred
        // is completed with that same result instead of hanging forever.
        confirmGate.complete(Unit)
        val leaderResult = leader.await()
        val followerResult = follower.await()
        assertTrue(leaderResult.isError)
        assertTrue(followerResult.isError)
        assertEquals(text(leaderResult), text(followerResult))
        // The gate is released: a later call with the same id replays the cached failure, not a new leader.
        val replay = registry.call(call)
        assertTrue(replay.isError)
        assertEquals(text(leaderResult), text(replay))
    }
}
