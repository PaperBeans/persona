package persona.core.hub

import kotlinx.coroutines.runBlocking
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import persona.core.contracts.api.PersonaPacksUnsupported

class HttpPersonaPacksTest {
    private fun ok(path: String, body: String) = Response.Builder().request(Request.Builder().url("https://hub.test$path").build())
        .protocol(Protocol.HTTP_1_1).code(200).message("OK").body(body.toResponseBody()).build()

    @Test fun anOlderHubWithoutTheRouteIsUnsupported() {
        // The connection throws for a non-2xx answer before the transport sees it.
        val packs = HttpPersonaPacks { throw HubFailure(HubError.Server, "HTTP 404", status = 404) }
        assertThrows(PersonaPacksUnsupported::class.java) { runBlocking { packs.list() } }
        val down = HttpPersonaPacks { throw HubFailure(HubError.Server, "HTTP 500", status = 500) }
        assertThrows(HubFailure::class.java) { runBlocking { down.list() } }
    }

    @Test fun filesAreFetchedByEncodedPath() = runBlocking {
        val asked = mutableListOf<String>()
        val packs = HttpPersonaPacks { path -> asked += path; ok(path, if (path.endsWith("personas")) """{"personas":[]}""" else "png") }
        assertEquals(emptyList<Any>(), (packs.list()).zhAny())
        assertEquals("png", (packs.file("doudou", "dot/idle.png").decodeToString()).zhAny())
        assertEquals(listOf("/api/v1/personas", "/api/v1/personas/doudou/dot/idle.png"), (asked).zhAny())
    }

    @Test fun looksAndTheChoiceGoToTheHub() = runBlocking {
        val put = mutableListOf<Pair<String, String>>()
        val listing = """{"looks":[{"id":"persona","source":"builtin","name":"团子","app_icon":"persona"},
            {"id":"aurora","source":"extension","name":"极光","app_icon":"persona","extension_name":"Example Looks","rev":"0123456789abcdef",
             "manifest":{"format":1,"id":"aurora","name":"极光","names":{"en":"Aurora"},"avatar":{"sizes":{"128":"a.png"},"svg":"a.svg","dark":{"sizes":{"128":"d.png"}}},
             "dot":{"states":{"idle":{"file":"i.png"}}},"source":{"kind":"extension","extension":"example-looks","extension_name":"Example Looks"}}}],
            "current":{"id":null,"effective":"persona"}}"""
        val packs = HttpPersonaPacks({ path, body -> put += path to body; ok(path, """{"id":"aurora","effective":"aurora"}""") }) { path -> ok(path, listing) }
        val looks = packs.looks()
        assertEquals(null, looks.current.id)
        val aurora = looks.looks.mapNotNull { it.pack }.single()
        assertEquals("aurora", aurora.id)
        assertEquals(true, aurora.usable)
        assertEquals("Example Looks", aurora.manifest.source?.extensionName)
        assertEquals("aurora", packs.choose("aurora", ifUnset = true).id)
        assertEquals(listOf("/api/v1/looks/current" to """{"id":"aurora","if_unset":true}"""), put)
    }

    @Test fun aHubWithoutLooksIsUnsupported() {
        val packs = HttpPersonaPacks { throw HubFailure(HubError.Server, "HTTP 404", status = 404) }
        assertThrows(PersonaPacksUnsupported::class.java) { runBlocking { packs.looks() } }
        assertThrows(PersonaPacksUnsupported::class.java) { runBlocking { packs.choose("aurora") } }
    }

    private fun answer(path: String, code: Int, body: String) = Response.Builder().request(Request.Builder().url("https://hub.test$path").build())
        .protocol(Protocol.HTTP_1_1).code(code).message("HTTP $code").body(body.toResponseBody()).build()

    /** New from Photo (#2407): only with `look_uploads`; the image goes as base64 with the name, a description when given and `choose`. */
    @Test fun uploadsSendTheImageAndDeletesNameTheLook() = runBlocking {
        val calls = mutableListOf<Triple<String, String, String?>>()
        val made = """{"look":{"id":"u-0a1b2c3d","source":"hub","name":"我的猫","rev":"0123456789abcdef","uploaded":true,
            "manifest":{"format":1,"id":"u-0a1b2c3d","name":"我的猫","avatar":{"sizes":{"128":"avatar-128.png"}},
            "dot":{"states":{"idle":{"file":"dot/idle.png"}}},"source":{"kind":"upload"}}},
            "current":{"id":"u-0a1b2c3d","effective":"u-0a1b2c3d"}}"""
        val answers = ArrayDeque(listOf(201 to made, 409 to """{"error":"已经有 50 个了","code":"too_many"}""", 200 to """{"current":{"id":"persona","effective":"persona"}}""",
            404 to """{"error":"not found","code":"not_found"}"""))
        val packs = HttpPersonaPacks(call = { method, path, body -> calls += Triple(method, path, body); answers.removeFirst().let { (code, text) -> answer(path, code, text) } },
            features = { listOf("looks", "look_uploads") }) { path -> ok(path, "{}") }
        assertEquals(true, packs.uploads)
        assertEquals(false, HttpPersonaPacks(features = { listOf("looks") }) { path -> ok(path, "{}") }.uploads)

        val image = byteArrayOf(0x89.toByte(), 'P'.code.toByte(), 'N'.code.toByte(), 'G'.code.toByte())
        val look = packs.upload("我的猫", " ", image, choose = true)
        assertEquals("u-0a1b2c3d", look.look.id)
        assertEquals(true, look.look.uploaded)
        assertEquals(true, look.look.manifest?.uploaded)
        assertEquals("u-0a1b2c3d", look.current?.id)
        assertEquals(Triple("POST", "/api/v1/looks/uploads", """{"name":"我的猫","image":"iVBORw==","choose":true}"""), calls.last())

        // The hub's refusal keeps its code and words.
        val refused = runCatching { packs.upload("Cat", "On the sofa", image, choose = false) }.exceptionOrNull() as HubFailure
        assertEquals("too_many", refused.code)
        assertEquals("已经有 50 个了", refused.message)
        assertEquals("""{"name":"Cat","subtitle":"On the sofa","image":"iVBORw=="}""", calls.last().third)

        assertEquals("persona", packs.deleteUpload("u-0a1b2c3d").id)
        assertEquals(Triple("DELETE", "/api/v1/looks/uploads/u-0a1b2c3d", null), calls.last())
        val gone = runCatching { packs.deleteUpload("a b/c") }.exceptionOrNull() as HubFailure
        assertEquals(404, gone.status)
        assertEquals("/api/v1/looks/uploads/a%20b%2Fc", calls.last().second)
    }
}
