import Foundation

/// Bold and italics next to CJK punctuation, read as the web reads them (remark-cjk-friendly, #3863). CommonMark closes
/// `**` after punctuation only when a space or more punctuation follows, and opens it before punctuation only after a
/// space or punctuation; so a bold label written the Chinese way, a full-width colon and no space after it
/// (`**Note：**text`), kept its asterisks on the screen, and so did bold around corner brackets (`a**「stress」**b`) and
/// italics around full-width parentheses (`*（aside）*b`). MarkdownUI's cmark has no option for it, so before a reply is
/// parsed the punctuation at the inner edge of such a pair moves outside it: `**Note**：text`, `a「**stress**」b`,
/// `（*aside*）b` (in Chinese in `CJKEmphasisTests`). Only a pair of `*` runs on one line that CommonMark would leave
/// open, only where CJK text is involved (the punctuation moved, or the letter outside it), and only punctuation moves: a
/// bracket or quote only together with its partner (`**Plan（default）：**` moves just the `：`; with nothing else to
/// move the pair stays as written), and never a mark Markdown reads (an ASCII bracket, a backtick, an escaped character,
/// an image's `!`). Code spans, fences (in lists and quotes too), indented code, formulas, autolinks, bare URLs and link
/// destinations are left alone, and so are `_` runs (inside a word they never open anyway). What is copied stays the
/// source; this is only what is parsed.
public nonisolated enum CJKEmphasis {
    public static func apply(_ text: String) -> String {
        // Nothing to mend without an asterisk and something beyond ASCII and Latin. A CRLF text is left as it is: "\r\n"
        // is one Character, so `split(separator: "\n")` would keep its lines whole and its fences could not be told apart.
        guard text.contains("*"), !text.utf8.contains(13), text.unicodeScalars.contains(where: { $0.value >= 0x2010 }) else { return text }
        var out: [Substring] = []
        var changed = false
        /// Inside a fence: it, and how many quote markers deep it opened (a line with fewer ends the quote and the fence).
        var fence: (opener: MarkdownFence, quotes: Int)?
        /// Inside a `$$` / `\[` block: the line that ends it.
        var mathEnd: String?
        var inList = false
        var inCode = false
        var afterBlank = true
        /// The line before is a paragraph's: four spaces in under it go on with it, not code.
        var afterText = false
        for line in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if let open = fence {
                let (depth, inner) = Self.unquoted(trimmed, upTo: open.quotes)
                if depth == open.quotes {
                    if open.opener.isClosed(by: inner) { fence = nil }
                    out.append(line)
                    continue
                }
                fence = nil
            }
            if let end = mathEnd {
                if trimmed == end { mathEnd = nil }
                out.append(line)
                continue
            }
            if trimmed.isEmpty {
                afterBlank = true
                afterText = false
                out.append(line)
                continue
            }
            let indent = Self.indent(line)
            // Four spaces in, outside a list and not under a paragraph's line, is code.
            if indent >= 4, !inList, inCode || !afterText {
                inCode = true
                afterBlank = false
                out.append(line)
                continue
            }
            inCode = false
            let wasBlank = afterBlank
            afterBlank = false
            afterText = false
            if Self.listContent(trimmed) != nil { inList = true } else if wasBlank, indent == 0 { inList = false }
            // A fence opens at most three spaces in, or anywhere under a list item (a list's code is indented with
            // it), after a list marker, or in a quote.
            let (quotes, quoted) = Self.unquoted(trimmed)
            if indent < 4 || inList || quotes > 0, let open = MarkdownFence.opening(Self.listContent(quoted) ?? quoted) {
                fence = (open, quotes)
                out.append(line)
                continue
            }
            if trimmed == "$$" || trimmed == "\\[" {
                mathEnd = trimmed == "$$" ? "$$" : "\\]"
                out.append(line)
                continue
            }
            // A line that is one formula (`$$…$$`, `\[…\]`) becomes a math fence (`MathMarkdown`): TeX, not prose.
            if trimmed.hasPrefix("$$") || trimmed.hasPrefix("\\["), MathMarkdown.display(trimmed) != nil {
                out.append(line)
                continue
            }
            afterText = !trimmed.hasPrefix("#")
            if let mended = Self.mend(line) {
                out.append(Substring(mended))
                changed = true
            } else {
                out.append(line)
            }
        }
        return changed ? out.joined(separator: "\n") : text
    }

    private struct Run {
        var at: Int
        var count: Int
        var end: Int { at + count }
    }

    /// One line with its open pairs mended; nil when nothing changes.
    static func mend(_ line: Substring) -> String? {
        guard line.contains("*") else { return nil }
        let c = Array(line)
        // What is not prose: formulas, code spans, autolinks and tags, link destinations, bare URLs.
        var skip = [Bool](repeating: false, count: c.count)
        func mark(_ r: Range<Int>) { for k in r where k < c.count { skip[k] = true } }
        for span in MathMarkdown.inlineSpans(c) { mark(span.range) }
        var runs: [Run] = []
        var i = 0
        while i < c.count {
            if skip[i] { i += 1; continue }
            switch c[i] {
            case "\\":
                mark(i..<(i + 2)) // an escaped character is no delimiter and stays where it is
                i += 2
            case "`":
                let n = run(of: "`", in: c, at: i, skip)
                if let j = closingRun(of: "`", count: n, in: c, from: i + n) {
                    mark(i..<(j + n))
                    i = j + n
                } else {
                    i += n
                }
            case "<":
                if let j = c[(i + 1)...].firstIndex(of: ">"), !c[(i + 1)..<j].contains(where: \.isWhitespace) {
                    mark(i..<(j + 1))
                    i = j + 1
                } else {
                    i += 1
                }
            case "]":
                if i + 1 < c.count, c[i + 1] == "(", let j = c[(i + 2)...].firstIndex(of: ")") {
                    mark((i + 1)..<(j + 1))
                    i = j + 1
                } else {
                    i += 1
                }
            case "h", "H":
                if let j = urlEnd(c, at: i) {
                    mark(i..<j)
                    i = j
                } else {
                    i += 1
                }
            case "*":
                let n = run(of: "*", in: c, at: i, skip)
                runs.append(Run(at: i, count: n))
                i += n
            default:
                i += 1
            }
        }
        guard runs.count >= 2 else { return nil }

        func before(_ r: Run) -> Character? { r.at > 0 ? c[r.at - 1] : nil }
        func after(_ r: Run) -> Character? { r.end < c.count ? c[r.end] : nil }
        func leftFlanking(_ r: Run) -> Bool {
            let b = before(r), a = after(r)
            return !isSpace(a) && (!isPunctuation(a) || isSpace(b) || isPunctuation(b))
        }
        func rightFlanking(_ r: Run) -> Bool {
            let b = before(r), a = after(r)
            return !isSpace(b) && (!isPunctuation(b) || isSpace(a) || isPunctuation(a))
        }
        /// Whether the character at `p`, between a pair whose closer is `e`, may move across a delimiter: not escaped or
        /// code, and not a `!` that is an image's or would become one (`![a](u)`; `!**[a](u)` is no image).
        func movable(_ p: Int, closer e: Run) -> Bool {
            guard !skip[p], isMovable(c[p]) else { return false }
            return c[p] != "!" || (p + 1 == e.at ? after(e) : c[p + 1]) != "["
        }
        /// The bracket or quote that pairs with the one at `p`, looked for within `r`.
        func partner(_ p: Int, within r: Range<Int>) -> Int? {
            let forward = isOpening(c[p])
            guard forward || isClosing(c[p]) else { return nil }
            var depth = 0
            var q = forward ? p + 1 : p - 1
            while r.contains(q) {
                if !skip[q] {
                    if forward ? isOpening(c[q]) : isClosing(c[q]) {
                        depth += 1
                    } else if forward ? isClosing(c[q]) : isOpening(c[q]) {
                        if depth == 0 { return q }
                        depth -= 1
                    }
                }
                q += forward ? 1 : -1
            }
            return nil
        }

        /// Pairs in order, each run with the next of the same length; what moves out of each that needs it.
        var moves: [(open: Run, close: Run, lead: Int, trail: Int)] = []
        var k = 0
        pairs: while k + 1 < runs.count {
            let o = runs[k], e = runs[k + 1]
            let lo = o.end, hi = e.at
            guard o.count == e.count, o.count <= 3, lo < hi,
                  !c[lo..<hi].contains(where: { $0 == "|" || $0.isNewline }) else { k += 1; continue }
            let opens = leftFlanking(o), closes = rightFlanking(e)
            if opens && closes { k += 2; continue } // CommonMark reads it already
            var lead = 0
            while lo + lead < hi, movable(lo + lead, closer: e) { lead += 1 }
            var trail = 0
            while hi - trail - 1 >= lo + lead, movable(hi - trail - 1, closer: e) { trail += 1 }
            // A bracket or quote whose partner is in the words that stay inside stays too, and so does what is further
            // in: `**Plan（default）：**` moves the `：` alone, not the `）` away from its `（`.
            func paired(_ from: Range<Int>, _ to: Range<Int>) -> Int? {
                from.first { p in partner(p, within: lo..<hi).map(to.contains) == true }
            }
            while true {
                if let p = paired(lo..<(lo + lead), (lo + lead)..<(hi - trail)) {
                    lead = p - lo
                } else if let p = paired((hi - trail)..<hi, (lo + lead)..<(hi - trail)) {
                    trail = hi - p - 1
                } else {
                    break
                }
            }
            // Words must be left between them, not starting or ending with a space.
            guard lo + lead < hi - trail, !c[lo + lead].isWhitespace, !c[hi - trail - 1].isWhitespace else { k += 1; continue }
            var moveLead = 0, moveTrail = 0
            if !opens {
                guard lead > 0, c[lo..<(lo + lead)].contains(where: isCJKPunctuation) || isCJKLetter(before(o)) else { k += 1; continue pairs }
                moveLead = lead
            }
            if !closes {
                guard trail > 0, c[(hi - trail)..<hi].contains(where: isCJKPunctuation) || isCJKLetter(after(e)) else { k += 1; continue pairs }
                moveTrail = trail
            }
            // Brackets or quotes around the words move out together: 「**强调**」, not **「强调**」.
            if moveLead == 0, moveTrail > 0, paired((hi - trail)..<hi, lo..<(lo + lead)) != nil { moveLead = lead }
            if moveTrail == 0, moveLead > 0, paired(lo..<(lo + lead), (hi - trail)..<hi) != nil { moveTrail = trail }
            moves.append((o, e, moveLead, moveTrail))
            k += 2
        }
        guard !moves.isEmpty else { return nil }

        var out: [Character] = []
        out.reserveCapacity(c.count)
        var at = 0
        for m in moves {
            let lo = m.open.end, hi = m.close.at
            out += c[at..<m.open.at]
            out += c[lo..<(lo + m.lead)]
            out += c[m.open.at..<lo]
            out += c[(lo + m.lead)..<(hi - m.trail)]
            out += c[hi..<m.close.end]
            out += c[(hi - m.trail)..<hi]
            at = m.close.end
        }
        out += c[at...]
        return String(out)
    }

    // MARK: Characters

    /// CommonMark's Unicode whitespace for flanking; the start and end of the line count as whitespace.
    static func isSpace(_ ch: Character?) -> Bool { ch?.isWhitespace ?? true }

    /// ASCII punctuation or a Unicode punctuation character (cmark-gfm's rule: `：` counts, a Han letter does not).
    static func isPunctuation(_ ch: Character?) -> Bool {
        guard let s = ch?.unicodeScalars.first else { return false }
        if s.isASCII { return "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~".unicodeScalars.contains(s) }
        switch s.properties.generalCategory {
        case .connectorPunctuation, .dashPunctuation, .openPunctuation, .closePunctuation, .initialPunctuation,
             .finalPunctuation, .otherPunctuation: return true
        default: return false
        }
    }

    /// CJK punctuation: the CJK Symbols and Punctuation block, the full-width and vertical forms, the katakana middle dot.
    static func isCJKPunctuation(_ ch: Character) -> Bool {
        guard isPunctuation(ch), let s = ch.unicodeScalars.first else { return false }
        switch s.value {
        case 0x3001...0x303F, 0xFF00...0xFF65, 0xFE10...0xFE1F, 0xFE30...0xFE4F, 0x30FB: return true
        default: return false
        }
    }

    /// What may move across a delimiter: CJK punctuation, the quotes, dashes and ellipsis CJK text shares with others
    /// (“ ” ‘ ’ — …), and ASCII sentence punctuation. Never a bracket, backtick, `!` before a link or another mark
    /// Markdown reads, so moving it changes nothing but the emphasis.
    static func isMovable(_ ch: Character) -> Bool {
        if isCJKPunctuation(ch) { return true }
        guard ch.unicodeScalars.count == 1, let s = ch.unicodeScalars.first else { return false }
        switch s.value {
        case 0x2010...0x2027, 0x00B7: return isPunctuation(ch)
        default: return ":;,.?!".unicodeScalars.contains(s)
        }
    }

    /// A Han, kana, Hangul or bopomofo letter.
    static func isCJKLetter(_ ch: Character?) -> Bool {
        guard let s = ch?.unicodeScalars.first, !isPunctuation(ch) else { return false }
        switch s.value {
        case 0x1100...0x11FF, 0x3040...0x30FF, 0x3100...0x312F, 0x3130...0x318F, 0x31F0...0x31FF, 0x3400...0x4DBF,
             0x4E00...0x9FFF, 0xAC00...0xD7AF, 0xF900...0xFAFF, 0xFF66...0xFF9F, 0x20000...0x3FFFF: return true
        default: return false
        }
    }

    /// An opening bracket or quote (「 （ “ 《).
    static func isOpening(_ ch: Character) -> Bool {
        guard let s = ch.unicodeScalars.first else { return false }
        return [.openPunctuation, .initialPunctuation].contains(s.properties.generalCategory)
    }

    /// A closing bracket or quote (」 ） ” 》).
    static func isClosing(_ ch: Character) -> Bool {
        guard let s = ch.unicodeScalars.first else { return false }
        return [.closePunctuation, .finalPunctuation].contains(s.properties.generalCategory)
    }

    // MARK: Lines

    private static func run(of ch: Character, in c: [Character], at i: Int, _ skip: [Bool]) -> Int {
        var j = i
        while j < c.count, c[j] == ch, !skip[j] { j += 1 }
        return j - i
    }

    /// The next run of exactly `count` of `ch` (a code span's closer) on the line.
    private static func closingRun(of ch: Character, count: Int, in c: [Character], from start: Int) -> Int? {
        var j = start
        while j < c.count {
            guard c[j] == ch else { j += 1; continue }
            var n = 0
            while j + n < c.count, c[j + n] == ch { n += 1 }
            if n == count { return j }
            j += n
        }
        return nil
    }

    /// Where a bare `http(s)://` URL at `i` ends (`Links` makes it a link, so nothing in it is emphasis).
    private static func urlEnd(_ c: [Character], at i: Int) -> Int? {
        guard i == 0 || !(c[i - 1].isASCII && (c[i - 1].isLetter || c[i - 1].isNumber)) else { return nil }
        let head = String(c[i..<min(c.count, i + 8)]).lowercased()
        let scheme = head.hasPrefix("https://") ? 8 : head.hasPrefix("http://") ? 7 : 0
        guard scheme > 0 else { return nil }
        var j = i + scheme
        while j < c.count, c[j].isASCII, !c[j].isWhitespace, !"<>]`".contains(c[j]) { j += 1 }
        return j
    }

    /// Leading spaces, a tab counting as four.
    private static func indent(_ line: Substring) -> Int {
        var n = 0
        for ch in line {
            if ch == " " { n += 1 } else if ch == "\t" { n += 4 } else { break }
        }
        return n
    }

    /// What follows a list item's marker ("- item", "* item", "+ item", "3. item", "3) item"), trimmed; nil when the
    /// line is no list item.
    private static func listContent(_ trimmed: String) -> String? {
        var rest = Substring(trimmed)
        if let first = rest.first, "-*+".contains(first) {
            rest = rest.dropFirst()
        } else {
            let digits = rest.prefix { $0.isASCII && $0.isNumber }
            guard (1...9).contains(digits.count) else { return nil }
            rest = rest.dropFirst(digits.count)
            guard let mark = rest.first, mark == "." || mark == ")" else { return nil }
            rest = rest.dropFirst()
            if rest.isEmpty { return "" }
        }
        guard let next = rest.first, next == " " || next == "\t" else { return nil }
        return rest.trimmingCharacters(in: .whitespaces)
    }

    /// How many quote markers (`>`) start the trimmed line, at most `limit`, and what follows them, trimmed.
    private static func unquoted(_ trimmed: String, upTo limit: Int = .max) -> (Int, String) {
        var depth = 0
        var rest = Substring(trimmed)
        while depth < limit, rest.first == ">" {
            rest = rest.dropFirst().drop { $0 == " " || $0 == "\t" }
            depth += 1
        }
        return (depth, String(rest))
    }
}
