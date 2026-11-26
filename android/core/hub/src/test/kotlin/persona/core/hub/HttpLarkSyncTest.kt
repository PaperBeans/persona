package persona.core.hub

import kotlinx.coroutines.runBlocking
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test

/** Settings › Lark bot › daily sync over HTTP (web `lib/lark-sync.ts`): the view, an older hub, and every change's route. */
class HttpLarkSyncTest {
    private val timeouts = mutableListOf<Long?>()

    private fun <T> withHub(base: String? = "https://hub.example.com/persona/", block: suspend (MockWebServer, HttpLarkSync) -> T): T = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            block(server, HttpLarkSync({ path, method, body, timeout ->
                timeouts += timeout
                val response = http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
                if (!response.isSuccessful) throw response.use { HubFailure.from(it) }
                response
            }, { base?.toHttpUrl() }))
        }
    }

    private val view = """{"available":true,"schedule_hour":4,
        "accounts":[{"id":"a1","label":"Work","brand":"feishu","name":"Example User"}],
        "bots":[{"bot_id":"b1","label":"Second","role":"sync_only","tier":"docs_standard","primary":false,"has_secret":true},
                {"bot_id":"primary","app_id":"cli_a1","domain":"https://open.feishu.cn","role":"chat_and_sync","tier":"full_with_im",
                 "approval":"awaiting_approval","primary":true,"has_secret":true,"future":1}],
        "bindings":[{"key":"primary:a1","bot_id":"primary","account_id":"a1","tier":"full_with_im","enabled":true,"health":"ok",
                     "authorized_at":"2026-11-01T08:00:00Z","reauth_due_days":341,"last_run_at":"2026-11-24T03:12:00+08:00",
                     "next_run_at":"2026-11-25T03:12:00+08:00","last_messages":12,"last_docs":4,"running":false}]}"""

    @Test fun readsTheViewAndToleratesWhatItDoesNotKnow() = withHub { server, api ->
        server.enqueue(MockResponse().setBody(view))
        val v = api.view()!!
        assertEquals(4, v.scheduleHour)
        assertEquals(listOf("b1", "primary"), v.bots.map { it.botId })
        assertEquals("awaiting_approval", v.bots[1].approval)
        assertTrue(v.bots[1].primary)
        val b = v.bindings.single()
        assertEquals("primary:a1", b.key)
        assertEquals(341, b.reauthDueDays)
        assertEquals(12, b.lastMessages)
        assertEquals("2026-11-25T03:12:00+08:00", b.nextRunAt)
        assertEquals("Work", v.accounts.single().label)
        val request = server.takeRequest()
        assertEquals("GET", request.method)
        assertEquals("/api/v1/bots/feishu/sync", request.path)
    }

    @Test fun anOlderHubOrOneWithoutTheModuleHidesTheSection() = withHub { server, api ->
        for (status in listOf(404, 405, 501)) {
            server.enqueue(MockResponse().setResponseCode(status).setBody("""{"error":"not found"}"""))
            assertNull(api.view())
        }
        server.enqueue(MockResponse().setBody("""{"available":false,"schedule_hour":3,"accounts":[],"bots":[],"bindings":[]}"""))
        assertNull(api.view())
        // Another route's answer (an older hub's bot status) is no view either.
        server.enqueue(MockResponse().setBody("""{"enabled":true,"app_id":"cli_a1"}"""))
        assertNull(api.view())
        // A real failure is not hidden.
        server.enqueue(MockResponse().setResponseCode(500).setBody("""{"error":"database down"}"""))
        assertEquals(500, assertThrows(HubFailure::class.java) { runBlocking { api.view() } }.status)
    }

    @Test fun anHourOutOfRangeIsTheDefault() {
        assertEquals(3, HttpLarkSync.viewOf("""{"available":true,"schedule_hour":31,"bots":[],"bindings":[]}""")!!.scheduleHour)
    }

    @Test fun everyChangeGoesToItsRoute() = withHub { server, api ->
        val empty = """{"available":true,"schedule_hour":5,"accounts":[],"bots":[],"bindings":[]}"""
        val prompt = """{"kind":"qr","qr":{"size":2,"rows":["10","01"]},"interval":3}"""
        fun answer(body: String) = server.enqueue(MockResponse().setBody(body))
        answer(empty); assertEquals(5, api.setSchedule(5)!!.scheduleHour)
        answer("""{"bot_id":"b9","prompt":$prompt}"""); assertEquals("b9", api.createBot("Work", "full_with_im", "lark").botId)
        answer(prompt); assertEquals("qr", api.register("b9").kind)
        answer(prompt); assertEquals(listOf("10", "01"), api.addScopes("primary", "docs_standard").qr?.rows)
        answer(empty); val _ = api.removeBot("b 9")
        answer("""{"mode":"browser","url":"https://open.feishu.cn/authorize?x=1"}""")
        assertEquals("browser", api.authorize("b9", "https://hub.example.com/lark-oauth", "docs_standard").mode)
        answer("""{"done":true}"""); assertTrue(api.pollAuthorize("b9").done)
        answer(empty); val _ = api.finishOAuth("st", "co")
        answer(empty); val _ = api.setBindingEnabled("b9:a1", false)
        answer(empty); val _ = api.removeBinding("b9:a1")
        answer(empty); val _ = api.runBinding("b9:a1")
        val seen = List(11) { server.takeRequest() }.map { Triple(it.method, it.path, it.body.readUtf8()) }
        assertEquals(listOf(
            Triple("PUT", "/api/v1/bots/feishu/sync", """{"schedule_hour":5}"""),
            Triple("POST", "/api/v1/bots/feishu/sync/bots", """{"label":"Work","tier":"full_with_im","region":"lark"}"""),
            Triple("POST", "/api/v1/bots/feishu/sync/bots/b9/register", """{"step":"register"}"""),
            Triple("POST", "/api/v1/bots/feishu/sync/bots/primary/scopes", """{"tier":"docs_standard"}"""),
            Triple("DELETE", "/api/v1/bots/feishu/sync/bots/b%209", ""),
            Triple("POST", "/api/v1/bots/feishu/sync/bots/b9/authorize", """{"redirect_uri":"https://hub.example.com/lark-oauth","tier":"docs_standard"}"""),
            Triple("POST", "/api/v1/bots/feishu/sync/bots/b9/authorize/poll", "{}"),
            Triple("POST", "/api/v1/bots/feishu/sync/oauth", """{"state":"st","code":"co"}"""),
            Triple("PUT", "/api/v1/bots/feishu/sync/bindings/b9%3Aa1", """{"enabled":false}"""),
            Triple("DELETE", "/api/v1/bots/feishu/sync/bindings/b9%3Aa1", ""),
            Triple("POST", "/api/v1/bots/feishu/sync/bindings/b9%3Aa1/run", "{}"),
        ), seen)
        // The steps that ask the open platform first wait longer than the client's usual read timeout.
        assertEquals(listOf(null, 40L, 40L, 40L, null, 40L, null, 40L, null, null, null), timeouts)
    }

    @Test fun theReturnAddressIsTheHubsOwnUnderItsPrefix() = withHub { _, api ->
        assertEquals("https://hub.example.com/persona/lark-oauth", api.oauthRedirectUri())
        assertEquals("http://192.0.2.1:8787/lark-oauth", HttpLarkSync.oauthRedirectUri("http://192.0.2.1:8787".toHttpUrl()))
    }

    @Test fun noAddressNoReturn() = withHub(base = null) { _, api -> assertNull(api.oauthRedirectUri()) }
}
