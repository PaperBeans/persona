import Foundation
import Testing

import PersonaWire

/// The counts views of spec/fixtures/counts/view_samples.json (schema/wire/counts.schema.json, #15512) read into the
/// generated CountsView and write back the same bytes: every field, no other. A reason this app does not know is kept
/// as it came, and a count a manager did not send stays absent rather than 0.
@Suite struct CountsWireTests {
    private static let fixture = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        .appendingPathComponent("../../../../../spec/fixtures/counts/view_samples.json")

    @Test func generatedTypesWriteTheSampleBytesBack() throws {
        let root = try #require(JSONSerialization.jsonObject(with: Data(contentsOf: Self.fixture)) as? [String: Any])
        let cases = try #require(root["cases"] as? [[String: Any]])
        #expect(!cases.isEmpty)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        for c in cases {
            let view = try #require((c["expected"] as? [String: Any])?["view"] as? [String: Any])
            let data = try JSONSerialization.data(withJSONObject: view, options: [.sortedKeys, .withoutEscapingSlashes])
            let written = try encoder.encode(JSONDecoder().decode(CountsView.self, from: data))
            #expect(written == data, "\(c["name"] ?? "")")
        }
    }

    @Test func anUnknownReasonIsKeptAsItCame() throws {
        let row = try JSONDecoder().decode(CountsHeldRow.self, from: Data(#"{"id":"agent:studio/q","why":"quiet"}"#.utf8))
        #expect(row.why == .unknown("quiet"))
        #expect(String(decoding: try JSONEncoder().encode(row.why), as: UTF8.self) == #""quiet""#)
        #expect(CountsHeldReason(rawValue: "muted") == .muted)
    }

    @Test func aCountNotSentIsAbsent() throws {
        let view = try JSONDecoder().decode(CountsView.self, from: Data(
            #"{"epoch":"e1","rev":1,"as_of":1795593600000,"build":"1.0.0","counts":{}}"#.utf8))
        #expect(view.counts.agentsNeedsYou == nil)
        #expect(view.totals == nil)
        #expect(view.asOf == 1_795_593_600_000)
    }
}
