import Foundation

/// One step of a connector's sign-in (POST /api/v1/connectors/{id}/login): the fields to fill in, or done.
public nonisolated struct ConnectorPrompt: Codable, Equatable, Sendable {
    public nonisolated struct Field: Codable, Equatable, Sendable, Identifiable {
        public var name: String
        public var label: String
        public var secret: Bool?
        public var placeholder: String?
        public var id: String { name }

        public init(name: String, label: String, secret: Bool? = nil, placeholder: String? = nil) {
            self.name = name
            self.label = label
            self.secret = secret
            self.placeholder = placeholder
        }
    }
    public var step: String?
    public var fields: [Field]?
    public var hint: String?
    public var error: String?
    public var done: Bool?
    public var account: String?
    /// "device_code": show `userCode` to enter at `url`; "qr": show `qr`, the code of `url`, to scan with another app;
    /// "pair_code": show `userCode` to send to a bot from its chat app (a channel's binding, docs/extensions/channels.md). For all
    /// three the same step is sent again every `interval` seconds.
    public var kind: String?
    public var url: String?
    public var userCode: String?
    public var interval: Int?
    /// The QR code's modules, drawn by the hub for a "qr" step.
    public var qr: QRModules?
    /// Where what the step asks is done, opened outside the app (the Lark bot's registration page: on the phone that
    /// has Lark, opening it beats scanning it), with the button's label.
    public var link: String?
    public var linkText: String?

    public nonisolated struct QRModules: Codable, Equatable, Sendable {
        public var size: Int
        public var rows: [String]
    }

    enum CodingKeys: String, CodingKey {
        case step, fields, hint, error, done, account, kind, url, interval, qr
        case userCode = "user_code"
        case link
        case linkText = "link_text"
    }

    /// A code to enter on a web page (GitHub's device flow), asked again until the user has.
    public var isDeviceCode: Bool { kind == "device_code" && userCode != nil }
    /// A QR code to scan with another app (WeChat), asked again until it is scanned and confirmed there.
    public var isQRCode: Bool { kind == "qr" && !(qr?.rows.isEmpty ?? true) }
    /// A code to send to a bot from its chat app (the Feishu bot's pairing), asked again until the bot has it.
    public var isPairCode: Bool { kind == "pair_code" && userCode != nil }
    /// The step is asked again on its own until the user has acted somewhere else.
    public var polls: Bool { isDeviceCode || isQRCode || isPairCode }
    /// What the polling follows: a new code starts it over.
    public var pollKey: String? { isDeviceCode || isPairCode ? userCode : isQRCode ? qr?.rows.joined() : nil }
    /// A step this app cannot draw (#1693): a kind it does not know (a newer hub's), a code or QR code that came
    /// without the code, or a form with no fields. Drawn as a form it was an empty card with a Next that could never
    /// light up; the page says so instead, with Start Over.
    public var cannotDraw: Bool {
        if isDeviceCode || isQRCode || isPairCode { return false }
        return !(kind ?? "").isEmpty || (fields ?? []).isEmpty
    }
}

extension ConnectorPrompt {
    /// Why a step is not drawn and what to do: update the app; a QR code can also be scanned from the hub's web page,
    /// which draws it (Settings › Connectors, #1693).
    public func cannotDrawNote(name: String) -> String {
        kind == "qr"
            ? String(localized: "This version of the app can't show this step. Update the app and tap Start Over, or scan the code on the hub's web page in Settings › Connectors › \(name).")
            : String(localized: "This version of the app can't show this step. Update the app and tap Start Over to try again.")
    }
}
