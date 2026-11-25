import Darwin
import Foundation

/// The menu-bar app and its helper must read the same state root; selecting it never moves or deletes state.
nonisolated enum LinkStatePaths {
    private static var previousHomeKey: String { LegacyNames.product.uppercased() + "_HOME" }

    static func serviceEnvironment(home: URL, environment: [String: String]) -> [String: String] {
        if environment["PERSONA_HOME"] != nil || environment[previousHomeKey] != nil { return environment }
        let names = ["com.persona.node.plist", "com." + LegacyNames.product + ".node.plist"]
        for name in names {
            let plist = home.appendingPathComponent("Library/LaunchAgents/" + name)
            guard let data = try? Data(contentsOf: plist),
                  let value = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
                  let stored = value["EnvironmentVariables"] as? [String: String],
                  let root = stored["PERSONA_HOME"] ?? stored[previousHomeKey] else { continue }
            var result = environment
            result["PERSONA_HOME"] = root
            return result
        }
        return environment
    }

    static func root(home: URL, environment: [String: String]) -> URL {
        let explicit = environment["PERSONA_HOME"] ?? environment[previousHomeKey] ?? ""
        if !explicit.isEmpty {
            let path: String
            if explicit == "~" { path = home.path }
            else if explicit.hasPrefix("~/") { path = home.path + "/" + explicit.dropFirst(2) }
            else if explicit.hasPrefix("/") { path = explicit }
            else { path = home.path + "/" + explicit }
            return URL(fileURLWithPath: clean(path), isDirectory: true)
        }
        let current = home.appendingPathComponent(".persona", isDirectory: true)
        let previous = home.appendingPathComponent("." + LegacyNames.product, isDirectory: true)
        var old = stat()
        guard lstat(previous.path, &old) == 0 else { return current }
        var a = stat(), b = stat()
        if stat(current.path, &a) == 0, stat(previous.path, &b) == 0,
           a.st_dev == b.st_dev, a.st_ino == b.st_ino { return current }
        return previous
    }

    /// Match filepath.Clean without resolving symlinks through a custom root.
    private static func clean(_ path: String) -> String {
        var components: [Substring] = []
        for component in path.split(separator: "/") {
            if component == "." { continue }
            if component == ".." { if !components.isEmpty { components.removeLast() } }
            else { components.append(component) }
        }
        return "/" + components.joined(separator: "/")
    }
}
