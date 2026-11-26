import Foundation
import PersonaWire

/// One file changed by a completed Agent exchange's own successful edits (#12016, matching web `lib/turn-changes.ts`
/// and server `transcript/fold_changes.go`, shared fixture `spec/fixtures/fold-changes/cases.json`).
nonisolated struct TurnChangedFile: Identifiable, Hashable, Sendable {
    /// Normalized path (resolved against the session folder when relative).
    var path: String
    /// Relative to the session folder when inside it; otherwise `path`.
    var shown: String
    var add: Int
    var del: Int
    /// False when the hub clipped one of this file's edit inputs for good: its counts are omitted.
    var exact: Bool

    var id: String { path }
}

/// The files a completed Agent exchange changed, in first-edited order, with combined line counts.
nonisolated struct TurnChangeSet: Hashable, Sendable {
    var files: [TurnChangedFile]
    var add: Int
    var del: Int
    /// True only when every file's counts are exact (no clipped edit inputs).
    var exact: Bool
}

/// Pure rules for computing a completed Agent exchange's changed files and line counts, matching the hub's
/// `transcript.foldChanges` and web's `turnChanges` across all `spec/fixtures/fold-changes` cases.
nonisolated enum TurnChanges {
    /// Files shown before the expansion toggle (matching web `VISIBLE_FILES`).
    static let initialVisibleCount = 3

    /// Only successful Edit, Write, and MultiEdit tool calls count (Codex file changes arrive as Edit with `diff`).
    static let editTools: Set<String> = ["Edit", "Write", "MultiEdit"]

    /// Past these line-diff limits, count all old lines removed and all new lines added (matching Go/web Myers caps).
    static let diffLineLimit = 60_000
    static let diffMaxEdits = 6_000

    private static let clipPrefixBytes = Array("\n…（已截断，共 ".utf8) // i18n-ignore: matches the hub's clip note
    private static let clipSuffixBytes = Array(" 字节）".utf8) // i18n-ignore: matches the hub's clip note
    private static let clipApproxBytes = Array("≈".utf8) // i18n-ignore: matches the hub's clip note

    private struct LineCounts: Hashable, Sendable {
        var add: Int
        var del: Int
    }

    private struct LineMemoKey: Hashable, Sendable {
        var oldText: String
        var newText: String
    }

    private struct PatchMemoKey: Hashable, Sendable {
        var patch: String
        var change: String
    }

    /// Bounded value cache (entry + byte budget, skipping huge payloads) so repeated chat line rebuilds skip
    /// re-running Myers diffs on unchanged edits without retaining unbounded edit bodies (P4, P11).
    private final class CountCache: @unchecked Sendable {
        static let shared = CountCache()
        private let lock = NSLock()
        private var lines: [LineMemoKey: LineCounts] = [:]
        private var patches: [PatchMemoKey: LineCounts] = [:]
        private var lineBytes = 0
        private var patchBytes = 0
        private let maxEntries = 256
        private let maxSingleBytes = 200_000
        private let maxTotalBytes = 2_000_000

        func lineCounts(old: String, new: String) -> LineCounts {
            let cost = old.utf8.count + new.utf8.count
            guard cost <= maxSingleBytes else {
                return TurnChanges.computeLineCounts(old, new)
            }
            let key = LineMemoKey(oldText: old, newText: new)
            lock.lock()
            if let hit = lines[key] {
                lock.unlock()
                return hit
            }
            lock.unlock()

            let computed = TurnChanges.computeLineCounts(old, new)

            lock.lock()
            if lines.count >= maxEntries || lineBytes + cost > maxTotalBytes {
                lines.removeAll(keepingCapacity: true)
                lineBytes = 0
            }
            if lines[key] == nil { lineBytes += cost }
            lines[key] = computed
            lock.unlock()
            return computed
        }

        func patchCounts(patch: String, change: String) -> LineCounts {
            let cost = patch.utf8.count + change.utf8.count
            guard cost <= maxSingleBytes else {
                return TurnChanges.computePatchCounts(patch, change: change)
            }
            let key = PatchMemoKey(patch: patch, change: change)
            lock.lock()
            if let hit = patches[key] {
                lock.unlock()
                return hit
            }
            lock.unlock()

            let computed = TurnChanges.computePatchCounts(patch, change: change)

            lock.lock()
            if patches.count >= maxEntries || patchBytes + cost > maxTotalBytes {
                patches.removeAll(keepingCapacity: true)
                patchBytes = 0
            }
            if patches[key] == nil { patchBytes += cost }
            patches[key] = computed
            lock.unlock()
            return computed
        }
    }

    /// Files changed by an exchange's work items in first-edited order, or `nil` if there are no successful edits
    /// or if any folded work run lacks `changes` metadata (older hub: never claim a partial total).
    static func of(_ work: [WorkItem], cwd: String? = nil) -> TurnChangeSet? {
        var files: [TurnChangedFile] = []
        var indexByPath: [String: Int] = [:]

        func append(named: String, add: Int, del: Int, exact: Bool) {
            let normalized = changePath(named, cwd: cwd)
            guard !normalized.isEmpty else { return }
            if let index = indexByPath[normalized] {
                files[index].add += add
                files[index].del += del
                files[index].exact = files[index].exact && exact
            } else {
                indexByPath[normalized] = files.count
                files.append(TurnChangedFile(
                    path: normalized,
                    shown: relative(to: cwd, path: normalized),
                    add: add,
                    del: del,
                    exact: exact
                ))
            }
        }

        for item in work {
            switch item {
            case .step(.folded(let folded)):
                guard let changes = folded.fold?.changes else { return nil }
                for change in changes where !change.path.isEmpty {
                    append(named: change.path, add: change.add, del: change.del, exact: change.exact)
                }
            case .step(.call(let call)):
                let toolName = call.use.toolName ?? ""
                guard editTools.contains(toolName), let result = call.result, !(result.isError ?? false) else { continue }
                guard let path = editPath(in: call.input) else { continue }
                let counts = editCounts(toolName: toolName, input: call.input)
                append(named: path, add: counts.add, del: counts.del, exact: !isClipped(call.input))
            default:
                continue
            }
        }

        guard !files.isEmpty else { return nil }
        let totalAdd = files.reduce(0) { $0 + $1.add }
        let totalDel = files.reduce(0) { $0 + $1.del }
        let allExact = files.allSatisfy(\.exact)
        return TurnChangeSet(files: files, add: totalAdd, del: totalDel, exact: allExact)
    }

    /// The subset of files currently shown in the card (three initially, or all when expanded).
    static func visibleFiles(in changes: TurnChangeSet, expanded: Bool) -> [TurnChangedFile] {
        expanded ? changes.files : Array(changes.files.prefix(initialVisibleCount))
    }

    /// The file path named by an edit tool's input (`file_path`, `notebook_path`, `path`), trimmed.
    static func editPath(in input: JSONValue) -> String? {
        for key in ["file_path", "notebook_path", "path"] {
            if let raw = input[key]?.string {
                let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
                if !trimmed.isEmpty { return trimmed }
            }
        }
        return nil
    }

    /// Lines added and removed by one edit tool call.
    static func editCounts(toolName: String, input: JSONValue) -> (add: Int, del: Int) {
        switch toolName {
        case "Edit":
            if let diff = input["diff"]?.string {
                let change = input["change"]?.string ?? ""
                let counts = CountCache.shared.patchCounts(patch: diff, change: change)
                return (counts.add, counts.del)
            }
            let oldVal = input["old_string"]?.string
            let newVal = input["new_string"]?.string
            guard oldVal != nil || newVal != nil else { return (0, 0) }
            let counts = CountCache.shared.lineCounts(old: oldVal ?? "", new: newVal ?? "")
            return (counts.add, counts.del)
        case "Write":
            guard let content = input["content"]?.string else { return (0, 0) }
            let counts = CountCache.shared.lineCounts(old: "", new: content)
            return (counts.add, counts.del)
        case "MultiEdit":
            guard let edits = input["edits"]?.array else { return (0, 0) }
            var totalAdd = 0
            var totalDel = 0
            for edit in edits {
                let oldVal = edit["old_string"]?.string ?? ""
                let newVal = edit["new_string"]?.string ?? ""
                let counts = CountCache.shared.lineCounts(old: oldVal, new: newVal)
                totalAdd += counts.add
                totalDel += counts.del
            }
            return (totalAdd, totalDel)
        default:
            return (0, 0)
        }
    }

    /// Lines added and removed between two strings using the shared UTF-8 newline and Myers forward-pass rules.
    static func lineCounts(_ oldText: String, _ newText: String) -> (add: Int, del: Int) {
        let counts = CountCache.shared.lineCounts(old: oldText, new: newText)
        return (counts.add, counts.del)
    }

    /// Lines added and removed by a Codex unified diff patch (or whole-file content without hunks).
    static func patchCounts(_ patch: String, change: String = "") -> (add: Int, del: Int) {
        let counts = CountCache.shared.patchCounts(patch: patch, change: change)
        return (counts.add, counts.del)
    }

    /// Whether any string inside a tool input ends with the hub's truncation marker (`clip.go`).
    static func isClipped(_ value: JSONValue) -> Bool {
        switch value {
        case .string(let text):
            return hasClippedSuffix(text)
        case .array(let items):
            return items.contains(where: isClipped)
        case .object(let dict):
            return dict.values.contains(where: isClipped)
        default:
            return false
        }
    }

    /// Whether `text` ends with the hub's truncation marker (`clip.go`). Uses raw UTF-8 bytes so `\r\n` does not collapse
    /// into a single Swift grapheme cluster and evade suffix matching.
    static func hasClippedSuffix(_ text: String) -> Bool {
        let utf8 = text[...].utf8
        guard utf8.count > clipPrefixBytes.count + clipSuffixBytes.count else { return false }

        func dropSuffix(_ slice: Substring.UTF8View, _ suffix: [UInt8]) -> Substring.UTF8View? {
            guard slice.count >= suffix.count else { return nil }
            let start = slice.index(slice.endIndex, offsetBy: -suffix.count)
            guard slice[start...].elementsEqual(suffix) else { return nil }
            return slice[..<start]
        }

        guard var remaining = dropSuffix(utf8, clipSuffixBytes) else { return false }
        var digitCount = 0
        while let last = remaining.last, last >= UInt8(ascii: "0"), last <= UInt8(ascii: "9") {
            remaining = remaining[..<remaining.index(before: remaining.endIndex)]
            digitCount += 1
        }
        guard digitCount > 0 else { return false }
        if let withoutApprox = dropSuffix(remaining, clipApproxBytes) {
            remaining = withoutApprox
        }
        return dropSuffix(remaining, clipPrefixBytes) != nil
    }

    /// Resolves `named` against `cwd` when relative, then normalizes `.`, `..`, and redundant slashes.
    static func changePath(_ named: String, cwd: String?) -> String {
        if let cwd, !cwd.isEmpty, !isAbsolute(named) {
            return normalizePath(joinPath(cwd, named))
        }
        return normalizePath(named)
    }

    /// Relative to `root` when `path` is strictly inside it; otherwise `path` unchanged.
    static func relative(to root: String?, path: String) -> String {
        guard let root, !root.isEmpty else { return path }
        let normalizedRoot = trimTrailingSlashes(normalizePath(root))
        guard !normalizedRoot.isEmpty else { return path }
        let normalizedPath = normalizePath(path)
        let prefix = normalizedRoot + "/"
        if normalizedPath.hasPrefix(prefix) {
            return String(normalizedPath.dropFirst(prefix.count))
        }
        return normalizedPath
    }

    /// True when `path` is `root` itself or inside `root` (rejecting sibling prefixes such as `/repo-other`).
    static func isInside(_ root: String, path: String) -> Bool {
        guard !root.isEmpty else { return false }
        let r = trimTrailingSlashes(normalizePath(root))
        let p = trimTrailingSlashes(normalizePath(path))
        if p == r { return true }
        return p.hasPrefix(r + "/")
    }

    /// Normalizes a POSIX-style path (removing `.`, redundant `/`, and resolving `..` where possible).
    static func normalizePath(_ path: String) -> String {
        let abs = path.hasPrefix("/")
        var parts: [Substring] = []
        for part in path.split(separator: "/", omittingEmptySubsequences: false) {
            if part.isEmpty || part == "." { continue }
            if part == "..", let last = parts.last, last != "..", last != "~" {
                parts.removeLast()
            } else if part != ".." || !abs {
                parts.append(part)
            }
        }
        return (abs ? "/" : "") + parts.joined(separator: "/")
    }

    static func isAbsolute(_ path: String) -> Bool {
        if path.hasPrefix("/") || path.hasPrefix("~") || path.hasPrefix("\\\\") { return true }
        let utf8 = path.utf8
        guard utf8.count >= 3 else { return false }
        var iter = utf8.makeIterator()
        guard let first = iter.next(), let second = iter.next(), let third = iter.next() else { return false }
        let isLetter = (first >= UInt8(ascii: "A") && first <= UInt8(ascii: "Z"))
            || (first >= UInt8(ascii: "a") && first <= UInt8(ascii: "z"))
        return isLetter && second == UInt8(ascii: ":") && (third == UInt8(ascii: "/") || third == UInt8(ascii: "\\"))
    }

    private static func joinPath(_ dir: String, _ name: String) -> String {
        let trimmedDir = trimTrailingSlashes(dir)
        var trimmedName = name[...]
        while trimmedName.hasPrefix("/") { trimmedName = trimmedName.dropFirst() }
        return trimmedDir + "/" + trimmedName
    }

    private static func trimTrailingSlashes(_ path: String) -> String {
        var result = path[...]
        while result.hasSuffix("/") { result = result.dropLast() }
        return String(result)
    }

    // MARK: - Line diff & patch counting (raw UTF-8 bytes so `\r\n` never collapses into one Swift Character)

    private static func splitEditLines(_ oldText: String, _ newText: String) -> ([ArraySlice<UInt8>], [ArraySlice<UInt8>]) {
        let nl = UInt8(ascii: "\n")
        let oldBytes = Array(oldText.utf8)
        let newBytes = Array(newText.utf8)
        let oldEnds = oldBytes.last == nl
        let newEnds = newBytes.last == nl

        func split(_ bytes: [UInt8], stripTrailingNL: Bool) -> [ArraySlice<UInt8>] {
            guard !bytes.isEmpty else { return [] }
            let slice = stripTrailingNL ? bytes.dropLast() : bytes[...]
            return slice.split(separator: nl, omittingEmptySubsequences: false)
        }

        let a = split(oldBytes, stripTrailingNL: oldEnds && (newBytes.isEmpty || newEnds))
        let b = split(newBytes, stripTrailingNL: newEnds && (oldBytes.isEmpty || oldEnds))
        return (a, b)
    }

    private static func computeLineCounts(_ oldText: String, _ newText: String) -> LineCounts {
        let (a, b) = splitEditLines(oldText, newText)
        guard let d = editDistance(a, b) else {
            return LineCounts(add: b.count, del: a.count)
        }
        return LineCounts(add: (b.count - a.count + d) / 2, del: (a.count - b.count + d) / 2)
    }

    /// Myers forward pass (no traceback), interning lines to integer IDs for fast comparison.
    private static func editDistance(_ a: [ArraySlice<UInt8>], _ b: [ArraySlice<UInt8>]) -> Int? {
        let n = a.count
        let m = b.count
        if n + m > diffLineLimit { return nil }
        if n == 0 { return m <= diffMaxEdits ? m : nil }
        if m == 0 { return n <= diffMaxEdits ? n : nil }

        var ids: [ArraySlice<UInt8>: Int] = [:]
        ids.reserveCapacity(n + m)
        var nextID = 0
        var aIDs: [Int] = []
        aIDs.reserveCapacity(n)
        for line in a {
            if let existing = ids[line] {
                aIDs.append(existing)
            } else {
                ids[line] = nextID
                aIDs.append(nextID)
                nextID += 1
            }
        }
        var bIDs: [Int] = []
        bIDs.reserveCapacity(m)
        for line in b {
            if let existing = ids[line] {
                bIDs.append(existing)
            } else {
                ids[line] = nextID
                bIDs.append(nextID)
                nextID += 1
            }
        }

        let maxD = min(n + m, diffMaxEdits)
        let offset = maxD + 1
        var v = [Int](repeating: 0, count: 2 * maxD + 3)

        for d in 0...maxD {
            var k = -d
            while k <= d {
                var x: Int
                if k == -d || (k != d && v[offset + k - 1] < v[offset + k + 1]) {
                    x = v[offset + k + 1]
                } else {
                    x = v[offset + k - 1] + 1
                }
                var y = x - k
                while x < n && y < m && aIDs[x] == bIDs[y] {
                    x += 1
                    y += 1
                }
                v[offset + k] = x
                if x >= n && y >= m {
                    return d
                }
                k += 2
            }
        }
        return nil
    }

    private static let hunkPrefix = Array("@@".utf8)
    private static let diffGitPrefix = Array("diff --git".utf8)
    private static let indexPrefix = Array("index ".utf8)
    private static let oldHeaderPrefix = Array("--- ".utf8)
    private static let newHeaderPrefix = Array("+++ ".utf8)

    private static func computePatchCounts(_ patch: String, change: String) -> LineCounts {
        let nl = UInt8(ascii: "\n")
        let bytes = Array(patch.utf8)
        let lines = bytes.split(separator: nl, omittingEmptySubsequences: false)
        let hasHunks = lines.contains { $0.starts(with: hunkPrefix) }

        if !hasHunks {
            if bytes.isEmpty { return LineCounts(add: 0, del: 0) }
            let trimmed = bytes.last == nl ? bytes.dropLast() : bytes[...]
            let count = trimmed.split(separator: nl, omittingEmptySubsequences: false).count
            return change == "delete" ? LineCounts(add: 0, del: count) : LineCounts(add: count, del: 0)
        }

        var inHunk = false
        var add = 0
        var del = 0
        for line in lines {
            if line.starts(with: diffGitPrefix) {
                inHunk = false
            } else if !inHunk && (line.starts(with: indexPrefix) || line.starts(with: oldHeaderPrefix) || line.starts(with: newHeaderPrefix)) {
                continue
            } else if line.starts(with: hunkPrefix) {
                inHunk = true
            } else if inHunk, let first = line.first {
                if first == UInt8(ascii: "+") {
                    add += 1
                } else if first == UInt8(ascii: "-") {
                    del += 1
                }
            }
        }
        return LineCounts(add: add, del: del)
    }
}

/// Destination chosen when tapping the Changed Files card's Review button or a file row.
nonisolated enum TurnReviewAction: Equatable, Sendable {
    /// Open branch review, optionally focusing `targetFile` (with fallback to `FileViewer` if absent/unavailable).
    case review(targetFile: String?)
    /// Open `FileViewer` directly (hub lacks review, session has no workspace folder, or file is outside the workspace).
    case file(String)
    /// Review button unavailable.
    case none
}

/// Pure routing for the Changed Files card's Review button and file rows (#12016).
nonisolated enum TurnReviewRouting {
    static func route(targetFile: String?, canReview: Bool, cwd: String?) -> TurnReviewAction {
        guard let targetFile else {
            guard canReview, let cwd, !cwd.isEmpty else { return .none }
            return .review(targetFile: nil)
        }
        if canReview, let cwd, !cwd.isEmpty, TurnChanges.isInside(cwd, path: targetFile) {
            return .review(targetFile: targetFile)
        }
        return .file(targetFile)
    }
}
