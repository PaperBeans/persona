import Foundation

/// A message to land on once its chat is open (a hub link's `?at=`, #2696): taken by that chat's view.
struct MessageJump: Equatable {
    var key: SessionKey
    var item: String
    var id = UUID() // the same message linked twice still moves there again
}

/// Where a hub link leads in the app (#2696, HubPageLink): which paired group it is a page of, and the key it opens.
extension AppModel {
    /// Every address `group`'s hubs are known by: what was paired, where the socket is, the mesh's endpoints and the
    /// hub's `public_url` (the one its links are written with, often not the address this device uses).
    func hubAddresses(_ group: String) -> [URL] {
        var out: [URL] = []
        let members = registry.members(group)
        out += members.map(\.config.url)
        out += members.flatMap { ($0.mesh ?? []).flatMap { ($0.endpoints ?? []).compactMap(URL.init(string:)) } }
        if let l = link(group) {
            out.append(l.config.url)
            if let u = l.hello.publicURL { out.append(u) }
            out += (l.hello.mesh ?? []).flatMap { ($0.endpoints ?? []).compactMap(URL.init(string:)) }
        }
        return out
    }

    /// The group and page a link is, `preferring` a group when two answer to the same address; nil: not a page of a
    /// paired hub the app has natively (it opens as a web page, as before).
    func hubPage(_ url: URL, preferring group: String? = nil) -> (group: String, page: HubPage)? {
        let groups = links.filter { !$0.isDemo }.map(\.group)
        let order = (group.map { [$0] } ?? []) + groups.filter { $0 != group }
        for g in order where link(g) != nil {
            if let p = HubPageLink.page(url, hubs: hubAddresses(g)) { return (g, p) }
        }
        return nil
    }

    /// Whether a port link (a link to the agent's machine, PortLinks) is really one of a paired hub's pages: the hub runs
    /// on that machine, or is the phone's own localhost (a mock hub).
    func isHubPage(_ l: PortLink, hostnames: [String]) -> Bool {
        let scheme = l.secure ? "https" : "http"
        return (["127.0.0.1", "localhost"] + hostnames).contains { host in
            URL(string: "\(scheme)://\(host):\(l.port)\(l.path)").flatMap { hubPage($0) } != nil
        }
    }

    /// The key of a session a link names in `group`: its full id from the session list when the link has a prefix of
    /// one (the web accepts short ids, #2464), else as written.
    func linkedSession(_ group: String, machine: String, session: String) -> SessionKey {
        let sessions = sessionsByMachine[MachineRef(group: group, id: machine)] ?? []
        if !sessions.contains(where: { $0.sessionID == session }) {
            let matches = sessions.filter { $0.sessionID.hasPrefix(session) }
            if session.count >= 6, matches.count == 1 { return SessionKey(group: group, machineID: machine, sessionID: matches[0].sessionID) }
        }
        return SessionKey(group: group, machineID: machine, sessionID: session)
    }

    /// The key of a Persona conversation a link names (`/persona/<id>`): the session of that id in the group's lists, else
    /// on the assistant's machine when the group is the hub-level one; nil when neither says where it is.
    func linkedConversation(_ group: String, id: String) -> SessionKey? {
        for (ref, sessions) in sessionsByMachine where ref.group == group {
            if let s = sessions.first(where: { $0.sessionID == id }) { return SessionKey(group: group, machineID: ref.id, sessionID: s.sessionID) }
        }
        guard group == hub?.group, let config = assistantConfig, config.main == id else { return nil }
        return config.mainKey(group: group)
    }

    /// Opens a chat a link leads to, at `item` when it names one: a Persona conversation in the Persona tab, an agent's
    /// in Agents (as a notification does).
    func openLinked(_ key: SessionKey, item: String?) {
        messageJump = item.map { MessageJump(key: key, item: $0) }
        if session(key)?.assistant == true || key == assistantMain {
            openAssistantConversation(key)
        } else {
            #if os(macOS)
            MacShellState.shared.willOpen()
            #endif
            open(key)
        }
    }
}
