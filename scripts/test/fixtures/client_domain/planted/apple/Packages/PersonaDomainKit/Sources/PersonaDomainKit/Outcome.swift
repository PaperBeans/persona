/// An expected failure, read only by visiting it: not an Error, and not Equatable, so nothing decides on its code.
public protocol Fail: Sendable {
    func accept(_ v: inout some FailVisitor)
}

/// Reads a failure by its kind. Adapters implement it to map a failure once, at the edge.
public protocol FailVisitor {
    mutating func visitInvalid(_ code: FailCode, _ part: FailPart)
    mutating func visitRefused(_ code: FailCode)
}

/// A failure's code, made only from a literal.
public struct FailCode: Sendable {
    private let code: StaticString
    private init(code: StaticString) { self.code = code }
    public static func new(_ code: StaticString) -> FailCode { FailCode(code: code) }
    @_spi(Boundary) public var wire: String { "\(code)" }
}

/// The wrong input, in the core's own words.
public struct FailPart: Sendable {
    private let part: StaticString
    private init(part: StaticString) { self.part = part }
    public static func new(_ part: StaticString) -> FailPart { FailPart(part: part) }
    @_spi(Boundary) public var wire: String { "\(part)" }
}

/// A value or a failure of one family. The one public enum of the domain code (the unpack): the edge switches on it.
public enum Outcome<Value, Failure: Fail> {
    case ok(Value)
    case failed(Failure)
}
