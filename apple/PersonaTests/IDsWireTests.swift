import Foundation
import Testing
import PersonaWire

/// The IDs and AgentRef of spec/fixtures/ids (schema/wire/ids.schema.json, #15397) read into the generated types and
/// write back as the same JSON; an AgentRef without a field fails to decode.
struct IDsWireTests {
    private static let fixture = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appending(path: "spec/fixtures/ids/ids.json")

    @Test func writesEverySampleBack() throws {
        let root = try #require(JSONSerialization.jsonObject(with: Data(contentsOf: Self.fixture)) as? [String: Any])
        let cases = try #require(root["cases"] as? [[String: Any]])
        #expect(!cases.isEmpty)
        for c in cases {
            let expected = try #require(c["expected"] as? [String: Any])
            let value = try #require(expected["value"])
            let data = try JSONSerialization.data(withJSONObject: ["v": value])
            struct Box<T: Codable>: Codable { var v: T }
            let written: Data
            switch expected["definition"] as? String {
            case "AgentRef": written = try JSONEncoder().encode(JSONDecoder().decode(Box<AgentRef>.self, from: data))
            case "MachineID": written = try JSONEncoder().encode(JSONDecoder().decode(Box<MachineID>.self, from: data))
            case "SessionID": written = try JSONEncoder().encode(JSONDecoder().decode(Box<SessionID>.self, from: data))
            case "InteractionID": written = try JSONEncoder().encode(JSONDecoder().decode(Box<InteractionID>.self, from: data))
            case "TodoID": written = try JSONEncoder().encode(JSONDecoder().decode(Box<TodoID>.self, from: data))
            default: continue
            }
            let back = try #require(JSONSerialization.jsonObject(with: written) as? [String: Any])
            #expect(NSDictionary(dictionary: back).isEqual(to: ["v": value]), "\(c["name"] ?? "")")
        }
    }

    @Test func anAgentRefNeedsBothFields() {
        #expect(throws: DecodingError.self) {
            _ = try JSONDecoder().decode(AgentRef.self, from: Data(#"{"machine":"m"}"#.utf8))
        }
    }
}
