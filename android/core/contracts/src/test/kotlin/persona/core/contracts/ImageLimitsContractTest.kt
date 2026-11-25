package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Lenient `hello.image_limits` decoding and per-agent selection (docs/protocol/protocol.md "Pictures in a message",
 * #13043): malformed or non-positive values keep the documented defaults and never lose the hello frame.
 */
class ImageLimitsContractTest {
    private fun helloJson(limits: String): String =
        """{"type":"hello","features":["a"],"image_limits":$limits}"""

    @Test fun absentImageLimitsKeepsHelloAndDocumentedDefaults() {
        val hello = ServerEvent.decode("""{"type":"hello","features":["a"]}""") as? Hello
        assertNotNull(hello)
        assertNull(hello!!.imageLimits)
        val standard = ImageLimits()
        assertEquals(33_554_432L, standard.requestBytes)
        assertEquals(3_750_000L, standard.imageBytes)
        assertEquals(1600, standard.maxSide)
        assertEquals(20, standard.perMessage)
    }

    @Test fun perAgentOverridesTakePrecedenceAndEmptyAgentMapsToClaude() {
        val hello = ServerEvent.decode(
            helloJson(
                """{"request_bytes":1000,"image_bytes":500,"max_side":800,"per_message":6,""" +
                    """"agents":{"codex":{"request_bytes":2000,"image_bytes":900,"max_side":900,"per_message":9},""" +
                    """"claude":{"per_message":3}}}"""
            )
        ) as? Hello
        val table = requireNotNull(hello?.imageLimits)
        assertEquals(ImageLimits(2000L, 900L, 900, 9), table.forAgent("codex"))
        assertEquals(ImageLimits(1000L, 500L, 800, 6), table.forAgent("pi"))
        assertEquals(3, table.forAgent("claude").perMessage)
        assertEquals(3, table.forAgent("").perMessage)
    }

    @Test fun malformedOrNonPositiveValuesKeepDefaultsWithoutLosingHello() {
        val badValues = ServerEvent.decode(
            helloJson("""{"request_bytes":"lots","image_bytes":0,"per_message":0,"max_side":-1,"agents":7}""")
        ) as? Hello
        assertNotNull("hello must not be lost on bad image_limits values", badValues)
        assertEquals(listOf("a"), badValues!!.features)
        assertEquals(ImageLimitsTable(), badValues.imageLimits)

        val wrongShape = ServerEvent.decode(helloJson(""""nonsense"""")) as? Hello
        assertNotNull("hello must not be lost when image_limits is not an object", wrongShape)
        assertEquals(listOf("a"), wrongShape!!.features)
        assertNull(wrongShape.imageLimits)

        val mixedAgents = ServerEvent.decode(
            helloJson("""{"per_message":12,"agents":{"claude":"bad","codex":{"per_message":5,"max_side":0}}}""")
        ) as? Hello
        assertNotNull(mixedAgents)
        val table = requireNotNull(mixedAgents!!.imageLimits)
        assertEquals(12, table.forAgent("claude").perMessage)
        assertEquals(5, table.forAgent("codex").perMessage)
        assertEquals(1600, table.forAgent("codex").maxSide)
    }

    @Test fun roomLeavesOneMibForWordsAndCountsEveryPicturesBase64() {
        val limits = ImageLimits(
            requestBytes = ImageLimits.BODY_ROOM + 100L,
            imageBytes = 50L,
            maxSide = 1600,
            perMessage = 3,
        )
        assertEquals(ImageLimits.Room.Ok, limits.room(emptyList(), 100L))
        assertEquals(ImageLimits.Room.Bytes, limits.room(listOf(60L), 41L))
        assertEquals(ImageLimits.Room.Count, limits.room(listOf(1L, 1L, 1L), 1L))
        assertEquals(1, limits.roomLeft(2))
        assertEquals(0, limits.roomLeft(5))
        assertEquals(0L, ImageLimits.base64Bytes(0L))
        assertEquals(4L, ImageLimits.base64Bytes(1L))
        assertEquals(4L, ImageLimits.base64Bytes(3L))
        assertEquals(8L, ImageLimits.base64Bytes(4L))
    }

    @Test fun tableRoundTripsThroughJsonWithAgents() {
        val table = ImageLimitsTable(
            requestBytes = 10_000_000L,
            imageBytes = 1_000_000L,
            maxSide = 1200,
            perMessage = 8,
            agents = mapOf("codex" to ImageLimits(5_000_000L, 500_000L, 900, 4)),
        )
        val encoded = JsonCodec.encodeToString(ImageLimitsTable.serializer(), table)
        assertEquals(table, JsonCodec.decodeFromString(ImageLimitsTable.serializer(), encoded))
    }
}
