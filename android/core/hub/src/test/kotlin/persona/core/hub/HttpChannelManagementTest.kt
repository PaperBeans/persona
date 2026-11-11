package persona.core.hub

import persona.core.contracts.api.ChannelChange
import persona.core.contracts.api.ChannelNotFound
import persona.core.contracts.api.ChannelReadOnly
import persona.core.contracts.api.ChannelRejected
import persona.core.contracts.api.ChannelTarget
import persona.core.contracts.api.ChannelsUnsupported
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.*
import org.junit.Test

class HttpChannelManagementTest {
    private val timeouts = mutableListOf<Long?>()

    // Like the pairing's connection: a status that is not 2xx is thrown as the hub's error before the client sees it.
    private fun <T> withHub(block: suspend (MockWebServer, HttpChannelManagement) -> T): T = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            block(server, HttpChannelManagement({ path, method, body, timeout ->
                timeouts += timeout
                val response = http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
                if (!response.isSuccessful) throw response.use { HubFailure.from(it) }
                response
            }))
        }
    }

    private val wechat = """{"id":"wechat","name":"微信","kind":"im","source":"plugin","plugin":"wechat","installed":true,"enabled":true,
        "state":"connected","account":{"name":"微信用户 a1b2c3"},
        "capabilities":{"images_in":true,"voice_in":true,"format":"plain","max_text":3500,"asks":"text","push":true,"typing":true},
        "target":{"kind":"conversation","machine_id":"hub","session_id":"s1","title":"周报"},
        "features":[{"id":"asks","title":"提问和批准","description":"在这里回答","on":true},{"id":"pushes","title":"推送也发到这里","on":false}],
        "bind":"qr","last_event_at":"2026-11-04T10:00:00Z","unknown_key":[1]"""

    @Test fun listsTheChannelsAndToleratesWhatItDoesNotKnow() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"channels":[
            {"id":"app","name":"Persona","kind":"app","source":"builtin","installed":true,"enabled":true,"state":"connected","bind":"none",
             "read_only":true,"surfaces":["iPhone","Android"],"capabilities":{}},
            $wechat},
            {"id":"examplechat","name":"Example Chat","kind":"im","source":"market","plugin":"example-chat","market":"m1",
             "installed":false,"enabled":false,"state":"not_installed","bind":"none","capabilities":{},"future":"x"}]}"""))
        val list = api.channels()
        assertEquals("/api/v1/channels", (server.takeRequest().path).zhAny())
        assertEquals(listOf("app", "wechat", "examplechat"), (list.map { it.id }).zhAny())
        assertTrue(list[0].readOnly)
        assertEquals(listOf("iPhone", "Android"), (list[0].surfaces).zhAny())
        val w = list[1]
        assertEquals("微信用户 a1b2c3", (w.account?.name).zhAny())
        assertEquals(3500, w.capabilities.maxText)
        assertEquals("text", (w.capabilities.asks).zhAny())
        assertTrue(w.capabilities.imagesIn && !w.capabilities.filesIn)
        assertEquals(ChannelTarget("conversation", "hub", "s1", "周报"), w.target)
        assertEquals(listOf("asks", "pushes"), (w.features.map { it.id }).zhAny())
        assertEquals("2026-11-04T10:00:00Z", (w.lastEventAt).zhAny())
        assertEquals("m1", (list[2].market).zhAny())
        assertEquals("example-chat", (list[2].extension).zhAny())
    }

    @Test fun anOldHubWithoutChannelsSaysSo() = withHub { server, api ->
        server.enqueue(MockResponse().setResponseCode(404).setBody("404 page not found\n"))
        assertTrue(runCatching { api.channels() }.exceptionOrNull() is ChannelsUnsupported)
    }

    @Test fun decodesTheDetailFromOneObject() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""$wechat,"waiting":{"answers":1,"asks":2},
            "conversations":[{"machine_id":"hub","session_id":"s1","title":"周报","at":"2026-11-04T09:00:00Z"}],
            "settings_url":"settings/plugins/wechat"}"""))
        val d = api.channel("wechat")
        assertEquals("/api/v1/channels/wechat", (server.takeRequest().path).zhAny())
        assertEquals("微信", (d.channel.name).zhAny())
        assertEquals(1, d.waiting.answers)
        assertEquals(2, d.waiting.asks)
        assertEquals("s1", (d.conversations.single().sessionId).zhAny())
        assertEquals("settings/plugins/wechat", (d.settingsUrl).zhAny())

        // The app channel: nothing waits, nothing to link to.
        server.enqueue(MockResponse().setBody("""{"id":"app","name":"Persona","kind":"app","source":"builtin","state":"connected","waiting":{"answers":0,"asks":0}}"""))
        val app = api.channel("app")
        assertNull(app.settingsUrl)
        assertTrue(app.conversations.isEmpty())
    }

    @Test fun sendsOnlyWhatChanged() = withHub { server, api ->
        repeat(3) { server.enqueue(MockResponse().setBody("$wechat}")) }
        api.update("wechat", ChannelChange(enabled = false))
        server.takeRequest().let {
            assertEquals("PUT", (it.method).zhAny()); assertEquals("/api/v1/channels/wechat", (it.path).zhAny())
            assertEquals("""{"enabled":false}""", (it.body.readUtf8()).zhAny())
        }
        api.update("wechat", ChannelChange(target = ChannelTarget.conversation("hub", "s2")))
        assertEquals("""{"target":{"kind":"conversation","machine_id":"hub","session_id":"s2"}}""", (server.takeRequest().body.readUtf8()).zhAny())
        api.update("wechat", ChannelChange(target = ChannelTarget("new", title = "不发回去"), features = mapOf("pushes" to true)))
        assertEquals("""{"target":{"kind":"new"},"features":{"pushes":true}}""", (server.takeRequest().body.readUtf8()).zhAny())
    }

    @Test fun bindsAStepAtATime() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"step":"credentials","fields":[{"name":"app_id","label":"App ID","placeholder":"cli_…"},
            {"name":"app_secret","label":"App Secret","secret":true}],"hint":"先建应用"}"""))
        val first = api.bind("feishu")
        server.takeRequest().let {
            assertEquals("POST", (it.method).zhAny()); assertEquals("/api/v1/channels/feishu/bind", (it.path).zhAny())
            assertEquals("{}", (it.body.readUtf8()).zhAny())
        }
        assertEquals(listOf("app_id", "app_secret"), (first.fields.map { it.name }).zhAny())
        assertTrue(first.fields[1].secret)
        assertFalse(first.polled)

        server.enqueue(MockResponse().setBody("""{"step":"pair","kind":"pair_code","user_code":"123456","interval":3,"hint":"发给机器人"}"""))
        val pair = api.bind("feishu", "credentials", mapOf("app_id" to "cli_x", "app_secret" to "s"))
        assertEquals("""{"step":"credentials","values":{"app_id":"cli_x","app_secret":"s"}}""", (server.takeRequest().body.readUtf8()).zhAny())
        assertEquals("123456", (pair.userCode).zhAny())
        assertEquals(3, pair.interval)
        assertTrue(pair.polled)

        server.enqueue(MockResponse().setBody("""{"step":"scan","kind":"qr","url":"https://example.com/qr","interval":2,
            "qr":{"size":2,"rows":["10","01"]}}"""))
        val qr = api.bind("wechat", "scan")
        assertEquals("""{"step":"scan"}""", (server.takeRequest().body.readUtf8()).zhAny())
        assertEquals(listOf("10", "01"), (qr.qr?.rows).zhAny())
        assertTrue(qr.polled)

        // The Lark bot's registration: the page the code points at also comes as a link to open directly.
        server.enqueue(MockResponse().setBody("""{"step":"register","kind":"qr","url":"https://example.com/page","interval":5,
            "qr":{"size":2,"rows":["10","01"]},"link":"https://example.com/page","link_text":"Open in Lark"}"""))
        val reg = api.bind("feishu", "register")
        server.takeRequest()
        assertEquals("https://example.com/page", (reg.link).zhAny())
        assertEquals("Open in Lark", (reg.linkText).zhAny())

        server.enqueue(MockResponse().setBody("""{"done":true,"account":"微信用户 a1b2c3"}"""))
        val done = api.bind("wechat", "scan")
        server.takeRequest()
        assertTrue(done.done && !done.polled)
        assertEquals("微信用户 a1b2c3", (done.account).zhAny())
        // Binding and the test message wait on the chat app: longer than the usual read timeout.
        assertTrue(timeouts.all { it != null && it > 15 })
    }

    @Test fun unbindsAndTests() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"id":"wechat","name":"微信","state":"not_connected"}"""))
        assertEquals("not_connected", (api.unbind("wechat").channel.state).zhAny())
        server.takeRequest().let { assertEquals("DELETE", (it.method).zhAny()); assertEquals("/api/v1/channels/wechat/binding", (it.path).zhAny()) }
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        api.test("wechat")
        server.takeRequest().let { assertEquals("POST", (it.method).zhAny()); assertEquals("/api/v1/channels/wechat/test", (it.path).zhAny()) }
        assertNull(timeouts.first())
    }

    @Test fun theHubsRefusalsComeBackAsTheirOwnErrors() = withHub { server, api ->
        server.enqueue(MockResponse().setResponseCode(400).setBody("""{"error":"只能选助手的对话","code":"invalid"}"""))
        val rejected = runCatching { api.update("wechat", ChannelChange(target = ChannelTarget.conversation("m", "s"))) }.exceptionOrNull()
        assertTrue(rejected is ChannelRejected)
        assertEquals("只能选助手的对话", (rejected!!.message).zhAny())

        server.enqueue(MockResponse().setResponseCode(409).setBody("""{"error":"Persona 不在这里设置","code":"read_only"}"""))
        val readOnly = runCatching { api.update("app", ChannelChange(enabled = true)) }.exceptionOrNull()
        assertTrue(readOnly is ChannelReadOnly)
        assertEquals("Persona 不在这里设置", (readOnly!!.message).zhAny())

        server.enqueue(MockResponse().setResponseCode(404).setBody("""{"error":"no such channel","code":"not_found"}"""))
        assertTrue(runCatching { api.channel("gone") }.exceptionOrNull() is ChannelNotFound)

        // A failure to reach the chat app is the hub's error as it is.
        server.enqueue(MockResponse().setResponseCode(502).setBody("""{"error":"微信 还没连上"}"""))
        val other = runCatching { api.test("wechat") }.exceptionOrNull() as HubFailure
        assertEquals(502, other.status)
        assertEquals("微信 还没连上", (other.message).zhAny())
    }

    @Test fun refusesIdsThatWouldBecomeAnotherPath() = withHub { server, api ->
        for (id in listOf("..", "a/b", "Wechat", "", "-x")) {
            assertTrue(id, runCatching { api.channel(id) }.exceptionOrNull() is IllegalArgumentException)
        }
        assertEquals(0, server.requestCount)
    }

    @Test fun anUnreadableAnswerIsAProtocolError() = withHub { server, api ->
        server.enqueue(MockResponse().setBody("""{"nope":true}"""))
        assertEquals(HubError.Protocol, (runCatching { api.channels() }.exceptionOrNull() as HubFailure).kind)
        server.enqueue(MockResponse().setBody("not json"))
        assertEquals(HubError.Protocol, (runCatching { api.channel("wechat") }.exceptionOrNull() as HubFailure).kind)
    }
}
