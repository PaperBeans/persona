package persona.feature.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class QueuedRelayTest {
    @Test fun parsesBothHubPrefixesAndKeepsTheBody() {
        val body = "来自「早报」（不唤醒）：第一行\n第二行"
        assertEquals(QueuedRelay("日报", body, true), QueuedRelay.parse("来自「  日报  」（不唤醒）：$body"))
        assertEquals(QueuedRelay("日报", body, false), QueuedRelay.parse("来自「日报」：$body"))
    }

    @Test fun keepsNoticesAndMalformedLabelsUnparsed() {
        assertNull(QueuedRelay.parse("督办消息"))
        assertNull(QueuedRelay.parse("来自「」（不唤醒）：来自「日报」：内容"))
    }
}
