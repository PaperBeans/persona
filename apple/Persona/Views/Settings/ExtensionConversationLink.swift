import Foundation
import PersonaWire

/// The narrow navigation handoff an extension may request: one assistant
/// conversation on a known computer. It is not a general internal-link grant.
nonisolated enum ExtensionConversationLink {
    static func session(_ url: URL) -> SessionKey? {
        guard url.scheme?.lowercased() == "persona", url.host == "tracks", url.user == nil, url.password == nil,
              url.query == nil, url.fragment == nil else { return nil }
        guard url.pathComponents.first == "/" else { return nil }
        let parts = Array(url.pathComponents.dropFirst())
        guard parts.count == 3, parts.allSatisfy({ part in
            !part.isEmpty && part.count <= 256 && part.allSatisfy { $0.isASCII && ($0.isLetter || $0.isNumber || "_.:-".contains($0)) }
        }) else { return nil }
        let (machine, space, sid) = (parts[0], parts[1], parts[2])
        guard space != "health" else { return nil }
        return SessionKey(machineID: machine, sessionID: sid)
    }
}
