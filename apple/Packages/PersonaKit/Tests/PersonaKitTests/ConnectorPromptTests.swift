import Foundation
import Testing
@testable import PersonaKit

/// A sign-in step as the hub sends it: fields to fill in, a code to enter on a web page (GitHub's device flow), or a QR
/// code to scan with another app (WeChat).
struct ConnectorPromptTests {
    private func prompt(_ json: String) throws -> ConnectorPrompt {
        try JSONDecoder().decode(ConnectorPrompt.self, from: Data(json.utf8))
    }

    @Test func aDeviceCode() throws {
        let p = try prompt(#"{"step":"device","kind":"device_code","url":"https://github.com/login/device","user_code":"ABCD-1234","interval":5}"#)
        #expect(p.isDeviceCode)
        #expect(p.userCode == "ABCD-1234")
        #expect(p.interval == 5)
    }

    @Test func aQRCodeWithALink() throws {
        let p = try prompt(#"{"step":"register","kind":"qr","url":"https://accounts.example.com/page","interval":5,"qr":{"size":1,"rows":["1"]},"link":"https://accounts.example.com/page","link_text":"Open in Lark"}"#)
        #expect(p.isQRCode)
        #expect(p.link == "https://accounts.example.com/page")
        #expect(p.linkText == "Open in Lark")
    }

    @Test func fieldsAsBefore() throws {
        let p = try prompt(#"{"step":"phone","fields":[{"name":"phone","label":"手机号"}]}"#)
        #expect(!p.isDeviceCode)
        #expect(!p.isQRCode)
        #expect(!p.polls)
        #expect(p.fields?.first?.name == "phone")
    }

    @Test func aQRCode() throws {
        let p = try prompt(#"{"step":"qr","kind":"qr","url":"https://example.com/q","interval":2,"qr":{"size":2,"rows":["10","01"]}}"#)
        #expect(p.isQRCode)
        #expect(!p.isDeviceCode)
        #expect(p.polls)
        #expect(p.qr?.rows == ["10", "01"])
        #expect(p.pollKey == "1001")
    }

    /// A QR step from a hub that did not draw the code (no `qr`) is not shown as one: nothing to scan.
    @Test func aQRStepWithoutTheCode() throws {
        let p = try prompt(#"{"step":"qr","kind":"qr","url":"https://example.com/q","interval":2}"#)
        #expect(!p.isQRCode)
        #expect(!p.polls)
    }

    /// A step the app cannot draw says so instead of an empty form whose Next never lights up (#1693): an unknown
    /// kind, a QR step without the code, a device code without the code, a form with no fields.
    @Test func stepsItCannotDraw() throws {
        for json in [#"{"step":"auth","kind":"open_url","url":"https://example.com/auth"}"#,
                     #"{"step":"qr","kind":"qr","url":"https://example.com/q","interval":2}"#,
                     #"{"step":"qr","kind":"qr","qr":{"size":0,"rows":[]}}"#,
                     #"{"step":"device","kind":"device_code","url":"https://example.com/device"}"#,
                     #"{"step":"wait","hint":"等一下"}"#] {
            let p = try prompt(json)
            #expect(p.cannotDraw, "\(json)")
            #expect(!p.polls, "\(json)")
        }
        for json in [#"{"step":"phone","fields":[{"name":"phone","label":"手机号"}]}"#,
                     #"{"step":"device","kind":"device_code","user_code":"ABCD-1234"}"#,
                     #"{"step":"qr","kind":"qr","qr":{"size":1,"rows":["1"]}}"#,
                     #"{"step":"pair","kind":"pair_code","user_code":"482913"}"#] {
            #expect(try !prompt(json).cannotDraw, "\(json)")
        }
    }

    @Test func theNoteSaysWhatToDo() throws {
        let qr = try prompt(#"{"step":"qr","kind":"qr"}"#)
        // `swift test` has no app catalog, so the note comes back as its English development key (#5758).
        #expect(qr.cannotDrawNote(name: "微信") == "This version of the app can't show this step. Update the app and tap Start Over, or scan the code on the hub's web page in Settings › Connectors › 微信.")
        let other = try prompt(#"{"step":"auth","kind":"open_url"}"#)
        #expect(other.cannotDrawNote(name: "Example") == "This version of the app can't show this step. Update the app and tap Start Over to try again.")
    }

    /// A new code (it expired and the connector made another) restarts the polling.
    @Test func aNewCodeIsANewPoll() throws {
        let a = try prompt(#"{"step":"qr","kind":"qr","qr":{"size":1,"rows":["1"]}}"#)
        let b = try prompt(#"{"step":"qr","kind":"qr","qr":{"size":1,"rows":["0"]}}"#)
        #expect(a.pollKey != b.pollKey)
    }
}
