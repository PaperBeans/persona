import PersonaKit
import Testing

struct QueuedRelayTests {
    @Test func parsesBothHubPrefixesAndKeepsTheBody() throws {
        let body = "来自「早报」（不唤醒）：第一行\n第二行"
        let quiet = try #require(QueuedRelay("来自「  日报  」（不唤醒）：\(body)"))
        #expect(quiet.source == "日报")
        #expect(quiet.text == body)
        #expect(quiet.noWake)

        let ordinary = try #require(QueuedRelay("来自「日报」：\(body)"))
        #expect(ordinary.source == "日报")
        #expect(ordinary.text == body)
        #expect(!ordinary.noWake)
    }

    @Test func keepsNoticesAndMalformedLabelsUnparsed() {
        #expect(QueuedRelay("督办消息") == nil)
        #expect(QueuedRelay("来自「」（不唤醒）：来自「日报」：内容") == nil)
    }
}
