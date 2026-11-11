import Foundation

/// A path a message names (docs/apple/ios.md「File viewer」), with the line it points at: `path:12`, `path:12:5`,
/// `path:12-20` (the first line), `path#L12`. The same rules as the web's `web/src/lib/path-refs.ts`.
public nonisolated struct PathRef: Hashable, Sendable {
    /// Where it is in the text (UTF-16): the path and its line.
    public var range: NSRange
    /// The path as written, without the line: what the machine is asked about (`FilePresence`).
    public var path: String
    public var line: Int?
    public var column: Int?
    /// A code span that is one path (`~/My Notes/a.md`), not an ASCII path found in text.
    public var whole = false
}

/// Finds the paths in text:
///
/// - `~/x`, `/x/y` (two parts at least: `/tmp` alone is a word), or relative with a folder and an extension (`src/a.ts`;
///   a letter-led extension, so `deepseek/v4.1` is not a file).
/// - Outside backticks a path is ASCII: Chinese text or full-width punctuation right after it ends it (「/tmp/a.md里」),
///   and a sentence's full stop after it is not part of it. In backticks the whole span is one, whatever it is written
///   in (「~/报告/第四季度.docx」); the ASCII paths in a span are candidates too (「/a.md 和 /b.md」): what is there decides.
/// - Not inside a URL or a longer word.
public nonisolated enum PathRefs {
    private static let seg = "[A-Za-z0-9_.@+\\-]+"
    private static let line = #"(?::(\d{1,7})(?::(\d{1,5}))?(?:[-–]\d{1,7})?|#L(\d{1,7})(?:-L?\d{1,7})?)?"#
    private static let ascii = try! NSRegularExpression(pattern:
        #"(?<![A-Za-z0-9_/:.\-~])(~(?:/"# + seg + #")+/?|/"# + seg + "(?:/" + seg + #")+/?|[A-Za-z0-9_.@\-]+(?:/"# + seg
        + #")+\.[A-Za-z][A-Za-z0-9]{0,7})"# + line + #"(?![A-Za-z0-9_/\-])"#)
    private static let codeSpan = try! NSRegularExpression(pattern: "`([^`\\n]+)`")
    /// A line at the end of a code span (`~/My Notes/a.md:3`).
    private static let trailingLine = try! NSRegularExpression(pattern: "(?:" + line.dropFirst(3).dropLast(2) + ")$")
    /// A whole code span that is a path: `~/` or `/` then words separated by single spaces.
    private static let whole = try! NSRegularExpression(pattern: "^~?/[^\\s`]+(?: [^\\s`]+)*$")
    private static let fullStops = try! NSRegularExpression(pattern: "(?<=[^./])\\.+$")

    /// The paths in plain text, in order (the longer first at one place), at most `limit`.
    public static func find(in text: String, limit: Int = 40) -> [PathRef] {
        let ns = text as NSString
        var out: [PathRef] = []
        for m in codeSpan.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let inner = m.range(at: 1)
            if let r = codePathRef(ns.substring(with: inner)) { out.append(shifted(r, by: inner.location)) }
        }
        for r in asciiRefs(in: text) where !out.contains(where: { $0.range == r.range }) { out.append(r) }
        out.sort { $0.range.location != $1.range.location ? $0.range.location < $1.range.location : $0.range.length > $1.range.length }
        return Array(out.prefix(limit))
    }

    /// The ASCII paths of text, outside backticks or in them.
    public static func asciiRefs(in text: String) -> [PathRef] {
        let ns = text as NSString
        return ascii.matches(in: text, range: NSRange(location: 0, length: ns.length)).compactMap { ref(m: $0, in: ns) }
    }

    /// The path a whole code span is (`/tmp/a.md`, `src/a.ts:12`, 「~/报告/第四季度.docx」), or nil; its range is in `code`.
    public static func codePathRef(_ code: String) -> PathRef? {
        let ns = code as NSString
        let lead = ns.length - (code.drop { $0 == " " } as Substring).utf16.count
        let t = code.trimmingCharacters(in: .whitespaces)
        let tn = t as NSString
        guard !t.isEmpty, !t.hasPrefix("//"), !t.contains("://") else { return nil }
        // Whole: the line taken off its end, the rest a path from the root or home, Chinese and spaces too.
        var path = t, lineNo: Int?, col: Int?
        if let m = trailingLine.firstMatch(in: t, range: NSRange(location: 0, length: tn.length)), m.range.length > 0 {
            path = tn.substring(to: m.range.location)
            lineNo = number(m, 1, tn) ?? number(m, 3, tn)
            col = lineNo == nil ? nil : number(m, 2, tn)
        }
        let pn = path as NSString
        if whole.firstMatch(in: path, range: NSRange(location: 0, length: pn.length)) != nil {
            let parts = path.split(separator: "/").count
            if parts >= (path.hasPrefix("~") ? 1 : 2), !isRoot(path) {
                return PathRef(range: NSRange(location: lead, length: tn.length), path: path, line: lineNo == 0 ? nil : lineNo,
                               column: lineNo == nil || lineNo == 0 ? nil : col, whole: true)
            }
        }
        // Or one ASCII path that is the whole span (`ios/Persona/Foo.swift`).
        if var r = asciiRefs(in: t).first, r.range.location == 0, r.range.length == tn.length {
            r.whole = true
            return shifted(r, by: lead)
        }
        return nil
    }

    /// Each path once, in order, for asking the machine.
    public static func unique(_ refs: [PathRef], limit: Int = 24) -> [String] { unique(refs.map(\.path), limit: limit) }

    public static func unique(_ paths: [String], limit: Int) -> [String] {
        var out: [String] = []
        for p in paths where !out.contains(p) {
            out.append(p)
            if out.count >= limit { break }
        }
        return out
    }

    /// The refs to link: the ones `linkable` says yes to, in order, none overlapping (the first, the longer at one place).
    public static func pick(_ refs: [PathRef], linkable: (String) -> Bool) -> [PathRef] {
        var out: [PathRef] = []
        var at = 0
        for r in refs where r.range.location >= at && linkable(r.path) {
            out.append(r)
            at = NSMaxRange(r.range)
        }
        return out
    }

    /// A streaming text without its last word, which may be a path still being written (`/tmp/jar…`).
    public static func written(_ text: String) -> String {
        guard let r = text.range(of: #"\s\S*$"#, options: .regularExpression) else { return "" }
        return String(text[..<r.lowerBound])
    }

    private static func ref(m: NSTextCheckingResult, in ns: NSString) -> PathRef? {
        var path = ns.substring(with: m.range(at: 1))
        let hasLine = m.range.length > m.range(at: 1).length
        // A sentence's full stop (or an ellipsis) is not part of the path, unless the path is only dots.
        if !hasLine { path = fullStops.stringByReplacingMatches(in: path, range: NSRange(location: 0, length: (path as NSString).length), withTemplate: "") }
        guard !path.isEmpty, path != "~", !isRoot(path) else { return nil }
        let lineNo = number(m, 2, ns) ?? number(m, 4, ns)
        let length = hasLine ? m.range.length : (path as NSString).length
        return PathRef(range: NSRange(location: m.range.location, length: length), path: path,
                       line: lineNo == 0 ? nil : lineNo, column: lineNo == nil || lineNo == 0 ? nil : number(m, 3, ns))
    }

    private static func isRoot(_ p: String) -> Bool { p.range(of: "^~?/+$", options: .regularExpression) != nil }

    private static func number(_ m: NSTextCheckingResult, _ group: Int, _ ns: NSString) -> Int? {
        let r = m.range(at: group)
        return r.location == NSNotFound ? nil : Int(ns.substring(with: r))
    }

    private static func shifted(_ r: PathRef, by n: Int) -> PathRef {
        var r = r
        r.range.location += n
        return r
    }
}

/// The link a path in a message is drawn with (`persona-path:<path>?line=N`): the chat's `openURL` opens it in the
/// file viewer, at its line.
public nonisolated enum PathLinkURL {
    public static let scheme = "persona-path"
    /// ASCII letters and digits and `/-._~` stay; everything else (spaces, Chinese, parentheses) is percent-encoded,
    /// so the link stays one Markdown destination.
    private static let kept = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789/-._~")

    public static func make(_ path: String, line: Int?) -> URL? {
        guard let p = path.addingPercentEncoding(withAllowedCharacters: kept) else { return nil }
        return URL(string: scheme + ":" + p + (line.map { "?line=\($0)" } ?? ""))
    }

    public static func parse(_ url: URL) -> (path: String, line: Int?)? {
        guard url.scheme == scheme else { return nil }
        var rest = String(url.absoluteString.dropFirst(scheme.count + 1))
        var line: Int?
        if let q = rest.range(of: "?line=") {
            line = Int(rest[q.upperBound...])
            rest = String(rest[..<q.lowerBound])
        }
        guard let path = rest.removingPercentEncoding, !path.isEmpty else { return nil }
        return (path, line)
    }
}

/// An answer's Markdown, ready to have its paths linked (`linked`): where the paths are in it, outside code blocks,
/// formulas and links (written or bare), and the code spans that are a whole path (the span is the link).
public nonisolated struct PathMarkdown: Sendable {
    public struct Spot: Sendable {
        public var range: NSRange
        public var ref: PathRef
        /// A whole code span: linked as written, backticks and all.
        public var code: Bool
    }

    public let source: String
    public let spots: [Spot]
    /// Each path once, for asking the machine.
    public let paths: [String]

    public init(_ source: String, limit: Int = 40) {
        self.source = source
        var spots: [Spot] = []
        var fence: MarkdownFence?
        var at = 0
        for line in source.split(separator: "\n", omittingEmptySubsequences: false).map(String.init) {
            defer { at += (line as NSString).length + 1 }
            let t = line.trimmingCharacters(in: .whitespaces)
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
                continue
            }
            if let f = MarkdownFence.opening(t) { fence = f; continue }
            if line.hasPrefix("    ") || line.hasPrefix("\t") { continue } // indented code (as `Links.markdown`)
            for s in Self.spots(in: line) {
                var s = s
                s.range.location += at
                s.ref.range.location += at
                spots.append(s)
            }
            if spots.count >= limit { break }
        }
        self.spots = Array(spots.prefix(limit))
        paths = PathRefs.unique(self.spots.map(\.ref))
    }

    /// The source with the paths `linkable` says yes to as links, and which paths those are. `plainCode`: a whole code
    /// span is linked as its words (Markdown's marks escaped), not as code: where code is drawn in the body's color
    /// (iOS, #2984), a path that opens keeps the link's.
    public func linked(plainCode: Bool = false, _ linkable: (String) -> Bool) -> (markdown: String, paths: Set<String>) {
        let ns = source as NSString
        var out = ""
        var at = 0
        var linked: Set<String> = []
        for s in spots where s.range.location >= at && linkable(s.ref.path) {
            guard let url = PathLinkURL.make(s.ref.path, line: s.ref.line) else { continue }
            out += ns.substring(with: NSRange(location: at, length: s.range.location - at))
            out += "[" + (s.code && plainCode ? Self.words(of: ns.substring(with: s.range)) : ns.substring(with: s.range)) + "](" + url.absoluteString + ")"
            at = NSMaxRange(s.range)
            linked.insert(s.ref.path)
        }
        guard !linked.isEmpty else { return (source, []) }
        out += ns.substring(from: at)
        return (out, linked)
    }

    /// A code span's words as Markdown text: the backticks off (and the one space CommonMark takes off each end),
    /// the marks Markdown reads inside a line escaped.
    static func words(of span: String) -> String {
        var inner = span.drop { $0 == "`" }
        while inner.last == "`" { inner = inner.dropLast() }
        var text = String(inner)
        if text.count > 2, text.hasPrefix(" "), text.hasSuffix(" "), !text.allSatisfy({ $0 == " " }) {
            text = String(text.dropFirst().dropLast())
        }
        return text.reduce(into: "") { out, c in
            if "\\`*_[]()<>!|~#$".contains(c) { out.append("\\") }
            out.append(c)
        }
    }

    // Links written ([text](url), images: the formulas are `![tex](persona-math:…)`), reference links, and <autolinks>
    // (bare links are made those by `Links.markdown` first).
    static let protected = try! NSRegularExpression(pattern: #"!?\[[^\]\n]*\]\([^)\n]*\)|!?\[[^\]\n]*\]\[[^\]\n]*\]|<[^\s<>]+>|^\s*\[[^\]\n]+\]:.*$"#)

    /// The paths of one line (outside a code block).
    private static func spots(in line: String) -> [Spot] {
        let ns = line as NSString
        let all = NSRange(location: 0, length: ns.length)
        let code = codeSpans(Array(line.utf16))
        let links = protected.matches(in: line, range: all).map(\.range)
            .filter { r in !code.contains { NSLocationInRange(r.location, $0.whole) } }
        func covered(_ r: NSRange) -> Bool {
            links.contains { NSIntersectionRange($0, r).length > 0 } || code.contains { NSIntersectionRange($0.whole, r).length > 0 }
        }
        var out: [Spot] = []
        for c in code where !links.contains(where: { NSIntersectionRange($0, c.whole).length > 0 }) {
            guard var r = PathRefs.codePathRef(ns.substring(with: c.inner)) else { continue }
            r.range.location += c.inner.location
            out.append(Spot(range: c.whole, ref: r, code: true))
        }
        for r in PathRefs.asciiRefs(in: line) where !covered(r.range) {
            out.append(Spot(range: r.range, ref: r, code: false))
        }
        return out.sorted { $0.range.location < $1.range.location }
    }

    /// A line's code spans: a run of backticks to the next run of the same length (the span, and what is in it).
    static func codeSpans(_ u: [UInt16]) -> [(whole: NSRange, inner: NSRange)] {
        var spans: [(NSRange, NSRange)] = []
        var i = 0
        while i < u.count {
            guard u[i] == 0x60 else { i += 1; continue }
            var n = 0
            while i + n < u.count, u[i + n] == 0x60 { n += 1 }
            var j = i + n
            var closed = false
            while j < u.count {
                if u[j] == 0x60 {
                    var m = 0
                    while j + m < u.count, u[j + m] == 0x60 { m += 1 }
                    if m == n {
                        spans.append((NSRange(location: i, length: j + m - i), NSRange(location: i + n, length: j - i - n)))
                        i = j + m
                        closed = true
                        break
                    }
                    j += m
                } else {
                    j += 1
                }
            }
            if !closed { i += n }
        }
        return spans
    }
}
