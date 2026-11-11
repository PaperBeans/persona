import Foundation

/// A pairing link handed to the app from outside (Notes, AirDrop, Mail, Safari): `persona://link?…`,
/// the link a signed-in phone or the web makes for one computer (docs/assistant/onboarding.md 4.2).
/// Opening one only fills in the pairing window: the user sees which hub it is and pairs by tapping 「配对」 (Pair). A forwarded
/// or forged link must never hand this computer to someone else's hub by itself.
struct PairLink: Equatable {
    /// The link as it came, for `persona-link pair -`.
    var raw: String
    /// The machine id the hub made the link for; nil when the link names none.
    var machine: String?
    /// The hub's host (and port) from `url`, to show before pairing.
    var hubHost: String?
    /// The link carries the hub's TLS key fingerprint (`fp`): the hub is pinned, not checked by name.
    var pinned: Bool
    /// Default SSH tunnel target (`127.0.0.1:<TLS port>`) inferred from the link's TLS port or https URL.
    var defaultSSHTarget: String? = nil
    /// Every candidate in the link is a loopback address, so Always use SSH defaults to on.
    var loopbackOnly: Bool = false

    enum Parsed: Equatable {
        /// A link for Persona Link: fill in the window.
        case link(PairLink)
        /// One of ours, for another app (`persona://pair?…` for the phone and the Mac app, `open`, …): do nothing.
        case otherApp
        /// A link for Persona Link without what pairing needs (no token).
        case incomplete
        /// Not a Persona link at all.
        case notOurs
    }

    static let schemes: Set<String> = ["persona", LegacyNames.product]

    /// The helper's passive pairing link, not a request to pair this app automatically.
    static func isConnectorOutput(_ raw: String) -> Bool {
        schemes.contains { raw.hasPrefix($0 + "://connector?") }
    }

    static func parse(_ url: URL) -> Parsed {
        parse(url.absoluteString)
    }

    static func parse(_ raw: String) -> Parsed {
        let s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: s), schemes.contains(c.scheme?.lowercased() ?? "") else { return .notOurs }
        guard c.host?.lowercased() == "link" else { return .otherApp }
        let q = c.queryItems ?? []
        func value(_ name: String) -> String? {
            q.first { $0.name == name }?.value.flatMap { $0.isEmpty ? nil : $0 }
        }
        guard value("token") != nil else { return .incomplete }
        let host = value("url").map { LinkText.host($0) }.flatMap { $0.isEmpty ? nil : $0 }
        let pinned = value("fp") != nil
        let defaults = sshDefaults(queryItems: q, pinned: pinned)
        return .link(PairLink(raw: s, machine: value("machine"), hubHost: host, pinned: pinned,
                              defaultSSHTarget: defaults.target, loopbackOnly: defaults.loopbackOnly))
    }

    private static func sshDefaults(queryItems q: [URLQueryItem], pinned: Bool) -> (target: String?, loopbackOnly: Bool) {
        func value(_ name: String) -> String? {
            q.first { $0.name == name }?.value.flatMap { $0.isEmpty ? nil : $0 }
        }
        var urls: [URLComponents] = []
        if pinned {
            if let tls = value("tls"), let port = Int(tls), (1...65535).contains(port) {
                var c = URLComponents()
                c.scheme = "https"
                c.host = value("url").flatMap { URLComponents(string: $0)?.host }.flatMap { $0.isEmpty ? nil : $0 } ?? "127.0.0.1"
                c.port = port
                urls.append(c)
            }
        } else if let rawURL = value("url"), let c = URLComponents(string: rawURL), c.scheme?.lowercased() == "https",
                  !(c.host ?? "").isEmpty {
            urls.append(c)
        }
        for item in q where item.name == "alt" {
            guard let v = item.value?.trimmingCharacters(in: .whitespacesAndNewlines), !v.isEmpty else { continue }
            let withScheme = v.contains("://") ? v : "https://\(v)"
            if let c = URLComponents(string: withScheme), c.scheme?.lowercased() == "https", !(c.host ?? "").isEmpty {
                urls.append(c)
            }
        }
        guard let first = urls.first else { return (nil, false) }
        let port = first.port ?? 443
        guard (1...65535).contains(port) else { return (nil, false) }
        let loopback = urls.allSatisfy { isLoopbackHost($0.host ?? "") }
        return ("127.0.0.1:\(port)", loopback)
    }

    static func isLoopbackHost(_ raw: String) -> Bool {
        let h = raw.trimmingCharacters(in: CharacterSet(charactersIn: "[]")).lowercased()
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
        if h == "localhost" || h == "::1" || h == "0:0:0:0:0:0:0:1" { return true }
        let parts = h.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4, parts[0] == "127" else { return false }
        return parts.allSatisfy { Int($0).map { (0...255).contains($0) } == true }
    }

    /// What the window says about a link that came from outside and did not fill it in.
    static func notice(for parsed: Parsed) -> String? {
        switch parsed {
        case .link, .notOurs: nil
        case .otherApp: "这是给手机 / Mac App 用的链接，不是给这台电脑配对用的。在手机或网页的「添加电脑」里拷贝这台电脑的配对链接。"
        case .incomplete: "这条配对链接不完整（没有 token）：在手机或网页的「添加电脑」里重新拷贝一次。"
        }
    }
}
