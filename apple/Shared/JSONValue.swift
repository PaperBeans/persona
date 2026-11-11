import Foundation

/// Arbitrary JSON (tool inputs, schemas) that round-trips without key rewriting.
nonisolated enum JSONValue: Codable, Hashable, Sendable {
    case null
    case bool(Bool)
    case number(Double)
    case string(String)
    case array([JSONValue])
    case object([String: JSONValue])

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let b = try? c.decode(Bool.self) { self = .bool(b) }
        else if let n = try? c.decode(Double.self) { self = .number(n) }
        else if let s = try? c.decode(String.self) { self = .string(s) }
        else if let a = try? c.decode([JSONValue].self) { self = .array(a) }
        else { self = .object(try c.decode([String: JSONValue].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .null: try c.encodeNil()
        case .bool(let b): try c.encode(b)
        case .number(let n): try c.encode(n)
        case .string(let s): try c.encode(s)
        case .array(let a): try c.encode(a)
        case .object(let o): try c.encode(o)
        }
    }

    subscript(key: String) -> JSONValue? {
        if case .object(let o) = self { return o[key] }
        return nil
    }

    var string: String? {
        switch self {
        case .string(let s): s
        // A whole number in Int's range without a decimal point; anything else (1e20, which `Int(_:)` traps on) as Double prints it.
        case .number(let n): Int(exactly: n).map { String($0) } ?? String(n)
        case .bool(let b): b ? "true" : "false"
        default: nil
        }
    }

    var double: Double? {
        switch self {
        case .number(let n): n
        case .string(let s): Double(s)
        default: nil
        }
    }

    /// Toward zero, as `Int(_:)`; nil past Int's range or for "nan" and "inf", where `Int(_:)` traps.
    var int: Int? { double.flatMap { Int(exactly: $0.rounded(.towardZero)) } }
    var bool: Bool? { if case .bool(let b) = self { b } else { nil } }
    var array: [JSONValue]? { if case .array(let a) = self { a } else { nil } }
    var object: [String: JSONValue]? { if case .object(let o) = self { o } else { nil } }

    /// Pretty JSON for display.
    var pretty: String {
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? enc.encode(self) else { return "" }
        return String(decoding: data, as: UTF8.self)
    }

    static func from(_ any: Any?) -> JSONValue {
        switch any {
        case nil, is NSNull: return .null
        case let b as Bool: return .bool(b)
        case let i as Int: return .number(Double(i))
        case let d as Double: return .number(d)
        case let s as String: return .string(s)
        case let a as [Any]: return .array(a.map { from($0) })
        case let o as [String: Any]: return .object(o.mapValues { from($0) })
        default: return .string(String(describing: any!))
        }
    }
}

nonisolated enum JSON {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let s = (try? c.decode(String.self)) ?? ""
            return parseDate(s) ?? Date(timeIntervalSince1970: 0)
        }
        return d
    }()

    static let encoder: JSONEncoder = {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        return e
    }()

    nonisolated(unsafe) private static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    nonisolated(unsafe) private static let plain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    static func parseDate(_ s: String) -> Date? {
        guard !s.isEmpty else { return nil }
        // Go emits up to nanoseconds; ISO8601DateFormatter only takes milliseconds.
        var t = s
        if let dot = t.firstIndex(of: "."), let end = t[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let frac = t[t.index(after: dot)..<end]
            if frac.count > 3 { t.replaceSubrange(t.index(after: dot)..<end, with: frac.prefix(3)) }
        }
        return fractional.date(from: t) ?? plain.date(from: t)
    }
}

extension Date {
    /// Go's zero time and other placeholders.
    nonisolated var isMeaningful: Bool { timeIntervalSince1970 > 946_684_800 }
}
