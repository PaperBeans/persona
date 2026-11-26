"""The client domain rules: Swift and Kotlin domain modules held to docs/server/architecture.md "Domain model"
('Client modules').

The modules are found by their names, never through a list: a Swift target named `*Domain` or `*Adapt` in
`apple/Packages/*/Package.swift` (and `PersonaDomainKit`), a Gradle project `android/core/<ctx>/domain`, the package
`persona.core.<ctx>.adapt` under `android/core/<ctx>/src/main/kotlin`, and `android/core/domainkit`. The existing
`:core:domain` module (package `persona.core.domain`) is not one. A directory, source file or import that is named like
a domain module the manifests do not declare is a finding (`swift-discovery`, `kotlin-discovery`), so a module the
scan missed fails instead of passing as "no modules"; so is a Swift target found by those names but not named
`Persona<Ctx>Domain`, `Persona<Ctx>Adapt` or `Persona<Ctx>DomainTests`, which every rule still reads.

Every function here takes text (or a symbol graph already read from JSON) and returns findings; it reads no file and
keeps no state. Walking the tree is `scan_apple` and `scan_android`, at the end, and the suites in
test_client_domain_modules.py run them. Comments and string literals are blanked before a rule reads source, so a
banned word in a comment or a string is never a finding.

A finding prints as one line: `<path>:<line>: RULE <id> <what>. WHY ... FIX ... GOOD ... see docs/server/architecture.md
"Domain model" ('Client modules')`. GOOD names a compliant twin under scripts/test/fixtures/client_domain/planted, which
the guard's own tests hold at no findings. There is no ignore marker.
"""
from bisect import bisect_right
from collections import namedtuple
import gzip
import json
import os
from pathlib import Path
import re

DOC = "see docs/server/architecture.md \"Domain model\" ('Client modules')"
GOOD = "scripts/test/fixtures/client_domain/planted/"
_SWIFT_GOOD = GOOD + "apple/Packages/PersonaPlanted/"
_KOTLIN_GOOD = GOOD + "android/core/planted/"


class Finding(namedtuple("Finding", "path line rule what")):
    """One broken rule at one place: a repository-relative path, a 1-based line (0 when the place is a whole file)."""

    def message(self):
        why, fix, good = RULES[self.rule]
        return f"{self.path}:{self.line}: RULE {self.rule} {self.what}. WHY {why}. FIX {fix}. GOOD {good}. {DOC}"


# Each rule: why it holds, the change that fixes a finding, and the compliant twin that shows it done.
RULES = {
    "swift-discovery": (
        "the guard finds domain targets only by their names in a package manifest, and every context names its "
        "targets one way, so a target, folder or import it cannot place is checked by nothing or hides which context "
        "it belongs to",
        "declare the target with `.target(name: \"Persona<Ctx>Domain\")` (or `...Adapt`, and its tests "
        "`Persona<Ctx>DomainTests`) in its package's Package.swift, with its sources in Sources/<name>, or rename what "
        "only looks like one",
        _SWIFT_GOOD + "Package.swift"),
    "swift-manifest": (
        "a Domain target is a functional core: it links only the kit, the compiler settings stop Foundation, "
        "concurrency and dropped results from reaching it, and without runtime reflection metadata no "
        "String(describing:), interpolation, dump or Mirror outside the target prints a value's fields",
        "depend only on PersonaDomainKit, and pass the core settings: "
        ".swiftLanguageMode(.v6), .defaultIsolation(nil), the upcoming features InternalImportsByDefault, "
        "MemberImportVisibility and ExistentialAny, and .unsafeFlags([\"-warnings-as-errors\", \"-Xfrontend\", "
        "\"-disable-implicit-concurrency-module-import\", \"-Xfrontend\", "
        "\"-disable-implicit-string-processing-module-import\", \"-Xfrontend\", "
        "\"-reflection-metadata-for-debugger-only\"])",
        _SWIFT_GOOD + "Package.swift"),
    "swift-context": (
        "one core is one bounded context: a core that uses another context's core, or an adapter another context's "
        "adapter, ties two models together where only the application should join them",
        "give the other context's concept a value type of this context's own, and let the application meet the two "
        "contexts; the clients have no shared kernel yet, and the first one adds to this guard how it is told apart",
        _SWIFT_GOOD + "Package.swift"),
    "swift-testable": (
        "domain tests are black-box and the public API rules read the symbol graph scripts/ci/apple.sh emits; a "
        "package built another way skips both",
        "give the package a Persona<Ctx>DomainTests target, and keep the domain package step in scripts/ci/apple.sh: "
        "`swift test --disable-testable-imports` with -emit-symbol-graph for every package "
        "test_client_domain_modules.py --swift-packages lists, then AppleDomain",
        _SWIFT_GOOD + "Package.swift"),
    "swift-import": (
        "a core computes only from its arguments, so it imports no platform module",
        "import only PersonaDomainKit, and move what needs another module into the *Adapt target",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift"),
    "swift-banned-call": (
        "a core takes time, randomness and output from its caller and stops only through an outcome",
        "take the value as a parameter (Now, an id or an order made by the adapter), return an Outcome, and log in "
        "the application",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (snoozed)"),
    "swift-state": (
        "a core is values: a class, an actor or a stored var keeps state that changes in place, and a global or an "
        "unchecked escape hatch keeps state outside every value",
        "make it a struct of private `let`s whose transitions return the next value; a type's constants are its "
        "`public static let` values",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (Item)"),
    "swift-discardable": (
        "a domain result is the next value or a failure, and @discardableResult lets every caller drop it unseen",
        "remove @discardableResult; a call whose caller needs nothing back returns nothing",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (snoozed)"),
    "swift-callback": (
        "a closure in the API runs the caller's code inside the core: a port, an effect or a variation in disguise",
        "take domain values; a variation the domain knows is a closed set's value, and effects stay in the "
        "application",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (Wake)"),
    "swift-extensible": (
        "a public protocol lets code outside the target supply a domain type: a port, or a value no constructor "
        "checked",
        "make it a struct with a private init; the only public protocols are *Visitor protocols",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (ItemVisitor)"),
    "swift-visitor-name": (
        "a visitor reads a value's parts, and a requirement named otherwise is a port or a callback on it",
        "name each requirement visit<Part>, taking domain values",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (ItemVisitor)"),
    "swift-boundary": (
        "the boundary conversion is the one way out, and an @_spi member of another shape, or a static or free wire, "
        "is a second way in or out that the symbol graph does not show",
        "mark only an instance's `public var wire` with @_spi(Boundary) (the kit may mark an init too), and "
        "@_spi(Clock) only the kit's init of Now; a static or free function or property is not named wire",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (ItemID.wire)"),
    "swift-throws": (
        "an expected failure is a value the caller has to read, and a thrown Error is neither typed nor visited",
        "return Outcome<Value, <Ctx>Fail> and read the failure through FailVisitor",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (PlantedFail)"),
    "swift-conformance": (
        "codecs, descriptions and raw values read a value's state from outside, around its behaviour",
        "drop the conformance; the *Adapt target converts to the wire type and back",
        _SWIFT_GOOD + "Sources/PersonaPlantedAdapt/ItemWire.swift"),
    "swift-public-enum": (
        "a public enum lets the application switch on a domain value instead of asking it",
        "make it a struct over a private enum with `public static let` values and accept(_ v: inout some XVisitor)",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (Urgency)"),
    "swift-mutating": (
        "a transition returns the next value, so an earlier value never changes under its holder",
        "return the next value from a non-mutating method; `mutating` stays only on a visitor protocol's requirements",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (snoozed)"),
    "swift-stored-public": (
        "a stored public property, or a setter, is a getter or setter on the value's state",
        "make it `private let`, and add the behaviour the caller needs (a predicate, a transition, accept)",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (needsOwner)"),
    "swift-primitive-field": (
        "from the inside out, a String or Int beside a value's other fields is a concept without a type, which every "
        "rule inside the value decides on; only a single-field value object wraps a primitive",
        "give the field a value type of its own, a single-field struct over the primitive made through a static parse, "
        "and write every stored property's type",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (Item, ItemID)"),
    "swift-alias": (
        "an alias is another type under a new name, so the rules that read type names (a primitive beside other "
        "fields, a primitive in the API, an identity type) read the old type as a new one",
        "name the type itself, or give the concept a value type of its own: a single-field struct over the primitive "
        "made through a static parse",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (ItemID)"),
    "swift-primitive-api": (
        "a String or Int in the public API is a concept without a type, and every caller decides on it",
        "take and return value types; raw input enters only through a static parse, and a Bool only as a computed "
        "predicate or a function's result",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (ItemID.parse)"),
    "swift-accessor": (
        "a member that hands state out lets the caller decide what the value should decide, whatever its parameters, "
        "and so does a static or free function that only returns an input's field",
        "replace it with the behaviour the caller needs: a Bool predicate, a transition returning Self or Outcome, or "
        "accept(_ v: inout some XVisitor); a domain service computes from what its inputs tell it; `id` stays when it "
        "returns the target's own <Name>ID type",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (needsOwner, nextWake)"),
    "swift-generic": (
        "a generic domain type carries a caller's object inside the core, where the core can neither check nor own it",
        "use a concrete value type, or the kit's Slot for the caller's place",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift"),
    "swift-public-evolve": (
        "applying an event outside its decider can put a value in a state no rule allowed",
        "keep evolve internal and expose decide",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (snoozed)"),
    "swift-factory": (
        "values are built only through constructors named for how they are built, with at most four inputs, and a "
        "construction that can fail says why in an Outcome, never as nil",
        "construct through a static parse (raw input) or new (domain values) returning the type or an Outcome of it, "
        "or restore (stored state) returning an Outcome, on a private init, with a staged builder (newBuilder, "
        "restoreBuilder) above four inputs; only a closed set (a struct whose one stored property is its private "
        "enum) names a factory for one of its cases",
        _SWIFT_GOOD + "Sources/PersonaPlantedDomain/Item.swift (Item.new, Wake.at)"),
    "swift-spi-import": (
        "the boundary conversions are for adapters only, and only the kit's adapter reads the clock",
        "convert in a *Adapt target, which alone writes `@_spi(Boundary) import`; `@_spi(Clock) import` belongs to "
        "PersonaDomainKitAdapt",
        _SWIFT_GOOD + "Sources/PersonaPlantedAdapt/ItemWire.swift"),
    "swift-visitor-impl": (
        "code that reads a domain value through a visitor converts it, which is the adapter's job",
        "implement the visitor in the *Adapt target (or the domain tests) and call the behaviour the app needs",
        _SWIFT_GOOD + "Sources/PersonaPlantedAdapt/ItemWire.swift"),
    "swift-app-discard": (
        "a domain transition returns the next value or a failure, and dropping it loses the change or the failure",
        "use the result: keep the next value, or read the outcome",
        GOOD + "apple/Persona/PlantedRow.swift"),
    "kotlin-app-discard": (
        "a domain transition returns the next value or a failure, and dropping it loses the change or the failure",
        "use the result: keep the next value, or fold the outcome",
        GOOD + "android/feature/planted/src/main/kotlin/persona/feature/planted/PlantedUse.kt"),
    "kotlin-discovery": (
        "the guard finds domain modules only by their names in settings.gradle.kts and their source paths, and one it "
        "cannot see is checked by nothing",
        "include the module as \":core:<ctx>:domain\" in android/settings.gradle.kts with its package "
        "persona.core.<ctx>.domain, keep adapters in android/core/<ctx>/src/main/kotlin/persona/core/<ctx>/adapt, or "
        "rename what only looks like one",
        _KOTLIN_GOOD + "domain/build.gradle.kts"),
    "kotlin-build-flags": (
        "the compiler holds the module to explicit visibility, warnings as errors and checked results, and the "
        "Android-wide flags make a dropped domain result an error in every module",
        "apply persona.jvm.library, call explicitApi(), set allWarningsAsErrors to true and add "
        "-Xreturn-value-checker=full; keep -Xreturn-value-checker=check and "
        "-Xwarning-level=RETURN_VALUE_NOT_USED:error in android/build-logic for every module",
        _KOTLIN_GOOD + "domain/build.gradle.kts"),
    "kotlin-module": (
        "a domain module is a functional core that links only the kit",
        "depend only on project(\":core:domainkit\"); move the rest to :core:<ctx>'s adapt package",
        _KOTLIN_GOOD + "domain/build.gradle.kts"),
    "kotlin-context": (
        "one core is one bounded context: a core that uses another context's core, or an adapter another context's "
        "adapter, ties two models together where only the application should join them",
        "give the other context's concept a value type of this context's own, and let the application meet the two "
        "contexts; the clients have no shared kernel yet, and the first one adds to this guard how it is told apart",
        _KOTLIN_GOOD + "domain/build.gradle.kts"),
    "kotlin-var": (
        "a core value never changes in place",
        "use val and return the next value from a transition",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (snoozed)"),
    "kotlin-state": (
        "a core is values: a mutable collection or an array changes under its holder, and package-level state "
        "outlives every call",
        "hold read-only List, Set and Map values built once (listOf, buildList) and return the next value from a "
        "transition; a class's constants live in its companion, and package-level code is functions",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (Item)"),
    "kotlin-ignorable": (
        "a domain result is the next value or a failure, and @IgnorableReturnValue lets every caller drop it unseen",
        "remove the annotation; a call whose caller needs nothing back returns Unit",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (snoozed)"),
    "kotlin-callback": (
        "a function type in the API runs the caller's code inside the core: a port, an effect or a variation in "
        "disguise",
        "take domain values; a variation the domain knows is a closed set's value, and effects stay in the "
        "application",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (snoozed)"),
    "kotlin-extensible": (
        "a public interface, abstract class or open class lets code outside the module supply a domain type: a port, "
        "or a value no constructor checked",
        "make it a final class with a private constructor; the only public interfaces are *Visitor interfaces",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemVisitor)"),
    "kotlin-visitor-name": (
        "a visitor reads a value's parts, and a member named otherwise is a port or a callback on it",
        "name each function visit<Part>, taking domain values",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemVisitor)"),
    "kotlin-boundary": (
        "the boundary conversion is the one way out, and a member of another shape behind the opt-in, or a companion "
        "or top-level wire(), is a second way in or out that the API rules do not read",
        "mark only a member `fun wire()` with @DomainBoundary (the kit may mark a companion function too), and "
        "@ClockRead only a companion function of the kit; a companion or top-level function is not named wire",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemId.wire)"),
    "kotlin-generic": (
        "a type parameter carries a caller's object inside the core, where the core can neither check nor own it",
        "use a concrete value type, or the kit's Slot for the caller's place",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt"),
    "kotlin-ctor-property": (
        "a public constructor property is a getter on the value's state",
        "make it private val (or internal val for the module's peers) and add the behaviour the caller needs",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt"),
    "kotlin-public-constructor": (
        "a public constructor builds a value without the checks of parse, new or restore",
        "declare `private constructor` and construct through companion parse, new or restore",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemId)"),
    "kotlin-value-class": (
        "a value class's generated toString prints its wrapped value, a second way out beside wire(), and an override "
        "of it is a getter",
        "write a class with a private constructor and a private val, with equals and hashCode where identity matters; "
        "its toString is Any's, which prints no field",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemId)"),
    "kotlin-data-class": (
        "a data class exposes its state through componentN, copy and toString, around its constructors",
        "write a class with a private constructor and private vals, with equals and hashCode where identity matters",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt"),
    "kotlin-public-sealed": (
        "a public sealed type, enum class or data object lets the application `when` on a domain value",
        "make it a class with a private constructor, companion values and accept(visitor); a sealed type or enum "
        "class stays internal or private inside it",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (Urgency)"),
    "kotlin-primitive-field": (
        "from the inside out, a String or Int beside a value's other fields is a concept without a type, which every "
        "rule inside the value decides on; only a single-field value object wraps a primitive",
        "give the field a value type of its own, a class over the primitive made through companion parse, and write "
        "every stored property's type",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (Item, ItemId)"),
    "kotlin-alias": (
        "an alias is another type under a new name, so the rules that read type names (a primitive beside other "
        "fields, a primitive in the API, an identity class) read the old type as a new one",
        "name the type itself, or give the concept a value type of its own: a class over the primitive made through "
        "companion parse",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemId)"),
    "kotlin-primitive-api": (
        "a String or Int in the public API is a concept without a type, and every caller decides on it",
        "take and return value types; raw input enters only through companion parse, and a Boolean only as a "
        "computed predicate or a function's result",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (ItemId.parse)"),
    "kotlin-accessor": (
        "a member that hands state out lets the caller decide what the value should decide, whatever its parameters, "
        "and so does a companion or top-level function that only returns an input's field",
        "replace it with the behaviour the caller needs: a computed Boolean, a transition returning the class or its "
        "Outcome, or accept(visitor); a domain service computes from what its inputs tell it; id() stays when it "
        "returns the module's own <Name>Id class",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (needsOwner, nextWake)"),
    "kotlin-import": (
        "a core computes only from its arguments, so it uses no platform, library or adapter API",
        "import only persona.core.domainkit (not its adapt package) and its own package, by import or full name; "
        "move the rest to the adapt package",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt"),
    "kotlin-banned-call": (
        "a core takes time, ids, randomness and output from its caller, and runs on its caller's thread",
        "take Now, ids and orders as parameters from the adapter, compute eagerly, and log in the application",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (snoozed)"),
    "kotlin-serializable": (
        "a serializable core type is a stored or wire shape, which lives in the store or adapt package",
        "keep the @Serializable record in :core:<ctx>'s store or adapt package and convert in the adapter",
        _KOTLIN_GOOD + "src/main/kotlin/persona/core/planted/adapt/ItemWire.kt"),
    "kotlin-optin": (
        "the boundary conversions are for adapters only, and only the kit's clock adapter reads the clock",
        "opt in to DomainBoundary only in persona.core.<ctx>.adapt; ClockRead only in persona.core.domainkit.adapt; "
        "never through a compiler flag",
        _KOTLIN_GOOD + "src/main/kotlin/persona/core/planted/adapt/ItemWire.kt"),
    "kotlin-throw": (
        "an expected failure is a value, and a programming error is a Defect",
        "return Outcome for an expected failure and `throw Defect(...)` for a programming error; no !!, error(), "
        "require() or check()",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (PlantedFail)"),
    "kotlin-public-evolve": (
        "applying an event outside its decider can put a value in a state no rule allowed",
        "keep evolve internal and expose decide",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (snoozed)"),
    "kotlin-visitor-impl": (
        "code that reads a domain value through a visitor converts it, which is the adapter's job",
        "implement the visitor in persona.core.<ctx>.adapt (or a test) and call the behaviour the app needs",
        _KOTLIN_GOOD + "src/main/kotlin/persona/core/planted/adapt/ItemWire.kt"),
    "kotlin-factory": (
        "values are built only through constructors named for how they are built, with at most four inputs, and a "
        "construction that can fail says why in an Outcome, never as null",
        "name a companion factory parse or new returning the class or an Outcome of it, restore returning an Outcome, "
        "or newBuilder or restoreBuilder returning the first stage, and use a staged builder above four inputs; only "
        "a closed set (a class whose one field is its private enum class or internal sealed type) names a factory for "
        "one of its cases",
        _KOTLIN_GOOD + "domain/src/main/kotlin/persona/core/planted/domain/Item.kt (Item.new)"),
}

# Named, reviewed exceptions for the kit's documented API, by rule, kit and declaration path. A line nothing needs
# fails (test_client_domain_modules.py), so the list only holds what the kit declares.
KIT_EXCEPTIONS = {
    ("swift-public-enum", "PersonaDomainKit", "Outcome"): "the unpack: the one public enum, switched on at the edge",
    ("swift-generic", "PersonaDomainKit", "Outcome"): "carries every core's value and failure family",
    ("swift-extensible", "PersonaDomainKit", "Fail"): "every core's failure family conforms to it",
    ("kotlin-generic", ":core:domainkit", "Outcome"): "carries every core's value and failure family",
    ("kotlin-callback", ":core:domainkit", "Outcome.fold"): "the unpack: the edge reads an outcome through it",
    ("kotlin-accessor", ":core:domainkit", "Outcome.fold"): "the unpack: it returns what the edge computes",
    ("kotlin-extensible", ":core:domainkit", "Fail"): "every core's failure family implements it",
}
SWIFT_KIT_NAME = "PersonaDomainKit"
KOTLIN_KIT_NAME = ":core:domainkit"

# The names a factory may have: how it builds. Only a closed set adds one per case that carries data, named for the
# case (Decision.holdUntil(t), ItemCommand.markDone(by:)), and never a constructor in disguise.
FACTORY_NAMES = frozenset(("parse", "new", "restore", "newBuilder", "restoreBuilder"))
BANNED_FACTORY = re.compile(
    r"^(?:make|create|build|init|from|to|get|set|reconstruct|must|of|valueOf|invoke|with|copy|blank|empty|default)"
    r"(?:[A-Z_]|$)")
MAX_PARAMS = 4
SWIFT_PRIMITIVES = frozenset((
    "String", "Character", "Substring", "Bool", "Int", "Int8", "Int16", "Int32", "Int64", "Int128", "UInt", "UInt8",
    "UInt16", "UInt32", "UInt64", "UInt128", "Double", "Float", "Float16", "Float80", "CGFloat", "Decimal",
    "TimeInterval", "Date", "UUID", "Data", "URL", "Any", "AnyObject", "AnyHashable",
    # The standard library's own aliases of them.
    "Float32", "Float64", "UnicodeScalar", "CBool", "CChar", "CChar16", "CChar32", "CWideChar", "CSignedChar",
    "CUnsignedChar", "CShort", "CUnsignedShort", "CInt", "CUnsignedInt", "CLong", "CUnsignedLong", "CLongLong",
    "CUnsignedLongLong", "CFloat", "CDouble"))
KOTLIN_PRIMITIVES = frozenset((
    "String", "CharSequence", "Char", "Boolean", "Byte", "Short", "Int", "Long", "UByte", "UShort", "UInt", "ULong",
    "Float", "Double", "Number", "Any", "ByteArray", "ShortArray", "IntArray", "LongArray", "FloatArray", "DoubleArray",
    "CharArray", "BooleanArray"))
_BUILD_DIRS = frozenset(("build", "DerivedData", "node_modules", "xcuserdata"))
_BUILD_SUFFIXES = (".xcodeproj", ".xcworkspace", ".xcframework", ".xcassets", ".lproj", ".xcresult")


# ---------------------------------------------------------------------------------------------------------------------
# Comments and string literals


class _Stripper:
    """Finds comment and string-content ranges in Swift or Kotlin source. Block comments nest in both languages, and
    interpolated code inside a string (Swift \\(...), Kotlin ${...}) is code again."""

    _SWIFT = re.compile(r"//|/\*|#+\"|\"|[(){}\[\]]")
    _KOTLIN = re.compile(r"//|/\*|\"|'|[(){}\[\]]")
    _BLOCK = re.compile(r"/\*|\*/")

    def __init__(self, text, swift):
        self.text, self.n, self.swift = text, len(text), swift
        self.ranges = []  # (start, end, is_comment), in order and apart
        self._patterns = {}

    def code(self, i, inside=False):
        """Scan code from i. Inside an interpolation, stop after the bracket that closes it; return where it stopped."""
        text, depth = self.text, 0
        pattern = self._SWIFT if self.swift else self._KOTLIN
        while True:
            m = pattern.search(text, i)
            if not m:
                return self.n
            token, start = m.group(), m.start()
            if token == "//":
                end = text.find("\n", start)
                end = self.n if end < 0 else end
                self.ranges.append((start, end, True))
                i = end
            elif token == "/*":
                end = self._block_end(start)
                self.ranges.append((start, end, True))
                i = end
            elif token in "([{":
                depth += 1
                i = m.end()
            elif token in ")]}":
                if inside and depth == 0:
                    return m.end()
                depth -= 1
                i = m.end()
            else:
                i = self._swift_string(start, token) if self.swift else self._kotlin_string(start, token)

    def _block_end(self, start):
        depth, i = 0, start
        while True:
            m = self._BLOCK.search(self.text, i)
            if not m:
                return self.n
            depth += 1 if m.group() == "/*" else -1
            i = m.end()
            if depth == 0:
                return i

    def _pattern(self, key, source):
        if key not in self._patterns:
            self._patterns[key] = re.compile(source)
        return self._patterns[key]

    def _swift_string(self, start, token):
        text, hashes = self.text, len(token) - 1
        i = start + len(token)
        multi = text.startswith("\"\"", i)
        if multi:
            i += 2
        close = ("\"\"\"" if multi else "\"") + "#" * hashes
        escape = "\\" + "#" * hashes
        source = re.escape(close) + "|" + re.escape(escape) + ("" if multi else r"|\n")
        pattern = self._pattern(("s", hashes, multi), source)
        content = i
        while True:
            m = pattern.search(text, i)
            if not m:
                self.ranges.append((content, self.n, False))
                return self.n
            if m.group() == "\n":
                self.ranges.append((content, m.start(), False))
                return m.start()
            if m.group() == close:
                self.ranges.append((content, m.start(), False))
                return m.end()
            after = m.end()
            if after < self.n and text[after] == "(":
                self.ranges.append((content, m.start(), False))
                i = content = self.code(after + 1, inside=True)
            else:
                i = after + 1

    def _kotlin_string(self, start, token):
        text = self.text
        if token == "'":  # a char literal: 'a', '\n', '\'' or '\u0041'
            body = start + 1
            end = text.find("'", body + (2 if text.startswith("\\", body) else 1))
            if end < 0 or end - start > 8 or "\n" in text[start:end]:
                return start + 1
            self.ranges.append((body, end, False))
            return end + 1
        raw = text.startswith("\"\"\"", start)
        i = content = start + (3 if raw else 1)
        pattern = self._pattern(("k", raw), r"\"\"\"+|\$\{" if raw else r"\\.|\"|\$\{|\n")
        while True:
            m = pattern.search(text, i)
            if not m:
                self.ranges.append((content, self.n, False))
                return self.n
            found = m.group()
            if found == "${":
                self.ranges.append((content, m.start(), False))
                i = content = self.code(m.end(), inside=True)
            elif found.startswith("\"\"\""):
                self.ranges.append((content, m.end() - 3, False))
                return m.end()
            elif found == "\"" or found == "\n":
                self.ranges.append((content, m.start(), False))
                return m.end() if found == "\"" else m.start()
            else:
                i = m.end()


def strip(text, language, keep_strings=False):
    """The source with comments, and string contents unless keep_strings, turned to spaces. Newlines stay, so offsets
    and line numbers are the source's."""
    stripper = _Stripper(text, language == "swift")
    stripper.code(0)
    out, last = [], 0
    for start, end, comment in stripper.ranges:
        if keep_strings and not comment:
            continue
        out.append(text[last:start])
        out.append(re.sub(r"[^\n]", " ", text[start:end]))
        last = end
    out.append(text[last:])
    return "".join(out)


# ---------------------------------------------------------------------------------------------------------------------
# Tokens

Token = namedtuple("Token", "v line start end first")
# A number keeps its fraction (1.5e3) but not a member after it, so `$0.snoozed(` reads as a call.
_TOKEN = re.compile(r"`[^`\n]+`|[A-Za-z_][A-Za-z0-9_]*|\d\w*(?:\.\d\w*)*|->|::|\?\.|\?:|!!|\.\.[.<]?|&&|\|\||"
                    r"[=!<>]=|\S")


def tokens(text):
    """Tokens with their 1-based line and whether each is the first on its line."""
    newlines = [m.start() for m in re.finditer("\n", text)]
    out, last_line = [], 0
    for m in _TOKEN.finditer(text):
        line = bisect_right(newlines, m.start()) + 1
        value = m.group()
        if value.startswith("`"):
            value = value[1:-1]
        out.append(Token(value, line, m.start(), m.end(), line != last_line))
        last_line = line
    return out


def _line_of(text, offset):
    return text.count("\n", 0, offset) + 1


def _ident(value):
    return bool(value) and (value[0].isalpha() or value[0] == "_")


def _field_read(body, inputs, receiver, fields, implicit_return=False):
    """For a body that only hands state out as it is, (the expression, the last name it reads); else None.

    That is a member chain rooted at a parameter or the receiver (`item.wakes`, `this.wakes`, `self.wakes`), or in a
    member a stored field read through the implicit receiver (`wakes`, `state.kind`), as an expression body, a
    one-statement `{ return ... }` block or, with implicit_return (Swift), a one-expression block. body: ("expr" or
    "block", token values); inputs: the parameters' names; receiver: "this" or "self" in a member, None outside one;
    fields: the names of the stored fields the module declares."""
    if body is None:
        return None
    kind, toks = body[0], list(body[1])
    while toks and toks[-1] == ";":
        toks.pop()
    if kind == "block" and toks[:1] == ["return"]:
        toks = toks[1:]
    elif kind == "block" and not implicit_return:
        return None
    while len(toks) > 2 and toks[0] == "(" and toks[-1] == ")":
        toks = toks[1:-1]
    names, seps = toks[0::2], toks[1::2]
    if not toks or len(toks) % 2 == 0 or not all(_ident(n) for n in names) or any(s not in (".", "?.") for s in seps):
        return None
    root = names[0]
    if root in inputs or (receiver is not None and root == receiver):
        if len(names) < 2:
            return None  # the input itself, not its state
    elif receiver is None or root not in fields:
        return None
    return "".join(toks), names[-1]


# ---------------------------------------------------------------------------------------------------------------------
# Dropped results


def _expression_at(text, start):
    """The expression of stripped source from start to the end of its statement: a newline or ; outside brackets, unless
    the next line goes on with `.` (a chain broken over lines)."""
    depth, i, n = 0, start, len(text)
    while i < n:
        c = text[i]
        if c in "([{":
            depth += 1
        elif c in ")]}":
            if depth == 0:
                break
            depth -= 1
        elif c == ";" and depth == 0:
            break
        elif c == "\n" and depth == 0:
            rest = text[i + 1:i + 200].lstrip()
            if not rest.startswith((".", "?.")) or rest.startswith(".."):
                break
        i += 1
    return text[start:i]


def _type_receiver(values, i):
    """For the call whose name is values[i]: the type the code calls it on (`Uri` in `Uri.parse(raw)`, a capitalised
    name before the dot), or None for a value, a chain or no receiver."""
    if i >= 2 and values[i - 1] in (".", "?.") and _ident(values[i - 2]) and values[i - 2][:1].isupper():
        return values[i - 2]
    return None


def _after_call(values, i):
    """From the ( or { after a called name, the index past its arguments and trailing lambdas."""
    n = len(values)
    while i < n and values[i] in ("(", "{"):
        depth = 0
        while i < n:
            depth += values[i] in ("(", "[", "{")
            depth -= values[i] in (")", "]", "}")
            i += 1
            if depth == 0:
                break
    return i


def _dropped_calls(expr, functions, declared):
    """The domain functions whose results the expression after `_ =` or `val _ =` drops, in order: each call of one
    (`name(`, or `name {` with a trailing lambda) that is no other call's argument, leads on to no further call
    (`.fold(...)` reads it), is not assigned inside a lambda, and is not made on a type the domain modules do not
    declare (`Uri.parse(raw)` is Uri's). A call on a value is counted by its name: its type is not in the text."""
    toks = tokens(expr)
    values = [t.v for t in toks]
    n = len(values)
    out, parens, assigned = [], 0, [False]  # assigned: per lambda, whether its current statement assigns
    for i, value in enumerate(values):
        if i and toks[i].first and value not in (".", "?."):
            assigned[-1] = False  # a new line inside a lambda is a new statement
        if value in ("(", "["):
            parens += 1
        elif value in (")", "]"):
            parens -= 1
        elif value == "{":
            assigned.append(False)
        elif value == "}":
            if len(assigned) > 1:
                assigned.pop()
        elif value == "=" and not parens:  # inside parentheses it names an argument
            assigned[-1] = True
        elif value in (";", "->", "in"):
            assigned[-1] = False
        if parens or assigned[-1] or value not in functions or i + 1 >= n or values[i + 1] not in ("(", "{"):
            continue
        if i and values[i - 1] in ("fun", "func", "::"):
            continue
        receiver = _type_receiver(values, i)
        if receiver is not None and receiver not in declared:
            continue
        end = _after_call(values, i + 1)
        if end < n and (values[end] in (".", "?.") or (values[end] in ("!!", "!", "?") and end + 1 < n
                                                        and values[end + 1] in (".", "?."))):
            continue
        out.append(value)
    return out


def _called_on_other_types(line, name, declared):
    """Whether a line of stripped source calls name, and only on types the domain modules do not declare
    (`Draft.parse(raw)`)."""
    values = [t.v for t in tokens(line)]
    receivers = [_type_receiver(values, i) for i, v in enumerate(values)
                 if v == name and i + 1 < len(values) and values[i + 1] in ("(", "{")]
    return bool(receivers) and all(r is not None and r not in declared for r in receivers)


# ---------------------------------------------------------------------------------------------------------------------
# Kotlin declarations


class KtDecl:
    """A Kotlin declaration the rules read: a class, interface or object with its members, a function, a property, a
    secondary constructor or a type alias. What function and accessor bodies declare is not API, so they are skipped; a
    function's body and a property's getter are kept as token values, for the rule that a body returning an input's
    field is a getter."""

    def __init__(self, kind, name, line, mods, annos, parent):
        self.kind, self.name, self.line, self.mods, self.annos, self.parent = kind, name, line, mods, annos, parent
        self.params, self.type, self.receiver, self.supers, self.children = [], None, None, [], []
        self.ctor = None  # a class's primary constructor: None without one, else its visibility ("" for the default)
        self.getter = self.initializer = self.generic = False
        self.init_first = ""  # a property initializer's first token
        self.body = None  # a function's body or a property's getter: ("expr", [token values]) or ("block", [...])

    @property
    def public(self):
        own = not (self.mods & {"private", "internal", "protected"})
        return own and (self.parent is None or self.parent.public)

    def walk(self):
        for child in self.children:
            yield child
            yield from child.walk()


KtParam = namedtuple("KtParam", "name mods annos binding type line")
_KT_MODIFIERS = frozenset((
    "public", "private", "internal", "protected", "open", "final", "abstract", "sealed", "data", "enum", "value",
    "inline", "override", "companion", "inner", "annotation", "const", "lateinit", "operator", "infix", "suspend",
    "tailrec", "external", "expect", "actual", "noinline", "crossinline", "vararg"))
_KT_KEYWORDS = frozenset(("class", "interface", "object", "fun", "val", "var", "constructor", "init", "typealias"))
_KT_STARTERS = _KT_MODIFIERS | _KT_KEYWORDS | {"@", "}"}
_KT_ANNOTATION_TARGETS = frozenset(("file", "get", "set", "param", "field", "property", "receiver", "setparam",
                                    "delegate"))


class _KtParser:
    def __init__(self, toks):
        self.t, self.n = toks, len(toks)
        self.package, self.imports = "", []

    def v(self, i):
        return self.t[i].v if i < self.n else ""

    def new_line(self, i):
        return i < self.n and self.t[i].first

    def members(self, owner, i):
        while i < self.n:
            value = self.v(i)
            if value == "}" and owner.kind != "file":
                return i + 1
            if value in (";", "}"):
                i += 1
                continue
            if owner.kind == "file" and value in ("package", "import"):
                j = i + 1
                while j < self.n and not self.t[j].first:
                    j += 1
                name = "".join(tok.v for tok in self.t[i + 1:j])
                if value == "package":
                    self.package = name
                else:
                    self.imports.append((re.sub(r"(?<=[\w*])as[A-Za-z_]\w*$", "", name), self.t[i].line))
                i = j
                continue
            j, annos = self.annotations(i)
            k, mods = self.modifier_run(j)
            keyword = self.v(k)
            if keyword == "fun" and self.v(k + 1) == "interface":
                mods = mods | {"fun"}
                k += 1
                keyword = "interface"
            if keyword not in _KT_KEYWORDS:  # an expression or an enum entry, not a declaration
                i = max(self.skip_expression(j), i + 1)
                continue
            if keyword in ("class", "interface", "object"):
                i = self.klass(owner, k, keyword, mods, annos)
            elif keyword == "fun":
                i = self.fun(owner, k, mods, annos)
            elif keyword in ("val", "var"):
                i = self.prop(owner, k, keyword, mods, annos)
            elif keyword == "constructor":
                decl = KtDecl("constructor", "constructor", self.t[k].line, mods, annos, owner)
                i, decl.params = self.params(k + 1)
                owner.children.append(decl)
                if self.v(i) == ":":
                    i = self.skip_expression(i + 1, stops=("{",))
                if self.v(i) == "{":
                    i = self.skip_brackets(i)
            elif keyword == "init" and self.v(k + 1) == "{":
                i = self.skip_brackets(k + 1)
            elif keyword == "typealias" and _ident(self.v(k + 1)):
                decl = KtDecl("typealias", self.v(k + 1), self.t[k].line, mods, annos, owner)
                owner.children.append(decl)
                i = k + 2
                if self.v(i) == "<":
                    decl.generic = True
                    i = self.skip_angles(i)
                if self.v(i) == "=":
                    i, decl.type = self.type_until(i + 1, ())
            else:
                i = self.skip_expression(k + 1)
        return i

    def annotations(self, i):
        names = set()
        while self.v(i) == "@":
            i += 1
            if self.v(i) in _KT_ANNOTATION_TARGETS and self.v(i + 1) == ":":
                i += 2
            if self.v(i) == "[":  # @[A B]
                end = self.skip_brackets(i)
                names.update(tok.v for tok in self.t[i:end] if _ident(tok.v))
                i = end
                continue
            name = ""
            while _ident(self.v(i)):
                name = self.v(i)
                i += 1
                if self.v(i) == "." and _ident(self.v(i + 1)):
                    i += 1
                    continue
                break
            names.add(name)
            if self.v(i) == "<":
                i = self.skip_angles(i)
            if self.v(i) == "(" and i < self.n and self.t[i].start == self.t[i - 1].end:
                i = self.skip_brackets(i)
        return i, names

    def modifier_run(self, i):
        j, mods = i, set()
        while self.v(j) in _KT_MODIFIERS and self.v(j + 1) not in (":", "=", "(", ".", ","):
            mods.add(self.v(j))
            j += 1
            if self.v(j) == "@":
                j, _ = self.annotations(j)
        if self.v(j) in _KT_KEYWORDS:
            return j, mods
        return i, set()

    def skip_brackets(self, i):
        """From an opening (, [ or {, the index after its closing partner."""
        depth = 0
        while i < self.n:
            value = self.v(i)
            if value in ("(", "[", "{"):
                depth += 1
            elif value in (")", "]", "}"):
                depth -= 1
                if depth == 0:
                    return i + 1
            i += 1
        return i

    def skip_angles(self, i):
        depth = 0
        while i < self.n:
            value = self.v(i)
            if value == "<":
                depth += 1
            elif value == ">":
                depth -= 1
                if depth == 0:
                    return i + 1
            elif value in ("{", ";", "=") or (value == ")" and depth <= 1):
                return i
            i += 1
        return i

    def skip_expression(self, i, stops=(), accessors=False):
        """Past an expression or statement: to a ; or } at its depth, a stop token, or a line that starts a
        declaration (with accessors, also one that starts get or set)."""
        depth, begin = 0, i
        while i < self.n:
            value = self.v(i)
            if depth == 0 and i > begin and self.new_line(i):
                if value in _KT_STARTERS:
                    return i
                if accessors and (value in ("get", "set") or (value in ("private", "internal", "protected", "public")
                                                              and self.v(i + 1) in ("get", "set"))):
                    return i
            if depth == 0 and (value in (";", "}") or value in stops):
                return i
            if value in ("(", "[", "{"):
                depth += 1
            elif value in (")", "]", "}"):
                depth -= 1
            i += 1
        return i

    def type_until(self, i, stops):
        """The type's tokens from i to a stop token at depth 0, or to the end of its line."""
        begin, depth = i, 0
        while i < self.n:
            value = self.v(i)
            if depth == 0 and (value in stops or (i > begin and self.new_line(i))):
                break
            if value in ("(", "[", "<"):
                depth += 1
            elif value in (")", "]", ">"):
                if depth == 0:
                    break
                depth -= 1
            elif value in ("{", "}", ";") and depth == 0:
                break
            i += 1
        return i, [tok.v for tok in self.t[begin:i]]

    def params(self, i):
        """Parameters of the list opening at i: (index after it, [KtParam])."""
        if self.v(i) != "(":
            return i, []
        end = self.skip_brackets(i)
        chunks, chunk, depth = [], [], 0
        for tok in self.t[i + 1:end - 1]:
            if tok.v in ("(", "[", "{", "<"):
                depth += 1
            elif tok.v in (")", "]", "}", ">"):
                depth -= 1
            if tok.v == "," and depth == 0:
                chunks.append(chunk)
                chunk = []
            else:
                chunk.append(tok)
        if chunk:
            chunks.append(chunk)
        out = []
        for chunk in chunks:
            sub = _KtParser(chunk)
            j, annos = sub.annotations(0)
            mods = set()
            while sub.v(j) in _KT_MODIFIERS:
                mods.add(sub.v(j))
                j += 1
            binding = sub.v(j) if sub.v(j) in ("val", "var") else None
            if binding:
                j += 1
            name = sub.v(j)
            j += 1
            type_tokens = []
            if sub.v(j) == ":":
                j, type_tokens = sub.type_until(j + 1, ("=",))
            out.append(KtParam(name, mods, annos, binding, type_tokens, chunk[0].line))
        return end, out

    def klass(self, owner, k, keyword, mods, annos):
        i = k + 1
        name = ""
        if _ident(self.v(i)) and self.v(i) not in ("constructor",):
            name = self.v(i)
            i += 1
        elif "companion" in mods:
            name = "Companion"
        decl = KtDecl(keyword, name, self.t[k].line, mods, annos, owner)
        owner.children.append(decl)
        if self.v(i) == "<":
            decl.generic = True
            i = self.skip_angles(i)
        j, _ = self.annotations(i)
        visibility = ""
        while self.v(j) in ("private", "internal", "public", "protected"):
            visibility = self.v(j)
            j += 1
        if self.v(j) == "constructor":
            decl.ctor = visibility
            i = j + 1
        if self.v(i) == "(" and not self.new_line(i):
            if decl.ctor is None:
                decl.ctor = ""
            i, decl.params = self.params(i)
        if self.v(i) == ":":
            i = self.supertypes(decl, i + 1)
        if self.v(i) == "where":
            i = self.skip_expression(i + 1, stops=("{",))
        if self.v(i) == "{":
            if "enum" in mods:
                i = self.skip_brackets(i)
            else:
                i = self.members(decl, i + 1)
        return i

    def supertypes(self, decl, i):
        """The names after a class header's colon, up to its body or the end of the declaration."""
        while i < self.n and not (self.new_line(i) and self.v(i) in _KT_STARTERS):
            while _ident(self.v(i)) and self.v(i + 1) == ".":
                i += 2  # a qualified name: the last part names the type
            if not _ident(self.v(i)):
                break
            decl.supers.append(self.v(i))
            i += 1
            if self.v(i) == "<":
                i = self.skip_angles(i)
            if self.v(i) == "(":
                i = self.skip_brackets(i)
            if self.v(i) == "by":
                i = self.skip_expression(i + 1, stops=(",", "{"))
            if self.v(i) != ",":
                break
            i += 1
        return i

    def signature(self, i, prop=False):
        """The receiver and name before a function's ( or a property's : =: (index at the stop, receiver, name). A
        property's getter may follow its name on the same line (`val empty get() = ...`)."""
        begin, depth = i, 0
        while i < self.n:
            value = self.v(i)
            if value == "<":
                depth += 1
            elif value == ">":
                depth -= 1
            elif depth == 0 and (value in ("(", ":", "=", "{", ";", "}") or value == "by"
                                 or (i > begin and self.new_line(i))
                                 or (prop and i > begin and value in ("get", "set") and self.v(i + 1) in ("(", "="))):
                break
            i += 1
        parts = [tok.v for tok in self.t[begin:i]]
        name, receiver = (parts[-1] if parts else ""), None
        depth = 0
        for index in range(len(parts) - 1, -1, -1):
            if parts[index] == ">":
                depth += 1
            elif parts[index] == "<":
                depth -= 1
            elif parts[index] == "." and depth == 0:
                receiver = parts[:index]
                break
        return i, receiver, name

    def fun(self, owner, k, mods, annos):
        i = k + 1
        generic = self.v(i) == "<"
        if generic:
            i = self.skip_angles(i)
        i, receiver, name = self.signature(i)
        decl = KtDecl("fun", name, self.t[k].line, mods, annos, owner)
        decl.receiver, decl.generic = receiver, generic
        owner.children.append(decl)
        i, decl.params = self.params(i)
        if self.v(i) == ":":
            i, decl.type = self.type_until(i + 1, ("{", "=", "where"))
        if self.v(i) == "where":
            i = self.skip_expression(i + 1, stops=("{", "="))
        return self.body(decl, i)

    def body(self, decl, i):
        """Past a body at i (`{ ... }` or `= ...`), kept on decl; i itself when there is none."""
        if self.v(i) == "{":
            end = self.skip_brackets(i)
            decl.body = ("block", [tok.v for tok in self.t[i + 1:end - 1]])
            return end
        if self.v(i) == "=":
            end = self.skip_expression(i + 1, accessors=decl.kind in ("val", "var"))
            decl.body = ("expr", [tok.v for tok in self.t[i + 1:end]])
            return end
        return i

    def prop(self, owner, k, keyword, mods, annos):
        i = k + 1
        if self.v(i) == "<":
            i = self.skip_angles(i)
        i, receiver, name = self.signature(i, prop=True)
        decl = KtDecl(keyword, name, self.t[k].line, mods, annos, owner)
        decl.receiver = receiver
        owner.children.append(decl)
        if self.v(i) == ":":
            i, decl.type = self.type_until(i + 1, ("=", "by", "get", "set", ";", "}"))
        if self.v(i) in ("=", "by"):
            decl.initializer = True
            decl.init_first = self.v(i + 1)
            i = self.skip_expression(i + 1, accessors=True)
        while True:
            j, _ = self.annotations(i)
            while self.v(j) in ("private", "internal", "protected", "public", "override", "inline"):
                j += 1
            accessor = self.v(j) in ("get", "set") and (self.v(j + 1) in ("(", "=", "{", ";", "}")
                                                        or self.new_line(j + 1))
            if not accessor:
                break
            getter = self.v(j) == "get"
            decl.getter = decl.getter or getter
            i = j + 1
            if self.v(i) == "(":
                i = self.skip_brackets(i)
            if self.v(i) == ":":
                i, _ = self.type_until(i + 1, ("{", "="))
            if getter:
                i = self.body(decl, i)
            elif self.v(i) == "{":
                i = self.skip_brackets(i)
            elif self.v(i) == "=":
                i = self.skip_expression(i + 1, accessors=True)
        return i


def parse_kotlin(stripped):
    """(package, [(import, line)], file declaration) of stripped Kotlin source."""
    parser = _KtParser(tokens(stripped))
    root = KtDecl("file", "", 1, set(), set(), None)
    parser.members(root, 0)
    return parser.package, parser.imports, root


def _type_root(type_tokens):
    """The outermost type's name: Outcome for persona.core.domainkit.Outcome<Item, Fail>?, None for none."""
    root = None
    for value in type_tokens:
        if value in ("<", "(", ",", ">", ")", "?", "->"):
            break
        if _ident(value):
            root = value
    return root


def _first_type_argument(type_tokens):
    if "<" not in type_tokens:
        return None
    return _type_root(type_tokens[type_tokens.index("<") + 1:])


# ---------------------------------------------------------------------------------------------------------------------
# Kotlin rules


def _kt_owner(decl):
    """The class a member answers for: its class, the class of its companion object, or an extension's receiver."""
    if decl.receiver:
        return _type_root(decl.receiver)
    parent = decl.parent
    if parent is not None and parent.kind == "object" and "companion" in parent.mods and parent.parent is not None:
        return parent.parent.name
    if parent is not None and parent.kind in ("class", "interface", "object"):
        return parent.name
    return None


def _in_companion(decl):
    return decl.parent is not None and decl.parent.kind == "object" and "companion" in decl.parent.mods


def _kt_path(decl):
    """A declaration's names from the file down, companion objects left out: ["Outcome", "fold"]."""
    names = []
    while decl is not None and decl.kind != "file":
        if not (decl.kind == "object" and "companion" in decl.mods):
            names.append(decl.name)
        decl = decl.parent
    return names[::-1]


def _builds(type_tokens, owner):
    """Whether a result is its owner's own type or an Outcome of it, nullable or not."""
    if owner is None or not type_tokens:
        return False
    root = _type_root(type_tokens)
    return root == owner or (root == "Outcome" and _first_type_argument(type_tokens) == owner)


def _returns_own(type_tokens, owner):
    """Whether a result is its owner's own type or an Outcome of it, not nullable (absence is not a transition)."""
    return _builds(type_tokens, owner) and type_tokens[-1] != "?"


def _kt_stored(klass):
    """A class's stored state: each primary-constructor parameter and each property of its body that has a backing
    field (an initializer, or no getter of its own), as (name, type tokens, line)."""
    out = [(p.name, p.type, p.line) for p in klass.params]
    out += [(c.name, c.type, c.line) for c in klass.children if c.kind in ("val", "var") and c.receiver is None
            and (c.initializer or not c.getter) and "abstract" not in c.mods]
    return out


def _kt_field_names(root):
    """The names of the fields a file's classes store: constructor properties and body properties with a backing
    field."""
    names = set()
    for decl in root.walk():
        if decl.kind == "class":
            names.update(p.name for p in decl.params if p.binding)
            names.update(name for name, _, _ in _kt_stored(decl)[len(decl.params):])
    return names


def kotlin_fields(texts):
    """The names of the fields a module's classes store, for the rule that a body returning one is a getter."""
    names = set()
    for text in texts:
        names.update(_kt_field_names(parse_kotlin(strip(text, "kotlin"))[2]))
    return names


def _kt_closed_set(klass):
    """Whether a class is a closed set: its only state is one value of a private or internal enum class or sealed
    type declared inside it, which its private constructor takes (`Urgency private constructor(private val urgency:
    Case)`)."""
    if klass is None or klass.kind != "class" or klass.ctor != "private" or len(klass.params) != 1 \
            or len(_kt_stored(klass)) != 1:
        return False
    case = _type_root(klass.params[0].type)
    return any(child.kind in ("class", "interface") and child.name == case and child.mods & {"enum", "sealed"}
               and child.mods & {"private", "internal"} for child in klass.children)


def _kt_identity(type_tokens, declared):
    """Whether an id() result is the module's own identity class: one name, declared in the module, ending in Id."""
    return (type_tokens is not None and len(type_tokens) == 1 and type_tokens[0] in declared
            and type_tokens[0].endswith("Id") and len(type_tokens[0]) > 2)


def _kt_reference(name, module, ctx):
    """For a dotted name a domain module (or the kit) imports or writes out: None when it may, else (rule, what)."""
    if name.startswith("persona.core.domainkit.") and not name.startswith("persona.core.domainkit.adapt."):
        return None
    m = re.match(r"persona\.core\.([a-z0-9_]+)\.domain(?:\.|$)", name)
    if m and module == "domain":
        if m.group(1) == ctx:
            return None
        return "kotlin-context", "from another context's domain module"
    return "kotlin-import", None


def kotlin_declared(texts):
    """The classes, interfaces and objects a module's sources declare, nested ones included."""
    names = set()
    for text in texts:
        _, _, root = parse_kotlin(strip(text, "kotlin"))
        names.update(d.name for d in root.walk() if d.kind in ("class", "interface", "object") and d.name)
    return names


def kotlin_source_findings(path, text, module, ctx=None, declared=None, fields=None):
    """Findings for one Kotlin source file of a domain module (module "domain") or of the kit (module "kit"). ctx: the
    module's context (by default the one its package names); declared: the classes the module declares, and fields
    the names of the fields they store (kotlin_declared, kotlin_fields; by default this file's)."""
    stripped = strip(text, "kotlin")
    package, imports, root = parse_kotlin(stripped)
    if ctx is None:
        m = re.match(r"persona\.core\.([a-z0-9_]+)\.domain(?:\.|$)", package)
        ctx = m.group(1) if m else None
    if declared is None:
        declared = {d.name for d in root.walk() if d.kind in ("class", "interface", "object") and d.name}
    if fields is None:
        fields = _kt_field_names(root)
    found = []
    add = lambda line, rule, what: found.append(Finding(path, line, rule, what))  # noqa: E731
    toks = tokens(stripped)
    for name, line in imports:
        refused = _kt_reference(name, module, ctx)
        if refused:
            rule, whose = refused
            add(line, rule, f"imports {name}" + (f", {whose}" if whose else ""))
    _kotlin_token_rules(toks, module, ctx, add)
    for decl in root.walk():
        _kotlin_decl_rules(decl, add, module, declared, fields)
    return sorted(set(found))


_KT_FQN = re.compile(r"^(?:java|javax|kotlinx|android|androidx|dalvik|okhttp3|retrofit2)$")
_KT_FQN_KOTLIN = frozenset(("random", "io", "concurrent", "time", "system", "reflect", "coroutines"))
_KT_BANNED_CALLS = {
    ("Instant", "now"): "reads the clock", ("System", None): "reads the platform",
    ("UUID", "randomUUID"): "makes an id", ("Random", None): "draws a random number",
    ("Clock", None): "reads the clock", ("Thread", None): "uses a thread",
    ("LocalDateTime", "now"): "reads the clock", ("LocalDate", "now"): "reads the clock",
    ("ZonedDateTime", "now"): "reads the clock", ("OffsetDateTime", "now"): "reads the clock",
}
_KT_BANNED_BARE = {"println": "prints", "print": "prints", "runBlocking": "blocks a thread", "readLine": "reads input",
                   "Random": "draws a random number", "synchronized": "takes a lock",
                   "lazy": "caches a value behind a lock"}
# Members called on a value: `items.shuffled()`.
_KT_BANNED_MEMBERS = {"random": "draws a random number", "randomOrNull": "draws a random number",
                      "shuffled": "draws a random order", "shuffle": "draws a random order"}
_KT_BANNED_ANNOTATIONS = {"Synchronized": "takes a lock", "Volatile": "shares state between threads"}
_KT_HIDDEN_THROWS = frozenset(("error", "require", "requireNotNull", "check", "checkNotNull", "TODO"))
_KT_CODEC_NAMES = frozenset(("Serializable", "SerialName", "Parcelize", "Parcelable", "Transient"))
# Collections and arrays that change in place, their types and the calls that make them.
_KT_MUTABLE = re.compile(
    r"^(?:Mutable[A-Z]\w*|mutable[A-Z]\w*Of|toMutable[A-Z]\w*|asMutable[A-Z]\w*"
    r"|(?:array|arrayList|hashMap|hashSet|linkedMap|linkedSet|sortedMap|sortedSet)Of|arrayOfNulls|emptyArray"
    r"|(?:int|long|short|byte|char|float|double|boolean|uInt|uLong|uShort|uByte)ArrayOf|toTypedArray"
    r"|to(?:Int|Long|Short|Byte|Char|Float|Double|Boolean|UInt|ULong|UShort|UByte)Array"
    r"|Array|(?:Int|Long|Short|Byte|Char|Float|Double|Boolean|UInt|ULong|UShort|UByte)Array"
    r"|ArrayList|ArrayDeque|HashMap|HashSet|LinkedHashMap|LinkedHashSet|TreeMap|TreeSet|WeakHashMap|IdentityHashMap"
    r"|EnumMap|StringBuilder)$")


def _kotlin_token_rules(toks, module, ctx, add):
    n = len(toks)
    head = ""
    for i, tok in enumerate(toks):
        if tok.first:
            head = tok.v
        if head in ("package", "import"):
            continue  # kotlin-import reads these lines
        value, prev = tok.v, (toks[i - 1].v if i else "")
        after = toks[i + 1].v if i + 1 < n else ""
        if value == "var" and prev != ".":
            add(tok.line, "kotlin-var", "declares a var")
        elif value == "throw" and not (after == "Defect" and i + 2 < n and toks[i + 2].v == "("):
            add(tok.line, "kotlin-throw", "throws something other than a Defect")
        elif value == "!!":
            add(tok.line, "kotlin-throw", "uses !!, which throws")
        elif value in _KT_HIDDEN_THROWS and after == "(" and prev not in (".", "fun", "::"):
            add(tok.line, "kotlin-throw", f"calls {value}(), which throws")
        elif value in _KT_BANNED_BARE and after in ("(", "{") and prev not in (".", "fun", "::"):
            add(tok.line, "kotlin-banned-call", f"calls {value}() ({_KT_BANNED_BARE[value]})")
        elif value in _KT_BANNED_MEMBERS and prev in (".", "?.") and after in ("(", "{"):
            add(tok.line, "kotlin-banned-call", f"calls .{value}() ({_KT_BANNED_MEMBERS[value]})")
        elif value in _KT_BANNED_ANNOTATIONS and prev == "@":
            add(tok.line, "kotlin-banned-call", f"marks a declaration @{value} ({_KT_BANNED_ANNOTATIONS[value]})")
        elif value == "suspend" and prev != ".":
            add(tok.line, "kotlin-banned-call", "declares a suspend function or type (runs off its caller's thread)")
        elif prev != "." and i + 2 < n and toks[i + 1].v == "." and (value, toks[i + 2].v) in _KT_BANNED_CALLS:
            member = toks[i + 2].v
            add(tok.line, "kotlin-banned-call", f"calls {value}.{member} ({_KT_BANNED_CALLS[value, member]})")
        elif prev != "." and after == "." and (value, None) in _KT_BANNED_CALLS:
            add(tok.line, "kotlin-banned-call", f"uses {value} ({_KT_BANNED_CALLS[value, None]})")
        elif value in _KT_CODEC_NAMES and prev in ("@", ".", ":", ","):
            add(tok.line, "kotlin-serializable", f"uses {value}")
        elif value == "IgnorableReturnValue" and prev in ("@", ".", ":", "["):
            add(tok.line, "kotlin-ignorable", "marks a result @IgnorableReturnValue")
        elif value == "OptIn" and prev in ("@", ".", ":") and after == "(":
            end = i + 2
            while end < n and toks[end].v != ")":
                end += 1
            marks = {t.v for t in toks[i + 2:end]} & {"DomainBoundary", "ClockRead"}
            if marks:
                add(tok.line, "kotlin-optin", f"opts in to {', '.join(sorted(marks))} in a domain module")
        elif value == "ClockRead" and prev == "@" and module != "kit":
            add(tok.line, "kotlin-optin", "marks a declaration @ClockRead outside the kit")
        elif _KT_MUTABLE.match(value):
            add(tok.line, "kotlin-state", f"uses {value}, which changes in place")
        if prev != "." and _KT_FQN.match(value) and after == ".":
            add(tok.line, "kotlin-import", f"names {value}.{toks[i + 2].v if i + 2 < n else ''} by its full name")
        if value == "kotlin" and prev != "." and after == "." and i + 2 < n and toks[i + 2].v in _KT_FQN_KOTLIN:
            add(tok.line, "kotlin-import", f"names kotlin.{toks[i + 2].v} by its full name")
        if value == "persona" and prev != "." and after == ".":
            j = i
            while j + 2 < n and toks[j + 1].v == "." and _ident(toks[j + 2].v):
                j += 2
            name = "".join(t.v for t in toks[i:j + 1])
            refused = _kt_reference(name, module, ctx)
            if refused:
                rule, whose = refused
                add(tok.line, rule, f"names {name} by its full name" + (f", {whose}" if whose else ""))


def _kotlin_decl_rules(decl, add, module, declared, fields):
    public = decl.public
    kit = module == "kit"
    path = _kt_path(decl)

    def excepted(rule, key):
        return kit and _kit_exception(rule, KOTLIN_KIT_NAME, key)

    if decl.kind in ("class", "interface", "object"):
        label = decl.name or "an object"
        if "data" in decl.mods and decl.kind == "class":
            add(decl.line, "kotlin-data-class", f"{label} is a data class")
        if decl.kind == "class" and decl.mods & {"value", "inline"}:
            add(decl.line, "kotlin-value-class", f"{label} is a value class")
        stored = _kt_stored(decl) if decl.kind == "class" and "annotation" not in decl.mods else []
        if len(stored) > 1:  # the one wrapped field of a single-field value object is the exception
            for name, typed, line in stored:
                if not typed:
                    add(line, "kotlin-primitive-field", f"{label}.{name} has no declared type, one of {len(stored)} "
                                                        "fields")
                elif set(typed) & KOTLIN_PRIMITIVES:
                    add(line, "kotlin-primitive-field",
                        f"{label}.{name} is {' '.join(typed)}, one of {len(stored)} fields")
        if public and ("sealed" in decl.mods or "enum" in decl.mods or (decl.kind == "object" and "data" in decl.mods)):
            shape = ("an enum class" if "enum" in decl.mods else "a data object" if "data" in decl.mods
                     else "a sealed type")
            add(decl.line, "kotlin-public-sealed", f"{label} is public and {shape}")
        if decl.kind == "object" and "companion" not in decl.mods:
            if decl.parent.kind == "file":
                add(decl.line, "kotlin-state", f"{label} is a package-level object")
            else:
                held = [c.name for c in decl.children if c.kind in ("val", "var")]
                if held:
                    add(decl.line, "kotlin-state", f"{label} is an object that holds {held[0]}")
        if decl.kind == "interface" and decl.name.endswith("Visitor"):
            for child in decl.children:
                if child.kind == "fun" and not child.name.startswith("visit"):
                    add(child.line, "kotlin-visitor-name", f"{decl.name}.{child.name}() is not named visit…")
                elif child.kind in ("val", "var"):
                    add(child.line, "kotlin-visitor-name", f"{decl.name}.{child.name} is a property, not a visit…")
        if public and decl.generic and not excepted("kotlin-generic", path[0]):
            add(decl.line, "kotlin-generic", f"{label} declares a type parameter")
        if public and decl.kind == "interface" and "sealed" not in decl.mods and not decl.name.endswith("Visitor") \
                and not excepted("kotlin-extensible", ".".join(path)):
            add(decl.line, "kotlin-extensible", f"{label} is a public interface")
        if public and decl.kind == "class" and decl.mods & {"abstract", "open"}:
            shape = "abstract" if "abstract" in decl.mods else "open"
            add(decl.line, "kotlin-extensible", f"{label} is a public {shape} class")
        if public and decl.kind == "class":
            for param in decl.params:
                if param.binding and not (param.mods & {"private", "internal"}):
                    add(param.line, "kotlin-ctor-property",
                        f"{label}'s constructor property {param.name} is visible outside the module")
            constructible = not (decl.mods & {"abstract", "sealed", "annotation", "enum"})
            secondaries = [c for c in decl.children if c.kind == "constructor"]
            if constructible:
                if decl.ctor in ("", "public") or (decl.ctor is None and not secondaries):
                    add(decl.line, "kotlin-public-constructor", f"{label} has a public constructor")
                for ctor in secondaries:
                    if not (ctor.mods & {"private", "internal", "protected"}):
                        add(ctor.line, "kotlin-public-constructor", f"{label} has a public secondary constructor")
        return
    if decl.kind == "constructor":
        return
    if decl.kind == "typealias":  # whatever its visibility: a private one hides a primitive from the field rules too
        add(decl.line, "kotlin-alias", f"{decl.name} is a type alias of {' '.join(decl.type or []) or 'another type'}")
        return
    if decl.kind in ("val", "var") and decl.parent.kind == "file" and decl.receiver is None \
            and "const" not in decl.mods:
        add(decl.line, "kotlin-state", f"{decl.name} is package-level state")
    if not public:
        return
    owner = _kt_owner(decl)
    marks = decl.annos & {"DomainBoundary", "ClockRead"}
    override = "override" in decl.mods
    companion = _in_companion(decl)
    method = decl.receiver is not None or (decl.parent.kind in ("class", "interface", "object") and not companion)
    name = decl.name
    is_fun = decl.kind == "fun"
    label = (f"{owner}.{name}" if owner else name) + ("()" if is_fun else "")
    key = ".".join(path)
    if is_fun and name == "wire" and not method:  # marked or not: the boundary is a member's
        add(decl.line, "kotlin-boundary", f"{label} is a companion or top-level wire(), not a member's")
    # A member behind an opt-in is the boundary the way Swift's @_spi members are: the API rules below leave it to
    # kotlin-boundary, which allows only the shapes of the documented boundary.
    if marks:
        wire = is_fun and name == "wire" and not decl.params and method
        kit_factory = kit and is_fun and companion
        for mark in sorted(marks):
            allowed = kit_factory if mark == "ClockRead" else (wire or kit_factory)
            if not allowed and (kit or mark == "DomainBoundary"):
                add(decl.line, "kotlin-boundary", f"{label} is marked @{mark}")
        return
    types = [p.type for p in decl.params] + [decl.type or []]
    if any("->" in t for t in types) and not excepted("kotlin-callback", key):
        add(decl.line, "kotlin-callback", f"{label} takes or returns a function")
    if is_fun:
        identity = override and name in ("equals", "hashCode", "compareTo")
        if decl.generic and not excepted("kotlin-generic", path[0]):
            add(decl.line, "kotlin-generic", f"{label} declares a type parameter")
        if name in ("evolve", "apply"):
            add(decl.line, "kotlin-public-evolve", f"{label} is public")
        if len(decl.params) > MAX_PARAMS:
            add(decl.line, "kotlin-factory", f"{label} takes {len(decl.params)} parameters")
        if companion:
            _kotlin_factory_rules(decl, add, owner, label, declared)
        if not identity:
            parse = name == "parse" and companion
            loose = [p.name for p in decl.params if set(p.type) & KOTLIN_PRIMITIVES] if not parse else []
            result = set(decl.type or []) & KOTLIN_PRIMITIVES
            if loose:
                add(decl.line, "kotlin-primitive-api", f"{label} takes {', '.join(loose)} as a primitive")
            if result and decl.type != ["Boolean"]:
                add(decl.line, "kotlin-primitive-api", f"{label} returns {' '.join(decl.type)}")
        handed_out = False
        if (method or not decl.params) and not identity and not excepted("kotlin-accessor", key):
            root = _type_root(decl.type or [])
            told = decl.type is None or decl.type in (["Boolean"], ["Unit"]) or _returns_own(decl.type, owner)
            told = told or (name == "id" and not decl.params and _kt_identity(decl.type, declared))
            told = told or (name == "build" and not decl.params and (owner or "").endswith("Builder")
                            and root == "Outcome" and _first_type_argument(decl.type) in declared)
            told = told or ((owner or "").endswith("Step") and decl.type is not None and len(decl.type) == 1
                            and root in declared and root.endswith(("Step", "Builder")))
            if not told:
                add(decl.line, "kotlin-accessor", f"{label} hands out {' '.join(decl.type)}")
                handed_out = True
        # Whatever it returns and wherever it is declared, a body that only returns an input's field is a getter; a
        # member id() of the module's own identity class is the one the rule above lets through.
        member_identity = method and name == "id" and not decl.params and _kt_identity(decl.type, declared)
        read = None if identity or handed_out or member_identity else _field_read(
            decl.body, {p.name for p in decl.params}, "this" if method else None, fields)
        if read and (decl.type != ["Boolean"] or read[1] in fields):
            add(decl.line, "kotlin-accessor", f"{label} hands state out, returning {read[0]}")
        return
    # A val or var: the stored or computed value of a class, a companion value, or a package-level value.
    typed = decl.type or []
    computed_bool = decl.getter and not decl.initializer and typed == ["Boolean"]
    companion_value = companion and (
        _type_root(typed) == owner or (not typed and decl.initializer and decl.init_first == owner))
    if set(typed) & KOTLIN_PRIMITIVES and not computed_bool:
        add(decl.line, "kotlin-primitive-api", f"{label} is public and has the type {' '.join(typed)}")
    read = _field_read(decl.body, set(), "this" if method else None, fields) if computed_bool else None
    if read and read[1] in fields:
        add(decl.line, "kotlin-accessor", f"{label} hands state out, returning {read[0]}")
    if not computed_bool and not companion_value:
        what = "a package-level value" if decl.parent.kind == "file" else "state visible outside the module"
        add(decl.line, "kotlin-accessor", f"{label} is {what}; a public val is a computed Boolean")


def _kotlin_factory_rules(decl, add, owner, label, declared):
    """A companion function's name and result: a factory is named for how it builds, and returns what that name
    promises, never null."""
    name, typed = decl.name, decl.type or []
    shown = " ".join(typed) or "Unit"
    root = _type_root(typed)
    stage = len(typed) == 1 and root in declared and root.endswith(("Step", "Builder"))
    builds = _builds(typed, owner)
    if builds and name not in FACTORY_NAMES and (not _kt_closed_set(decl.parent.parent) or BANNED_FACTORY.match(name)):
        add(decl.line, "kotlin-factory",
            f"{label} builds a {owner} under a name other than parse, new, restore, newBuilder or restoreBuilder")
    if builds and "?" in typed:
        add(decl.line, "kotlin-factory", f"{label} returns {shown}, which may be null")
    if name == "restore" and not (root == "Outcome" and _first_type_argument(typed) == owner):
        add(decl.line, "kotlin-factory", f"{label} returns {shown}, not an Outcome of {owner}")
    elif name in ("parse", "new") and not builds and not (name == "new" and (owner or "").endswith("Builder")
                                                           and stage):
        add(decl.line, "kotlin-factory", f"{label} returns {shown}, not a {owner} or an Outcome of it")
    elif name in ("newBuilder", "restoreBuilder") and not stage:
        add(decl.line, "kotlin-factory", f"{label} returns {shown}, not the first …Step or …Builder of {owner}")


def kotlin_build_findings(path, text, kit):
    """Findings for a domain module's (or the kit's) build.gradle.kts."""
    source = strip(text, "kotlin", keep_strings=True)
    found = []
    add = lambda what, rule="kotlin-build-flags", line=0: found.append(Finding(path, line, rule, what))  # noqa: E731
    flat = re.sub(r"\s+", "", source)
    plugins = re.findall(r"\bid\(\"([^\"]+)\"\)|`([\w-]+)`|\bkotlin\(\"([^\"]+)\"\)|\balias\(([^)]*)\)",
                         _block(source, "plugins"))
    names = [next(part for part in match if part) for match in plugins]
    if "persona.jvm.library" not in names:
        add("does not apply persona.jvm.library")
    for other in names:
        if other != "persona.jvm.library":
            add(f"applies the plugin {other}", "kotlin-module")
    if "explicitApi()" not in flat:
        add("does not call explicitApi()")
    if "allWarningsAsErrors.set(true)" not in flat and "allWarningsAsErrors=true" not in flat:
        add("does not set allWarningsAsErrors to true")
    if "\"-Xreturn-value-checker=full\"" not in flat:
        add("does not add -Xreturn-value-checker=full")
    dependencies = _block(source, "dependencies")
    for line_number, line in enumerate(dependencies.splitlines(), _block_line(source, "dependencies")):
        m = re.match(r"\s*\"?([A-Za-z_]\w*)\"?\s*\((.*)\)\s*$", line)
        if not m or m.group(1).startswith(("test", "androidTest")):
            continue
        target = re.fullmatch(r"project\(\s*\"([^\"]+)\"\s*\)|projects((?:\.\w+)+)", m.group(2).strip())
        project = None
        if target:
            project = target.group(1) or ":" + ":".join(target.group(2).strip(".").split("."))
        if not kit and project is not None and re.fullmatch(r":core:[a-z0-9_]+:domain", project):
            add(f"depends on {project}, another context's domain module", "kotlin-context", line_number)
        elif kit or project != ":core:domainkit":
            add(f"depends on {m.group(2).strip()}", "kotlin-module", line_number)
    return found


def _block(source, name):
    m = re.search(r"(?m)^\s*" + name + r"\s*\{", source)
    if not m:
        return ""
    depth, i = 0, m.end() - 1
    while i < len(source):
        if source[i] == "{":
            depth += 1
        elif source[i] == "}":
            depth -= 1
            if depth == 0:
                return source[m.end():i]
        i += 1
    return source[m.end():]


def _block_line(source, name):
    m = re.search(r"(?m)^\s*" + name + r"\s*\{", source)
    return _line_of(source, m.end()) if m else 0


def kotlin_build_logic_findings(files):
    """The Android-wide flags in android/build-logic. files: {repository-relative path: text} of ReturnValues.kt and the
    application, library and JVM conventions."""
    found = []
    values = next((text for path, text in files.items() if path.endswith("ReturnValues.kt")), None)
    values_path = next((path for path in files if path.endswith("ReturnValues.kt")),
                       "android/build-logic/convention/src/main/kotlin/persona/buildlogic/ReturnValues.kt")
    if values is None:
        found.append(Finding(values_path, 0, "kotlin-build-flags", "is missing"))
    else:
        for flag in ("-Xreturn-value-checker=check", "-Xwarning-level=RETURN_VALUE_NOT_USED:error"):
            if f"\"{flag}\"" not in strip(values, "kotlin", keep_strings=True):
                found.append(Finding(values_path, 0, "kotlin-build-flags", f"no longer adds {flag}"))
        for m in re.finditer(r"\"(:core:[\w-]+:domain|:core:domainkit)\"", values):
            found.append(Finding(values_path, _line_of(values, m.start()), "kotlin-build-flags",
                                 f"keeps {m.group(1)}'s dropped results as warnings"))
    for path, text in files.items():
        if path.endswith(".gradle.kts") and "configureReturnValueChecks()" not in strip(text, "kotlin"):
            found.append(Finding(path, 0, "kotlin-build-flags", "does not call configureReturnValueChecks()"))
    return found


# A file that uses a domain module or the kit: it imports one, or names one in full (persona.core.domain, an older
# module, is not one).
_KT_USES_DOMAIN = re.compile(r"\bpersona\s*\.\s*core\s*\.\s*(?:(?!domain\b)[a-z0-9_]+\s*\.\s*domain|domainkit)\b")


def kotlin_outside_findings(path, text, place, visitors, functions=frozenset(), ctx=None, declared=frozenset()):
    """Findings for Android code outside the domain modules' main sources. place: "adapt", "kit-adapt", "test" or
    "other"; visitors: the domain visitor interfaces' names; functions: the public functions the domain modules
    declare, and declared their classes (kotlin_declared); ctx: an adapt package's context. Only a file that imports a
    domain module or the kit, or names one in full, is read for dropped results."""
    stripped = strip(text, "kotlin")
    found = []
    for m in re.finditer(r"\b(DomainBoundary|ClockRead)\b", stripped):
        mark = m.group(1)
        allowed = place == "kit-adapt" or (place == "adapt" and mark == "DomainBoundary")
        if path.endswith(".kts") or not allowed:
            found.append(Finding(path, _line_of(stripped, m.start()), "kotlin-optin", f"names {mark}"))
    if not path.endswith(".kt"):
        return sorted(set(found))
    if place == "adapt":
        for m in re.finditer(r"(?m)^[ \t]*import[ \t]+persona\.core\.([a-z0-9_]+)\.adapt\b", stripped):
            if m.group(1) not in (ctx, "domainkit"):
                found.append(Finding(path, _line_of(stripped, m.start(1)), "kotlin-context",
                                     f"imports persona.core.{m.group(1)}.adapt, another context's adapter"))
    if visitors and place not in ("adapt", "kit-adapt", "test"):
        _, _, root = parse_kotlin(stripped)
        for decl in root.walk():
            hit = sorted(set(decl.supers) & visitors)
            if hit:
                found.append(Finding(path, decl.line, "kotlin-visitor-impl",
                                     f"{decl.name or 'an object'} implements {', '.join(hit)}"))
        for m in re.finditer(r"\bobject\s*:\s*([\w.<>, ]+?)\s*[{(]", stripped):
            hit = sorted(set(re.findall(r"\w+", m.group(1))) & visitors)
            if hit:
                found.append(Finding(path, _line_of(stripped, m.start()), "kotlin-visitor-impl",
                                     f"an object implements {', '.join(hit)}"))
    if functions and place in ("adapt", "other") and _KT_USES_DOMAIN.search(stripped):
        for m in re.finditer(r"\bval\s+_\s*(?::[^=\n]*)?=(?!=)\s*", stripped):
            called = _dropped_calls(_expression_at(stripped, m.end()), functions, declared)
            if called:
                found.append(Finding(path, _line_of(stripped, m.start()), "kotlin-app-discard",
                                     f"drops the result of {called[0]}"))
    return sorted(set(found))


def kotlin_visitors(texts):
    """The visitor interfaces the domain modules declare."""
    names = set()
    for text in texts:
        names.update(re.findall(r"\binterface\s+(\w+Visitor)\b", strip(text, "kotlin")))
    return names


def kotlin_public_functions(texts):
    """The names of the functions the domain modules declare public that return something, for dropped results."""
    names = set()
    for text in texts:
        _, _, root = parse_kotlin(strip(text, "kotlin"))
        names.update(d.name for d in root.walk() if d.kind == "fun" and d.public and d.type not in (None, ["Unit"]))
    return names


# ---------------------------------------------------------------------------------------------------------------------
# Swift manifests

SwiftTarget = namedtuple("SwiftTarget", "package name role sources manifest line deps settings")
_TARGET_CALL = re.compile(r"\.(target|testTarget|executableTarget|binaryTarget|macro|plugin|systemLibrary)\s*\(")
# Every target named like a domain module is one, whatever comes before Domain or Adapt; NAMED says how it must be
# named (Persona<Ctx>Domain, Persona<Ctx>Adapt, Persona<Ctx>DomainTests), so a target missing the Persona prefix is
# checked and fails discovery rather than passing unseen.
DOMAIN_TARGET = re.compile(r"^\w*Domain$")
ADAPT_TARGET = re.compile(r"^\w*Adapt$")
DOMAIN_TESTS = re.compile(r"^\w*Domain(?:Kit)?Tests$")
NAMED = re.compile(r"^Persona(?:[A-Z]\w*(?:Domain|Adapt|DomainTests)|DomainKitTests)$")
KIT = "PersonaDomainKit"
KIT_ADAPT = "PersonaDomainKitAdapt"


def swift_role(name, call="target"):
    if call == "testTarget":
        return "test-domain" if DOMAIN_TESTS.match(name) else "other"
    if name == KIT:
        return "kit"
    if name == KIT_ADAPT:
        return "kit-adapt"
    if DOMAIN_TARGET.match(name):
        return "domain"
    if ADAPT_TARGET.match(name):
        return "adapt"
    return "other"


def _balanced(text, i, open_char="(", close_char=")"):
    """The text between the bracket at i and its partner (strings already blanked or kept whole)."""
    depth, j, in_string = 0, i, False
    while j < len(text):
        c = text[j]
        if c == "\"" and text[j - 1] != "\\":
            in_string = not in_string
        elif not in_string and c == open_char:
            depth += 1
        elif not in_string and c == close_char:
            depth -= 1
            if depth == 0:
                return text[i + 1:j]
        j += 1
    return text[i + 1:]


def _argument(arguments, start):
    """One argument's text: from start to the comma that ends it at depth 0, or to the end."""
    depth, in_string = 0, False
    for j in range(start, len(arguments)):
        c = arguments[j]
        if c == "\"" and arguments[j - 1] != "\\":
            in_string = not in_string
        elif in_string:
            continue
        elif c in "([{":
            depth += 1
        elif c in ")]}":
            depth -= 1
        elif c == "," and depth == 0:
            return arguments[start:j]
    return arguments[start:]


def _manifest_value(arguments, label, manifest):
    """A labelled argument's arrays: an array literal, or arrays the manifest declares by name, joined by +."""
    m = re.search(r"\b" + label + r"\s*:\s*", arguments)
    if not m:
        return ""
    out = []
    for part in _argument(arguments, m.end()).split("+"):
        part = part.strip()
        if part.startswith("["):
            out.append(_balanced(part, 0, "[", "]"))
        elif re.fullmatch(r"\w+", part):
            declared = re.search(r"\b(?:let|var)\s+" + part + r"\b[^=]*=\s*\[", manifest)
            if declared:
                out.append(_balanced(manifest, declared.end() - 1, "[", "]"))
    return ",".join(out)


def parse_manifest(package, manifest_path, text):
    """Every target a Package.swift declares (comments stripped, strings kept)."""
    source = strip(text, "swift", keep_strings=True)
    targets = []
    for m in _TARGET_CALL.finditer(source):
        arguments = _balanced(source, m.end() - 1)
        name = re.search(r"\bname\s*:\s*\"([^\"]+)\"", arguments)
        if not name:
            continue
        path = re.search(r"\bpath\s*:\s*\"([^\"]+)\"", arguments)
        default = "Tests/" if m.group(1) == "testTarget" else "Sources/"
        sources = package + "/" + (path.group(1).strip("/") if path else default + name.group(1))
        deps_text = _manifest_value(arguments, "dependencies", source)
        deps_text = re.sub(r"\bpackage\s*:\s*\"[^\"]*\"", "", deps_text)
        deps = re.findall(r"\"([^\"]+)\"", deps_text)
        settings = _manifest_value(arguments, "swiftSettings", source)
        targets.append(SwiftTarget(package, name.group(1), swift_role(name.group(1), m.group(1)), sources,
                                   manifest_path, _line_of(source, m.start()), deps, settings))
    return targets


_SWIFT_SETTINGS = (
    (".swiftLanguageMode(.v6)", ".swiftLanguageMode(.v6)"),
    (".defaultIsolation(nil)", ".defaultIsolation(nil)"),
    (".enableUpcomingFeature(\"InternalImportsByDefault\")", "the upcoming feature InternalImportsByDefault"),
    (".enableUpcomingFeature(\"MemberImportVisibility\")", "the upcoming feature MemberImportVisibility"),
    (".enableUpcomingFeature(\"ExistentialAny\")", "the upcoming feature ExistentialAny"),
)
_SWIFT_FLAGS = (("-warnings-as-errors",), ("-Xfrontend", "-disable-implicit-concurrency-module-import"),
                ("-Xfrontend", "-disable-implicit-string-processing-module-import"),
                ("-Xfrontend", "-reflection-metadata-for-debugger-only"))


def swift_manifest_findings(target):
    """A Domain target's (or the kit's) dependencies and settings."""
    found = []
    add = lambda what, rule="swift-manifest": found.append(  # noqa: E731
        Finding(target.manifest, target.line, rule, what))
    for dep in target.deps:
        if target.role != "kit" and DOMAIN_TARGET.match(dep):
            add(f"{target.name} depends on {dep}, another context's core", "swift-context")
        elif target.role == "kit" or dep != KIT:
            add(f"{target.name} depends on {dep}")
    flat = re.sub(r"\s+", "", target.settings)
    for needle, label in _SWIFT_SETTINGS:
        if needle not in flat:
            add(f"{target.name}'s swiftSettings lack {label}")
    flags = []
    for m in re.finditer(r"\.unsafeFlags\(\[", flat):
        flags.extend(re.findall(r"\"([^\"]*)\"", _balanced(flat, m.end() - 1, "[", "]")))
    for group in _SWIFT_FLAGS:
        if not any(tuple(flags[i:i + len(group)]) == group for i in range(len(flags))):
            add(f"{target.name}'s unsafeFlags lack {' '.join(group)}")
    return found


def swift_adapt_manifest_findings(target):
    """An *Adapt target depends on no other context's *Adapt target."""
    return [Finding(target.manifest, target.line, "swift-context",
                    f"{target.name} depends on {dep}, another context's adapter")
            for dep in target.deps if ADAPT_TARGET.match(dep) and dep not in (KIT_ADAPT, target.name)]


def swift_name_findings(target):
    """A domain, adapter or domain test target is named Persona<Ctx>Domain, Persona<Ctx>Adapt or
    Persona<Ctx>DomainTests."""
    if target.role not in ("domain", "adapt", "test-domain") or NAMED.match(target.name):
        return []
    suffix = re.search(r"(Domain(?:Kit)?Tests|Domain|Adapt)$", target.name).group(1)
    return [Finding(target.manifest, target.line, "swift-discovery",
                    f"{target.name} is named like a domain target, but not Persona<Ctx>{suffix}")]


def swift_testable_findings(apple_sh, packages, path="scripts/ci/apple.sh"):
    """scripts/ci/apple.sh tests every domain package black-box, emits its symbol graph and runs AppleDomain."""
    found = []
    lines = apple_sh.splitlines()
    if not any("test_client_domain_modules.py" in line and "--swift-packages" in line for line in lines):
        found.append(Finding(path, 0, "swift-testable", "no longer lists the domain packages with --swift-packages"))
    step = [line for line in lines if "swift test" in line and "-emit-symbol-graph-dir" in line]
    if not step:
        found.append(Finding(path, 0, "swift-testable", "has no `swift test` step that emits the domain symbol graphs"))
    for number, line in enumerate(lines, 1):
        if "swift test" in line and "-emit-symbol-graph-dir" in line:
            for flag in ("--disable-testable-imports", "-symbol-graph-minimum-access-level"):
                if flag not in line:
                    found.append(Finding(path, number, "swift-testable", f"tests the domain packages without {flag}"))
        for package in packages:
            if re.search(re.escape(package) + r"[\"'/\s]", line + " ") and "--package-path" in line \
                    and "--disable-testable-imports" not in line:
                found.append(Finding(path, number, "swift-testable",
                                     f"tests {package} without --disable-testable-imports"))
    if not any("AppleDomain" in line and "PERSONA_DOMAIN_SYMBOL_GRAPHS" in line for line in lines):
        found.append(Finding(path, 0, "swift-testable", "no longer runs AppleDomain over the domain symbol graphs"))
    return found


# ---------------------------------------------------------------------------------------------------------------------
# Swift sources

_SWIFT_IMPORT = re.compile(
    r"(?m)^[ \t]*((?:@[\w]+(?:\([^)\n]*\))?[ \t]+)*)(?:(?:public|package|internal|fileprivate|private)[ \t]+)?"
    r"import[ \t]+(?:(?:typealias|struct|class|enum|protocol|let|var|func)[ \t]+)?([A-Za-z_]\w*)")
_SWIFT_BANNED = (
    (re.compile(r"(?<![\w.])(print|debugPrint|dump)\s*\("), "prints"),
    (re.compile(r"(?<![\w.])(fatalError|preconditionFailure)\s*\("), "traps"),
    (re.compile(r"\.(random|randomElement|shuffled|shuffle)\s*\("), "draws a random number or order"),
    (re.compile(r"\b(SystemRandomNumberGenerator)\b"), "draws a random number"),
    (re.compile(r"(?<![\w.])(Mirror)\s*\("), "reflects on a value"),
    (re.compile(r"\b(ContinuousClock|SuspendingClock)\b"), "reads the clock"),
)
_SWIFT_CONFORMANCES = re.compile(r"\b(Codable|Encodable|Decodable|CustomStringConvertible|CustomDebugStringConvertible|"
                                 r"LosslessStringConvertible|RawRepresentable|CustomReflectable|CodingKey)\b")
_SWIFT_MODIFIERS = frozenset(("public", "open", "internal", "fileprivate", "private", "package", "static", "class",
                              "final", "override", "mutating", "nonmutating", "lazy", "weak", "unowned", "indirect",
                              "nonisolated", "isolated", "consuming", "borrowing", "dynamic", "required",
                              "convenience", "optional", "prefix", "postfix", "infix", "distributed"))
_SWIFT_ACCESS = frozenset(("public", "open", "internal", "fileprivate", "private", "package"))
_SWIFT_TYPE_KINDS = frozenset(("protocol", "struct", "class", "enum", "actor", "extension"))
_SWIFT_HEAD_KEYWORDS = _SWIFT_TYPE_KINDS | frozenset((
    "func", "init", "deinit", "subscript", "var", "let", "get", "set", "didSet", "willSet", "if", "else", "guard",
    "for", "while", "repeat", "switch", "case", "default", "do", "catch", "defer", "in", "return"))


def _swift_modifiers(toks, i):
    """The modifiers and attributes right before token i (`private(set)` included)."""
    mods, j = set(), i - 1
    while j >= 0:
        value = toks[j].v
        if value == ")" and j >= 3 and [t.v for t in toks[j - 2:j]] == ["(", "set"] and toks[j - 3].v in _SWIFT_ACCESS:
            mods.add(toks[j - 3].v + "(set)")
            j -= 4
        elif value in _SWIFT_MODIFIERS:
            mods.add(value)
            j -= 1
        elif value == ")":  # an attribute's arguments
            depth = 0
            while j >= 0:
                if toks[j].v == ")":
                    depth += 1
                elif toks[j].v == "(":
                    depth -= 1
                    if depth == 0:
                        break
                j -= 1
            j -= 1
            if j >= 1 and toks[j - 1].v == "@":
                mods.add("@" + toks[j].v)
                j -= 2
            else:
                break
        elif j >= 1 and toks[j - 1].v == "@" and _ident(value):
            mods.add("@" + value)
            j -= 2
        else:
            break
    return mods


class _SwiftScope(namedtuple("_SwiftScope", "kind name public exposed")):
    """A brace scope: its kind (a type kind, "code" or "file"), the type it declares or extends, whether its members
    default to public (a public extension), and whether anything in it can be public API (no enclosing internal
    type)."""


def _swift_scopes(toks):
    """For each token index, the stack of scopes it is in (as a tuple), computed in one pass."""
    stack = [_SwiftScope("file", None, False, True)]
    head = 0
    at = []
    for i, tok in enumerate(toks):
        at.append(tuple(stack))
        if tok.v == "{":
            kind, name, public, exposed = "code", None, False, False
            keyword_at = None
            for j in range(i - 1, head - 1, -1):
                if toks[j].v in _SWIFT_HEAD_KEYWORDS and not (toks[j].v == "class" and j + 1 < i
                                                               and toks[j + 1].v in ("func", "var", "let")):
                    keyword_at = j
                    break
            if keyword_at is not None and toks[keyword_at].v in _SWIFT_TYPE_KINDS:
                kind = toks[keyword_at].v
                parts = []
                for tok2 in toks[keyword_at + 1:i]:
                    if tok2.v in (":", "<", "where", "{"):
                        break
                    if _ident(tok2.v):
                        parts.append(tok2.v)
                name = parts[-1] if parts else None
                mods = _swift_modifiers(toks, keyword_at)
                public = bool(mods & {"public", "open"})
                exposed = stack[-1].exposed and (kind == "extension" or _swift_public(mods, stack[-1]))
            stack.append(_SwiftScope(kind, name, public, exposed))
            head = i + 1
        elif tok.v == "}":
            if len(stack) > 1:
                stack.pop()
            head = i + 1
        elif tok.v == ";":
            head = i + 1
    return at


def _swift_public(mods, scope):
    if mods & {"public", "open"}:
        return True
    if mods & {"internal", "fileprivate", "private", "package"}:
        return False
    return scope.kind == "extension" and scope.public


_SWIFT_UNSAFE = re.compile(r"^(?:Unsafe|withUnsafe|AutoreleasingUnsafe)\w*$")
_SWIFT_NOT_A_CLASS = frozenset(("func", "var", "let", "subscript", "init", "deinit", "typealias")) | _SWIFT_MODIFIERS


def _swift_binding(toks, i):
    """For the let or var at i: (its annotated type's tokens or None, the index after the type, what follows the name
    and type on its line: "=" for a stored value, "{" for accessors or observers, "" for neither)."""
    n = len(toks)
    j, annotated = i + 2, None
    if j < n and toks[j].v == ":":
        j += 1
        begin = j
        depth = 0
        while j < n and not (depth == 0 and (toks[j].v in ("=", "{") or toks[j].first)):
            depth += toks[j].v in ("<", "[", "(")
            depth -= toks[j].v in (">", "]", ")")
            j += 1
        annotated = [t.v for t in toks[begin:j]]
    follows = toks[j].v if j < n and (toks[j].v in ("=", "{") or not toks[j].first) else ""
    return annotated, j, follows


def _swift_spi_target(toks, i):
    """For the @_spi attribute at i: (its group, the keyword of the declaration it marks, that declaration's name)."""
    n = len(toks)
    group = toks[i + 3].v if i + 3 < n else ""
    j = i + 5
    while j < n:
        if toks[j].v == "@":
            j += 2
            if j < n and toks[j].v == "(":
                depth = 0
                while j < n:
                    depth += toks[j].v == "("
                    depth -= toks[j].v == ")"
                    j += 1
                    if depth == 0:
                        break
        elif toks[j].v in _SWIFT_MODIFIERS and not (toks[j].v == "class" and j + 1 < n
                                                     and toks[j + 1].v not in _SWIFT_NOT_A_CLASS):
            j += 1
            if j < n and toks[j].v == "(":  # private(set)
                j += 3
        else:
            break
    keyword = toks[j].v if j < n else ""
    name = toks[j + 1].v if j + 1 < n and _ident(toks[j + 1].v) else keyword
    return group, keyword, name


def _swift_close(toks, i, open_char, close_char):
    """From the opening bracket at i, the index of its closing partner (or the end)."""
    depth = 0
    while i < len(toks):
        depth += toks[i].v == open_char
        depth -= toks[i].v == close_char
        if depth == 0:
            return i
        i += 1
    return i


SwiftStored = namedtuple("SwiftStored", "name type line")  # type: its tokens, or None when not written


def _swift_struct_state(toks, scopes):
    """For each struct, keyed by its scopes from the file down: its stored instance properties ([SwiftStored]; every
    name of `let a: A, b: B`) and the private enums it declares."""
    stored, enums = {}, {}
    n = len(toks)
    for i, tok in enumerate(toks):
        stack = scopes[i]
        if stack[-1].kind != "struct" or tok.v not in ("let", "var", "enum"):
            continue
        key = tuple((s.kind, s.name) for s in stack[1:])
        stored.setdefault(key, [])
        mods = _swift_modifiers(toks, i)
        if tok.v == "enum":
            if i + 1 < n and _ident(toks[i + 1].v) and mods & {"private", "fileprivate"}:
                enums.setdefault(key, set()).add(toks[i + 1].v)
            continue
        if mods & {"static", "class"}:
            continue
        j = i + 1
        while j < n and _ident(toks[j].v):
            name, line = toks[j].v, toks[j].line
            j += 1
            typed, depth = None, 0
            if j < n and toks[j].v == ":":
                j += 1
                begin = j
                while j < n and not (depth == 0 and (toks[j].v in (",", "=", "{", ";", "}")
                                                     or (j > begin and toks[j].first))):
                    depth += toks[j].v in ("<", "[", "(")
                    depth -= toks[j].v in (">", "]", ")")
                    j += 1
                typed = [t.v for t in toks[begin:j]]
            if j < n and toks[j].v == "=":
                j += 1
                begin = j
                while j < n and not (depth == 0 and (toks[j].v in (",", ";", "}") or (j > begin and toks[j].first))):
                    depth += toks[j].v in ("(", "[", "{")
                    depth -= toks[j].v in (")", "]", "}")
                    j += 1
            if j < n and toks[j].v == "{" and not toks[j].first:
                if j + 1 < n and toks[j + 1].v in ("didSet", "willSet"):
                    stored[key].append(SwiftStored(name, typed, line))
                break  # accessors or observers end the declaration
            stored[key].append(SwiftStored(name, typed, line))
            if j < n and toks[j].v == "," and not toks[j].first:
                j += 1
                continue
            break
    return stored, enums


def _swift_case_values(toks, scopes):
    """Every enum case with associated values: (the enum's names from the file down, joined by dots, the case's name,
    line, [(label, type tokens)])."""
    out = []
    n = len(toks)
    for i, tok in enumerate(toks):
        if tok.v != "case" or scopes[i][-1].kind != "enum":
            continue
        j = i + 1
        while j < n and _ident(toks[j].v):
            case, line = toks[j].v, toks[j].line
            j += 1
            if j < n and toks[j].v == "(":
                end = _swift_close(toks, j, "(", ")")
                values, chunk, depth = [], [], 0
                for t in toks[j + 1:end] + [Token(",", 0, 0, 0, False)]:
                    depth += t.v in ("(", "[", "<")
                    depth -= t.v in (")", "]", ">")
                    if t.v == "," and depth == 0:
                        if chunk:
                            names = [c for c in chunk if c != "_"]
                            colon = chunk.index(":") if ":" in chunk else -1
                            typed = chunk[colon + 1:]
                            typed = typed[:typed.index("=")] if "=" in typed else typed
                            values.append((names[0] if colon > 0 else None, typed))
                        chunk = []
                    else:
                        chunk.append(t.v)
                out.append((".".join(s.name for s in scopes[i][1:] if s.name), case, line, values))
                j = end + 1
            while j < n and not toks[j].first and toks[j].v not in (",", ";", "}"):
                j += 1  # a raw value
            if j < n and toks[j].v == "," and not toks[j].first:
                j += 1
                continue
            break
    return out


def swift_fields(texts):
    """The names of the stored properties a target's structs declare, for the rule that a body returning one is a
    getter."""
    names = set()
    for text in texts:
        toks = tokens(strip(text, "swift"))
        stored, _ = _swift_struct_state(toks, _swift_scopes(toks))
        names.update(p.name for props in stored.values() for p in props)
    return names


def _swift_told(result, owner):
    """Whether a member's declared result is one the accessor rule lets through: nothing, a Bool, its own type or an
    Outcome of it."""
    plain = "".join(result or [])
    return plain in ("", "Void", "()", "Bool", owner, "Self") or (
        (result or [])[:1] == ["Outcome"] and _first_type_argument(result) in (owner, "Self") and "?" not in plain)


def _swift_func_name(toks, i):
    """For the func or subscript at i: (its name, the index after it). An operator's name is its symbols (`~`, `<~>`),
    up to its parameter list."""
    n = len(toks)
    if toks[i].v == "subscript":
        return "subscript", i + 1
    if i + 1 < n and _ident(toks[i + 1].v):
        return toks[i + 1].v, i + 2
    j = i + 1
    while j < n and toks[j].v not in ("(", "{") and not _ident(toks[j].v) and not toks[j].first:
        j += 1
    return "".join(t.v for t in toks[i + 1:j]), j


def _swift_function(toks, i):
    """For the func or subscript at i: (parameter names, result tokens, body token values or None)."""
    n = len(toks)
    _, j = _swift_func_name(toks, i)
    if j < n and toks[j].v == "<":
        j = _swift_close(toks, j, "<", ">") + 1
    inputs = set()
    if j < n and toks[j].v == "(":
        end = _swift_close(toks, j, "(", ")")
        chunk, depth = [], 0
        for t in toks[j + 1:end] + [Token(",", 0, 0, 0, False)]:
            depth += t.v in ("(", "[", "<")
            depth -= t.v in (")", "]", ">")
            if t.v == "," and depth == 0:
                if ":" in chunk:
                    inputs.add(chunk[chunk.index(":") - 1])
                chunk = []
            else:
                chunk.append(t.v)
        j = end + 1
    result = []
    while j < n and toks[j].v not in ("->", "{", "where") and not (toks[j].first and toks[j].v in _SWIFT_HEAD_KEYWORDS):
        j = _swift_close(toks, j, "(", ")") + 1 if toks[j].v == "(" else j + 1
    if j < n and toks[j].v == "->":
        j += 1
        begin, depth = j, 0
        while j < n and not (depth == 0 and toks[j].v in ("{", "where")):
            depth += toks[j].v in ("<", "[", "(")
            depth -= toks[j].v in (">", "]", ")")
            j += 1
        result = [t.v for t in toks[begin:j]]
    while j < n and toks[j].v != "{" and (toks[j].v == "where" or not toks[j].first):
        j += 1  # a where clause
    if j < n and toks[j].v == "{":
        end = _swift_close(toks, j, "{", "}")
        return inputs, result, _swift_getter([t.v for t in toks[j + 1:end]])
    return inputs, result, None


def _swift_getter(body):
    """An accessor block's getter (`get { x }` becomes `x`); None when it has a setter or observers."""
    if body[:1] in (["set"], ["didSet"], ["willSet"]) or (body[:1] == ["get"] and "set" in body):
        return None
    if body[:2] == ["get", "{"] and body[-1:] == ["}"]:
        body = body[2:-1]
    return body


def _swift_hands_out(toks, i, scope, fields):
    """For the public func (an operator included), subscript or var at i: (its label, what its body returns) when the
    body only returns an input's field, else None. An instance member is read only when it returns what swift-accessor
    lets through in the symbol graph (nothing, a Bool, its type or an Outcome of it), so an `id` of the target's own
    <Name>ID type is that rule's; @_spi members are swift-boundary's. Every other function, `id` and `wire`
    included, is read."""
    n = len(toks)
    mods = _swift_modifiers(toks, i)
    name = toks[i + 1].v if toks[i].v == "var" else _swift_func_name(toks, i)[0]
    member = scope.kind != "file" and not mods & {"static", "class"}
    if toks[i].v == "var":
        annotated, j, follows = _swift_binding(toks, i)
        body = None
        if follows == "{" and not (j + 1 < n and toks[j + 1].v in ("didSet", "willSet")):
            body = _swift_getter([t.v for t in toks[j + 1:_swift_close(toks, j, "{", "}")]])
        inputs, result = set(), annotated or []
    else:
        inputs, result, body = _swift_function(toks, i)
    if "@_spi" in mods or body is None or (member and not _swift_told(result, scope.name)):
        return None
    read = _field_read(("block", body), inputs, "self" if member else None, fields, implicit_return=True)
    if read and (result != ["Bool"] or read[1] in fields):
        return (f"{scope.name}.{name}" if scope.name else name), read[0]
    return None


def swift_source_findings(path, text, target, fields=None):
    """Findings for one Swift source file of a Domain target or of the kit. fields: the names of the stored properties
    the target's structs declare (swift_fields; by default this file's)."""
    stripped = strip(text, "swift")
    found = []
    add = lambda line, rule, what: found.append(Finding(path, line, rule, what))  # noqa: E731
    kit = target.role == "kit"
    for m in _SWIFT_IMPORT.finditer(stripped):
        attributes, module = m.group(1), m.group(2)
        line = _line_of(stripped, m.start(2))
        if "@_spi" in attributes:
            add(line, "swift-spi-import", f"{target.name} imports {module} with {attributes.strip()}")
        elif "@testable" not in attributes and module == KIT and not kit:
            continue
        elif "@testable" not in attributes and not kit and DOMAIN_TARGET.match(module) and module != target.name:
            add(line, "swift-context", f"{target.name} imports {module}, another context's core")
        else:
            add(line, "swift-import", f"{target.name} imports {module}")
    for pattern, why in _SWIFT_BANNED:
        for m in pattern.finditer(stripped):
            used = m.group(1) if m.groups() else m.group()
            add(_line_of(stripped, m.start()), "swift-banned-call", f"uses {used} ({why})")
    for m in re.finditer(r"\b(throws|rethrows|throw|Error)\b", stripped):
        add(_line_of(stripped, m.start()), "swift-throws", f"uses {m.group(1)}")
    for m in _SWIFT_CONFORMANCES.finditer(stripped):
        add(_line_of(stripped, m.start()), "swift-conformance", f"names {m.group(1)}")
    for m in re.finditer(r"@\s*discardableResult\b", stripped):
        add(_line_of(stripped, m.start()), "swift-discardable", "marks a result @discardableResult")
    toks = tokens(stripped)
    scopes = _swift_scopes(toks)
    n = len(toks)
    stored, _ = _swift_struct_state(toks, scopes)
    if fields is None:
        fields = {p.name for props in stored.values() for p in props}
    # From the inside out: beside other fields, no field is a primitive (the one wrapped field of a single-field value
    # object is the exception), whatever its access.
    for key, props in stored.items():
        if len(props) < 2:
            continue
        for prop in props:
            label = ".".join([name for _, name in key if name] + [prop.name])
            if prop.type is None:
                add(prop.line, "swift-primitive-field", f"{label} has no declared type, one of {len(props)} fields")
            elif set(prop.type) & SWIFT_PRIMITIVES:
                add(prop.line, "swift-primitive-field", f"{label} is {''.join(prop.type)}, one of {len(props)} fields")
    for owner, case, line, values in _swift_case_values(toks, scopes):
        if len(values) < 2:
            continue
        for label, typed in values:
            if set(typed) & SWIFT_PRIMITIVES:
                add(line, "swift-primitive-field", f"{owner}.{case}'s value {label or '_'} is {''.join(typed)}, one "
                                                   f"of {len(values)} values")
    for i, tok in enumerate(toks):
        stack = scopes[i]
        scope = stack[-1]
        in_code = any(s.kind == "code" for s in stack)
        value, after = tok.v, (toks[i + 1].v if i + 1 < n else "")
        prev = toks[i - 1].v if i else ""
        # State: reference types, escape hatches and raw memory, wherever they are.
        if value == "class" and _ident(after) and after not in _SWIFT_NOT_A_CLASS:
            add(tok.line, "swift-state", f"declares class {after}")
        elif value == "actor" and prev != "." and _ident(after) and i + 2 < n and toks[i + 2].v in ("{", ":", "<",
                                                                                                    "where"):
            add(tok.line, "swift-state", f"declares actor {after}")
        elif value == "nonisolated" and after == "(" and i + 2 < n and toks[i + 2].v == "unsafe":
            add(tok.line, "swift-state", "uses nonisolated(unsafe)")
        elif value == "unchecked" and prev == "@":
            add(tok.line, "swift-state", "uses @unchecked")
        elif _SWIFT_UNSAFE.match(value):
            add(tok.line, "swift-state", f"uses {value}")
        # A type alias, whatever its access and wherever it is: a private one hides a primitive from the field rules.
        if value == "typealias" and prev != "import" and _ident(after):
            j = i + 2
            if j < n and toks[j].v == "<":
                j = _swift_close(toks, j, "<", ">") + 1
            aliased = []
            if j < n and toks[j].v == "=":
                j += 1
                while j < n and not toks[j].first and toks[j].v not in (";", "{", "}"):
                    aliased.append(toks[j].v)
                    j += 1
            label = f"{scope.name}.{after}" if scope.name else after
            add(tok.line, "swift-alias", f"{label} is a type alias of {''.join(aliased) or 'another type'}")
        if in_code:
            continue
        # Tell, don't ask: whatever it returns and wherever it is declared, a body that only returns an input's field
        # is a getter; an instance member that returns another type is swift-accessor's in the symbol graph already.
        if value in ("func", "subscript", "var") and scope.kind != "protocol" and scope.exposed \
                and _swift_public(_swift_modifiers(toks, i), scope) \
                and i + 1 < n and (value != "var" or _ident(toks[i + 1].v)):
            handed = _swift_hands_out(toks, i, scope, fields)
            if handed:
                add(tok.line, "swift-accessor", f"{handed[0]} hands state out, returning {handed[1]}")
            mods = _swift_modifiers(toks, i)
            if value != "subscript" and toks[i + 1].v == "wire" and (scope.kind == "file"
                                                                     or mods & {"static", "class"}):
                label = f"{scope.name}.wire" if scope.name else "wire"
                add(tok.line, "swift-boundary", f"{label} is a static or free wire, not an instance's `var wire`")
        if value == "@" and after == "_spi" and i + 4 < n and toks[i + 2].v == "(":
            group, keyword, name = _swift_spi_target(toks, i)
            label = f"{scope.name}.{name}" if scope.name else name
            wire = keyword == "var" and name == "wire"
            allowed = (group == "Boundary" and (wire or (kit and keyword == "init"))) or (
                kit and group == "Clock" and keyword == "init")
            if keyword != "import" and not allowed:
                add(tok.line, "swift-boundary", f"{label} is marked @_spi({group})")
        elif value == "mutating":
            j = i + 1
            while j < n and toks[j].v in _SWIFT_MODIFIERS:
                j += 1
            if j < n and toks[j].v == "func" and scope.kind != "protocol":
                name = toks[j + 1].v if j + 1 < n else ""
                add(tok.line, "swift-mutating", f"declares mutating func {name}")
        elif value == "protocol" and _ident(after):
            label = f"{scope.name}.{after}" if scope.name else after
            public = scope.exposed and _swift_public(_swift_modifiers(toks, i), scope)
            if public and not after.endswith("Visitor") and not _kit_exception(
                    "swift-extensible", target.name, tuple(label.split("."))):
                add(tok.line, "swift-extensible", f"{label} is a public protocol")
        elif scope.kind == "protocol" and (scope.name or "").endswith("Visitor") and value in ("func", "var", "let"):
            if value != "func":
                add(tok.line, "swift-visitor-name", f"{scope.name}.{after} is a property, not a visit…")
            elif not after.startswith("visit"):
                add(tok.line, "swift-visitor-name", f"{scope.name}.{after} is not named visit…")
        elif value == "enum" and scope.exposed and _ident(after):
            label = f"{scope.name}.{after}" if scope.name else after
            if _swift_public(_swift_modifiers(toks, i), scope) and not _kit_exception(
                    "swift-public-enum", target.name, tuple(label.split("."))):
                add(tok.line, "swift-public-enum", f"{label} is a public enum")
        elif value in ("let", "var") and scope.kind != "protocol" and i + 1 < n:
            mods = _swift_modifiers(toks, i)
            name = after
            label = f"{scope.name}.{name}" if scope.name else name
            annotated, j, follows = _swift_binding(toks, i)
            observed = follows == "{" and j + 1 < n and toks[j + 1].v in ("didSet", "willSet")
            stored = value == "let" or follows != "{" or observed
            if scope.kind == "file":
                add(tok.line, "swift-state", f"{label} is a global")
            elif value == "var" and stored:
                add(tok.line, "swift-state", f"{label} is a stored var")
            if not (scope.exposed and _swift_public(mods, scope)):
                continue
            if any(mod.endswith("(set)") for mod in mods):
                add(tok.line, "swift-stored-public", f"{label} has a setter")
                continue
            if value == "let":
                if annotated:
                    own = scope.name is not None and _type_root(annotated) in (scope.name, "Self")
                else:
                    own = (scope.name is not None and follows == "=" and j + 1 < n
                           and toks[j + 1].v in (scope.name, "Self"))
                if not ({"static", "class"} & mods and own):
                    add(tok.line, "swift-stored-public", f"{label} is a stored public property")
            elif observed:
                add(tok.line, "swift-stored-public", f"{label} is a stored public property")
            elif follows == "{":
                end, depth = j, 0
                while end < n:
                    depth += toks[end].v == "{"
                    depth -= toks[end].v == "}"
                    if depth == 0:
                        break
                    if depth == 1 and toks[end].v == "set" and toks[end - 1].v in ("{", "}"):
                        add(tok.line, "swift-stored-public", f"{label} has a public setter")
                        break
                    end += 1
            else:
                add(tok.line, "swift-stored-public", f"{label} is a stored public property")
    return sorted(set(found))


def _kit_exception(rule, target, path):
    return (rule, target, path if isinstance(path, str) else ".".join(path)) in KIT_EXCEPTIONS


def swift_closed_sets(texts):
    """The structs that are closed sets: the only stored property of each is a value of a private enum it declares."""
    closed = set()
    for text in texts:
        toks = tokens(strip(text, "swift"))
        stored, enums = _swift_struct_state(toks, _swift_scopes(toks))
        for key, props in stored.items():
            if len(props) == 1 and props[0].type and _type_root(props[0].type) in enums.get(key, ()):
                closed.add(key[-1][1])
    return closed


def swift_public_functions(texts):
    """The names of the functions the Domain targets declare public, for the app's dropped results."""
    names = set()
    for text in texts:
        stripped = strip(text, "swift")
        names.update(re.findall(r"\b(?:public|open)\s+(?:[\w()]+\s+)*?func\s+([A-Za-z_]\w*)", stripped))
    return names


def swift_declared(texts):
    """The types the Domain targets and the kit declare, for telling their functions from another type's."""
    names = set()
    for text in texts:
        names.update(re.findall(r"\b(?:struct|enum|class|actor|protocol)\s+([A-Z]\w*)", strip(text, "swift")))
    return names


def swift_visitors(texts):
    names = set()
    for text in texts:
        names.update(re.findall(r"\bprotocol\s+(\w+Visitor)\b", strip(text, "swift")))
    return names


_SWIFT_CONFORMANCE_HEAD = re.compile(
    r"\b(?:struct|class|enum|actor|extension)\s+[\w.]+(?:\s*<[^>{]*>)?\s*:\s*([^{]*)\{")


# A file that uses a Domain target or the kit: it imports one.
_SWIFT_USES_DOMAIN = re.compile(r"\bimport\s+(?:(?:typealias|struct|class|enum|protocol|let|var|func)\s+)?"
                                r"(?:PersonaDomainKit\w*|\w*Domain)\b")


def swift_outside_findings(path, text, role, visitors, functions, app, declared=frozenset()):
    """Findings for a Swift file outside the Domain targets and the kit. role: the target role of the file ("adapt",
    "kit-adapt", "test-domain" or "other"); app: whether the file is app code (apple/Persona, apple/Shared);
    functions and declared: the public functions and the types the Domain targets and the kit declare. Only a file
    that imports a Domain target or the kit is read for dropped results."""
    stripped = strip(text, "swift")
    found = []
    for m in _SWIFT_IMPORT.finditer(stripped):
        attributes, module = m.group(1), m.group(2)
        if role == "adapt" and ADAPT_TARGET.match(module) and module != KIT_ADAPT:
            found.append(Finding(path, _line_of(stripped, m.start(2)), "swift-context",
                                 f"imports {module}, another context's adapter"))
        if not (module == KIT or DOMAIN_TARGET.match(module)):
            continue
        spis = set(re.findall(r"@_spi\(\s*(\w+)\s*\)", attributes))
        allowed = {"Boundary"} if role == "adapt" else {"Boundary", "Clock"} if role == "kit-adapt" else set()
        for spi in sorted(spis - allowed):
            found.append(Finding(path, _line_of(stripped, m.start(2)), "swift-spi-import",
                                 f"imports {module} with @_spi({spi})"))
    if visitors and role not in ("adapt", "kit-adapt", "test-domain"):
        for m in _SWIFT_CONFORMANCE_HEAD.finditer(stripped):
            hit = sorted(set(re.findall(r"\w+", m.group(1).split("where")[0])) & visitors)
            if hit:
                found.append(Finding(path, _line_of(stripped, m.start()), "swift-visitor-impl",
                                     f"conforms to {', '.join(hit)}"))
    if (app or role == "adapt") and functions and _SWIFT_USES_DOMAIN.search(stripped):
        for m in re.finditer(r"(?<![\w.])_\s*=(?!=)\s*", stripped):
            called = _dropped_calls(_expression_at(stripped, m.end()), functions, declared)
            if called:
                found.append(Finding(path, _line_of(stripped, m.start()), "swift-app-discard",
                                     f"drops the result of {called[0]}"))
    return sorted(set(found))


_UNUSED = re.compile(r"(?:(?P<file>/[^\s:]+\.swift):(?P<line>\d+):(?:\d+:)?\s*)?(?:warning: )?result of (?:call to "
                     r"'(?P<call>[^'(]+)(?:\([^']*\))?'|'(?P<init>[^']+)' initializer) is unused")
_APPLE_PATH = re.compile(r"^.*/(apple/[A-Z].*)$")  # the last apple/ in the path: the repository's


def swift_build_log_paths(text, functions):
    """The repository paths under apple/ where a build log's unused-result warnings name a domain function, for
    swift_build_log_findings' sources."""
    paths = set()
    for m in _UNUSED.finditer(text):
        inside = _APPLE_PATH.search(m.group("file") or "")
        if inside and (m.group("call") or m.group("init")) in functions:
            paths.add(inside.group(1))
    return sorted(paths)


def swift_build_log_findings(name, text, functions, declared=frozenset(), sources=None):
    """Unused-result warnings in an app build log (plain or an Xcode activity log's text) naming a domain function.
    sources: {repository path: text} of the files under apple/ the log names (swift_build_log_paths), read from the
    tree. A warning in a file counts only when the file is one of them, imports a Domain target or the kit, and on that
    line calls the function on something other than a type the domain modules do not declare (`Draft.parse(raw)`): a
    dependency's or a generated file cannot call a domain function. A warning the log places in no file counts by its
    name alone."""
    sources = sources or {}
    found = []
    for m in _UNUSED.finditer(text):
        called = m.group("call") or m.group("init")
        if called not in functions:
            continue
        path, line = name, 0
        if m.group("file"):
            inside = _APPLE_PATH.search(m.group("file"))
            source = sources.get(inside.group(1)) if inside else None
            if source is None:
                continue
            stripped = strip(source, "swift")
            if not _SWIFT_USES_DOMAIN.search(stripped):
                continue
            path, line = inside.group(1), int(m.group("line"))
            lines = stripped.split("\n")
            if 0 < line <= len(lines) and _called_on_other_types(lines[line - 1], called, declared):
                continue
        found.append(Finding(path, line, "swift-app-discard", f"drops the result of {called} (build warning)"))
    return sorted(set(found))


def read_build_log(path):
    """A build log as text: a plain log, or an Xcode .xcactivitylog (gzip)."""
    data = Path(path).read_bytes()
    if data[:2] == b"\x1f\x8b":
        data = gzip.decompress(data)
    return data.decode("utf-8", errors="replace")


# ---------------------------------------------------------------------------------------------------------------------
# Swift symbol graphs


def _fragments(fragments):
    return "".join(f.get("spelling", "") for f in fragments or [])


def _types(fragments):
    return [f["spelling"] for f in fragments or [] if f.get("kind") == "typeIdentifier"]


def _graph_location(symbol, root_marker="/apple/Packages/"):
    location = symbol.get("location") or {}
    uri = location.get("uri", "")
    path = uri.split("file://", 1)[-1]
    if root_marker in path:
        path = "apple/Packages/" + path.rsplit(root_marker, 1)[1]
    line = (location.get("position") or {}).get("line")
    return path, (line + 1 if isinstance(line, int) else 0)


_GRAPH_TYPES = frozenset(("swift.struct", "swift.enum", "swift.class", "swift.protocol", "swift.actor",
                          "swift.typealias"))


def swift_graph_findings(target, graphs, closed=frozenset()):
    """The public API rules over a Domain target's (or the kit's) symbol graphs: the parsed JSON of
    <Target>.symbols.json and every <Target>@<Other>.symbols.json. closed: the target's closed sets
    (swift_closed_sets), which alone name a factory for one of their cases."""
    symbols, parents = {}, {}
    for graph in graphs:
        for symbol in graph.get("symbols", []):
            symbols[symbol["identifier"]["precise"]] = symbol
        for relation in graph.get("relationships", []):
            if relation.get("kind") == "memberOf":
                parents[relation["source"]] = relation["target"]
    declared = {(s.get("pathComponents") or [""])[-1] for s in symbols.values()
                if s["kind"]["identifier"] in _GRAPH_TYPES}
    found = []
    for precise, symbol in symbols.items():
        # Members the compiler adds (a protocol's default implementations, Codable's init(from:)) have no place in the
        # source; the conformance that brought them is a finding of its own.
        if "::SYNTHESIZED::" in precise or not symbol.get("location"):
            continue
        if symbol.get("accessLevel") not in ("public", "open"):
            continue
        kind = symbol["kind"]["identifier"]
        components = symbol.get("pathComponents") or []
        path, line = _graph_location(symbol)
        name = components[-1] if components else ""
        base = name.split("(", 1)[0]
        owner = components[-2] if len(components) > 1 else None
        label = ".".join(components)
        add = lambda rule, what: found.append(Finding(path, line, rule, what))  # noqa: E731
        decl = _fragments(symbol.get("declarationFragments"))
        signature = symbol.get("functionSignature") or {}
        params = signature.get("parameters") or []
        returns = signature.get("returns") or []
        own_generics = _own_generics(symbol, symbols.get(parents.get(precise)))
        if kind == "swift.enum" and not _kit_exception("swift-public-enum", target, components):
            add("swift-public-enum", f"{label} is a public enum")
        generic = own_generics or kind == "swift.associatedtype"
        if generic and not _kit_exception("swift-generic", target, components[:1]):
            add("swift-generic", f"{label} is generic")
        if kind in ("swift.property", "swift.type.property", "swift.typealias", "swift.var"):
            stored_let = re.match(r"^(?:static |class )?let ", decl)
            stored_var = re.match(r"^(?:static |class )?var ", decl) and "{" not in decl
            setter = "{ get set }" in decl
            declared_type = _types(symbol.get("declarationFragments"))
            if kind != "swift.typealias" and (stored_let or stored_var or setter):
                own = declared_type[:1] in ([owner], ["Self"]) and len(declared_type) == 1
                if not (stored_let and decl.startswith(("static ", "class ")) and own):
                    what = "has a public setter" if setter else "is a stored public property"
                    add("swift-stored-public", f"{label} {what}")
            if "->" in decl:
                add("swift-callback", f"{label} has a function type")
            computed_bool = "{ get }" in decl and declared_type == ["Bool"]
            if set(declared_type) & SWIFT_PRIMITIVES and not computed_bool:
                add("swift-primitive-api", f"{label} has the type {' '.join(declared_type)}")
            optional = "?" in decl.split(":", 1)[-1].split("{", 1)[0]
            identity = base == "id" and kind == "swift.property" and not optional and _swift_identity(
                declared_type, declared)
            if kind != "swift.typealias" and owner and not identity and (optional or not (
                    declared_type == ["Bool"] or declared_type[:1] in ([owner], ["Self"]) and len(declared_type) == 1)):
                add("swift-accessor", f"{label} hands out {' '.join(declared_type) or 'state'}")
            continue
        if kind not in ("swift.method", "swift.type.method", "swift.func", "swift.init", "swift.subscript",
                        "swift.type.subscript", "swift.func.op"):
            continue
        param_types = [_types(p.get("declarationFragments")) for p in params]
        result = _types(returns)
        if base in ("evolve", "apply") and kind != "swift.init":
            add("swift-public-evolve", f"{label} is public")
        if len(params) > MAX_PARAMS:
            add("swift-factory", f"{label} takes {len(params)} parameters")
        if kind == "swift.init":
            add("swift-factory", f"{label} is a public initializer")
        if any("->" in _fragments(p.get("declarationFragments")) for p in params):
            add("swift-callback", f"{label} takes a function")
        if "->" in _fragments(returns):
            add("swift-callback", f"{label} returns a function")
        plain = _fragments(returns).strip()
        builds_own = plain in (owner, "Self") or (
            result[:1] == ["Outcome"] and result[1:2] in ([owner], ["Self"]) and not plain.endswith("?"))
        # A factory builds its type, optional or not: the name rule reads `Item?` as building an Item.
        builds = builds_own or plain.rstrip("?!") in (owner, "Self") or (
            result[:1] == ["Outcome"] and result[1:2] in ([owner], ["Self"]))
        stage = len(result) == 1 and plain == result[0] and result[0] in declared and result[0].endswith(
            ("Step", "Builder"))
        if kind == "swift.type.method" and builds and base not in FACTORY_NAMES | {"evolve", "apply"} and (
                owner not in closed or BANNED_FACTORY.match(base)):
            add("swift-factory", f"{label} builds a {owner} under a name other than parse, new, restore, newBuilder or "
                                 "restoreBuilder")
        if kind == "swift.type.method" and builds and ("?" in plain or plain.endswith("!")):
            add("swift-factory", f"{label} returns {plain}, which may be nil")
        if kind == "swift.type.method" and base == "restore" and not (
                result[:1] == ["Outcome"] and result[1:2] in ([owner], ["Self"])):
            add("swift-factory", f"{label} returns {plain or 'Void'}, not an Outcome of {owner}")
        elif kind == "swift.type.method" and base in ("parse", "new") and not builds and not (
                base == "new" and (owner or "").endswith("Builder") and stage):
            add("swift-factory", f"{label} returns {plain or 'Void'}, not a {owner} or an Outcome of it")
        elif kind == "swift.type.method" and base in ("newBuilder", "restoreBuilder") and not stage:
            add("swift-factory", f"{label} returns {plain or 'Void'}, not the first …Step or …Builder of {owner}")
        parse = kind == "swift.type.method" and base == "parse"
        loose = [p.get("name", "_") for p, kinds in zip(params, param_types) if set(kinds) & SWIFT_PRIMITIVES]
        if loose and not parse:
            add("swift-primitive-api", f"{label} takes {', '.join(loose)} as a primitive")
        if set(result) & SWIFT_PRIMITIVES and result != ["Bool"]:
            add("swift-primitive-api", f"{label} returns {_fragments(returns)}")
        # Tell, don't ask: an instance member answers with nothing, a Bool, its own type or an Outcome of it, whatever
        # its parameters; a type method without parameters likewise.
        method = kind in ("swift.method", "swift.subscript") or (kind == "swift.type.method" and not params)
        if owner and method:
            told = plain in ("", "()", "Void") or result == ["Bool"] or builds_own
            told = told or (base == "id" and not params and _swift_identity([plain], declared))
            told = told or (base == "build" and not params and owner.endswith("Builder")
                            and result[:1] == ["Outcome"] and result[1:2] and result[1] in declared)
            told = told or (owner.endswith("Step") and len(result) == 1 and result[0] in declared
                            and result[0].endswith(("Step", "Builder")) and plain == result[0])
            if not told:
                add("swift-accessor", f"{label} hands out {plain}")
    return sorted(set(found))


def _swift_identity(type_names, declared):
    """Whether an id's type is the target's own identity type: one name, declared in the target, ending in ID."""
    return (len(type_names) == 1 and type_names[0] in declared and type_names[0].endswith("ID")
            and len(type_names[0]) > 2)


def _own_generics(symbol, parent):
    """The generic parameters a symbol declares itself, not the ones of the type it is a member of."""
    def parameters(s):
        return {(p.get("name"), p.get("depth")) for p in ((s or {}).get("swiftGenerics") or {}).get("parameters") or []}
    return parameters(symbol) - parameters(parent)


# ---------------------------------------------------------------------------------------------------------------------
# The tree: finding the modules and running every rule

Scan = namedtuple("Scan", "modules findings inspected")


def _walk(top, suffixes):
    """Repository files under top with one of suffixes, build output and hidden folders left out."""
    for directory, folders, files in os.walk(top):
        folders[:] = sorted(f for f in folders if not f.startswith(".") and f not in _BUILD_DIRS
                            and not f.endswith(_BUILD_SUFFIXES))
        for name in sorted(files):
            if name.endswith(suffixes):
                yield Path(directory) / name


def _rel(root, path):
    return Path(path).relative_to(root).as_posix()


def _read(path):
    return Path(path).read_text(encoding="utf-8", errors="replace")


def swift_targets(root):
    """Every target of every apple/Packages/*/Package.swift, and the problems finding the domain ones."""
    root = Path(root)
    targets, problems = [], []
    packages = root / "apple" / "Packages"
    manifests = sorted(packages.glob("*/Package.swift")) if packages.is_dir() else []
    texts = {}
    for manifest in manifests:
        package = _rel(root, manifest.parent)
        texts[package] = _read(manifest)
        targets.extend(parse_manifest(package, _rel(root, manifest), texts[package]))
    declared = {t.name for t in targets}
    for package, text in texts.items():
        source = strip(text, "swift", keep_strings=True)
        for m in re.finditer(r"\"(\w*(?:Domain|Adapt)|PersonaDomainKit\w*)\"", source):
            if m.group(1) not in declared:
                problems.append(Finding(package + "/Package.swift", _line_of(source, m.start()), "swift-discovery",
                                        f"names {m.group(1)}, which no .target(name:) declares"))
    by_sources = {t.sources: t for t in targets}
    if (root / "apple").is_dir():
        for directory, folders, _ in os.walk(root / "apple"):
            folders[:] = [f for f in folders if not f.startswith(".") and f not in _BUILD_DIRS
                          and not f.endswith(_BUILD_SUFFIXES)]
            for folder in folders:
                if (Path(directory) / folder / "Package.swift").is_file():
                    continue  # a package's own folder (apple/Packages/PersonaDomainKit), not a target's sources
                if swift_role(folder) in ("domain", "adapt", "kit", "kit-adapt"):
                    rel = _rel(root, Path(directory) / folder)
                    if rel not in by_sources:
                        problems.append(Finding(rel, 0, "swift-discovery",
                                                f"is named like a domain target, but no package declares {folder} "
                                                "with these sources"))
    return targets, problems


def scan_apple(root, apple_sh=None, graphs_dir=None, build_logs=()):
    """Every Swift rule over the tree at root. apple_sh: scripts/ci/apple.sh's text (None leaves the step check out);
    graphs_dir: where apple.sh wrote the symbol graphs (None leaves the public API rules out); build_logs: app build
    logs to read for unused domain results."""
    root = Path(root)
    targets, findings = swift_targets(root)
    modules = [t for t in targets if t.role in ("domain", "kit", "adapt", "kit-adapt")]
    cores = [t for t in modules if t.role in ("domain", "kit")]
    inspected = 0
    core_texts = []
    target_texts = {}
    for target in targets:
        findings.extend(swift_name_findings(target))
        if target.role == "adapt":
            findings.extend(swift_adapt_manifest_findings(target))
    for target in cores:
        findings.extend(swift_manifest_findings(target))
        files = list(_walk(root / target.sources, (".swift",))) if (root / target.sources).is_dir() else []
        if not files:
            findings.append(Finding(target.sources, 0, "swift-discovery", f"{target.name} has no Swift sources here"))
        texts = {_rel(root, file): _read(file) for file in files}
        fields = swift_fields(texts.values())
        for rel, text in texts.items():
            core_texts.append(text)
            target_texts.setdefault(target.name, []).append(text)
            inspected += 1
            findings.extend(swift_source_findings(rel, text, target, fields))
    visitors = swift_visitors(core_texts)
    functions = swift_public_functions(core_texts)
    types = swift_declared(core_texts)
    core_dirs = {t.sources for t in cores}
    roles = {t.sources: t.role for t in targets}
    declared = {t.name for t in targets}
    if (root / "apple").is_dir():
        for file in _walk(root / "apple", (".swift",)):
            rel = _rel(root, file)
            owner = next((sources for sources in roles if rel.startswith(sources + "/")), None)
            if owner in core_dirs:
                continue
            data = file.read_bytes()
            for m in _SWIFT_DOMAIN_IMPORT.finditer(data):
                module = m.group(1).decode()
                if module not in declared:
                    findings.append(Finding(rel, data.count(b"\n", 0, m.start(1)) + 1, "swift-discovery",
                                            f"imports {module}, which no package declares"))
            app = rel.startswith(("apple/Persona/", "apple/Shared/"))
            role = roles.get(owner, "other")
            if b"_spi" not in data and not any(v.encode() in data for v in visitors) \
                    and not (app and b"_ =" in data) and role != "adapt":
                continue
            findings.extend(swift_outside_findings(rel, data.decode("utf-8", errors="replace"), role, visitors,
                                                   functions, app, types))
    for package in sorted({t.package for t in cores}):
        if not any(t.package == package and t.role == "test-domain" for t in targets):
            findings.append(Finding(package + "/Package.swift", 0, "swift-testable",
                                    "declares no Persona<Ctx>DomainTests target, so `swift test` has no test to run"))
    packages = sorted({t.package for t in modules})
    if apple_sh is not None:
        findings.extend(swift_testable_findings(apple_sh, packages))
    if graphs_dir is not None:
        for target in cores:
            main = Path(graphs_dir) / f"{target.name}.symbols.json"
            if not main.is_file():
                findings.append(Finding(target.sources, 0, "swift-testable",
                                        f"has no symbol graph {main.name} in {graphs_dir}"))
                continue
            graphs = [json.loads(main.read_text(encoding="utf-8"))]
            graphs += [json.loads(p.read_text(encoding="utf-8"))
                       for p in sorted(Path(graphs_dir).glob(f"{target.name}@*.symbols.json"))]
            inspected += 1
            findings.extend(swift_graph_findings(target.name, graphs,
                                                 swift_closed_sets(target_texts.get(target.name, []))))
    for log in build_logs:
        text = read_build_log(log)
        sources = {rel: _read(root / rel) for rel in swift_build_log_paths(text, functions) if (root / rel).is_file()}
        findings.extend(swift_build_log_findings(str(log), text, functions, types, sources))
    return Scan(modules, sorted(set(findings)), inspected)


_KT_DOMAIN_PACKAGE = re.compile(rb"(?m)^\s*package\s+persona\.core\.([a-z0-9_]+)\.(domain|adapt)\b")
_KT_DOMAIN_IMPORT = re.compile(rb"(?m)^\s*import\s+persona\.core\.(?:([a-z0-9_]+)\.domain|(domainkit))\b")
_SWIFT_DOMAIN_IMPORT = re.compile(rb"(?m)^[ \t]*(?:@\w+(?:\([^)\n]*\))?[ \t]+)*(?:(?:public|package|internal|"
                                  rb"fileprivate|private)[ \t]+)?import[ \t]+(?:(?:typealias|struct|class|enum|"
                                  rb"protocol|let|var|func)[ \t]+)?(PersonaDomainKit\w*|\w*Domain|\w*Adapt)\b")
_KT_KIT_PACKAGE = re.compile(rb"(?m)^\s*package\s+persona\.core\.domainkit\b")


KotlinModule = namedtuple("KotlinModule", "ctx kind path")  # kind: "domain", "kit" or "adapt"


def android_modules(root):
    """The Kotlin domain modules, the kit and the adapt packages, and the problems finding them."""
    root = Path(root)
    settings = root / "android" / "settings.gradle.kts"
    included = set()
    problems = []
    if settings.is_file():
        source = strip(_read(settings), "kotlin", keep_strings=True)
        included = set(re.findall(r"\"(:[\w:-]+)\"", _block_calls(source, "include")))
        for m in re.finditer(r"project\(\s*\"(:core:[\w-]+:domain|:core:domainkit)\"\s*\)\s*\.\s*projectDir", source):
            problems.append(Finding("android/settings.gradle.kts", _line_of(source, m.start()), "kotlin-discovery",
                                    f"moves {m.group(1)} away from its conventional folder"))
    modules = []
    for project in sorted(included):
        m = re.fullmatch(r":core:([a-z0-9_]+):domain", project)
        if m:
            modules.append(KotlinModule(m.group(1), "domain", f"android/core/{m.group(1)}/domain"))
        elif project == ":core:domainkit":
            modules.append(KotlinModule("domainkit", "kit", "android/core/domainkit"))
    core = root / "android" / "core"
    if core.is_dir():
        for ctx_dir in sorted(p for p in core.iterdir() if p.is_dir()):
            ctx = ctx_dir.name
            adapt = ctx_dir / "src" / "main" / "kotlin" / "persona" / "core" / ctx / "adapt"
            if adapt.is_dir() and f":core:{ctx}" in included:
                modules.append(KotlinModule(ctx, "adapt", _rel(root, adapt)))
            domain = ctx_dir / "domain"
            if domain.is_dir() and f":core:{ctx}:domain" not in included:
                problems.append(Finding(_rel(root, domain), 0, "kotlin-discovery",
                                        f"is named like a domain module, but settings.gradle.kts does not include "
                                        f":core:{ctx}:domain"))
        if (core / "domainkit").is_dir() and ":core:domainkit" not in included:
            problems.append(Finding("android/core/domainkit", 0, "kotlin-discovery",
                                    "is the kit's folder, but settings.gradle.kts does not include :core:domainkit"))
    return modules, problems


def _block_calls(source, name):
    out = []
    for m in re.finditer(r"\b" + name + r"\s*\(", source):
        out.append(_balanced(source, m.end() - 1))
    return "\n".join(out)


def _android_place(rel, modules):
    """Where an Android file is: ("domain"|"kit"|"adapt"|"kit-adapt"|"test"|"other", the module or None)."""
    for module in modules:
        if not rel.startswith(module.path + "/"):
            continue
        rest = rel[len(module.path) + 1:]
        if module.kind == "adapt":
            return "kit-adapt" if module.ctx == "domainkit" else "adapt", module
        if rest.startswith("src/main/"):
            if module.kind == "kit" and "/persona/core/domainkit/adapt/" in "/" + rest:
                return "kit-adapt", module
            return module.kind, module
        if rest.startswith("src/"):
            return "test", module
        return "build", module
    if re.search(r"/src/(test|androidTest|testFixtures)\w*/", rel):
        m = re.search(r"/persona/core/([a-z0-9_]+)/adapt/", rel)
        return ("adapt" if m else "test"), None
    return "other", None


def scan_android(root, build_logic=True):
    """Every Kotlin rule over the tree at root."""
    root = Path(root)
    modules, findings = android_modules(root)
    contexts = {m.ctx for m in modules if m.kind == "domain"}
    kit_found = any(m.kind == "kit" for m in modules)
    inspected = 0
    core_texts = []
    seen = set()
    for module in modules:
        if module.kind in ("domain", "kit"):
            build = root / module.path / "build.gradle.kts"
            if not build.is_file():
                findings.append(Finding(module.path, 0, "kotlin-discovery", "has no build.gradle.kts"))
            else:
                findings.extend(kotlin_build_findings(_rel(root, build), _read(build), module.kind == "kit"))
            main = root / module.path / "src" / "main"
            files = list(_walk(main, (".kt",))) if main.is_dir() else []
            if not files:
                findings.append(Finding(module.path, 0, "kotlin-discovery", "has no Kotlin sources in src/main"))
            texts = {}
            for file in files:
                rel = _rel(root, file)
                place, _ = _android_place(rel, modules)
                if place != "kit-adapt":
                    texts[rel] = _read(file)
            declared = kotlin_declared(texts.values())
            fields = kotlin_fields(texts.values())
            ctx = module.ctx if module.kind == "domain" else None
            for rel, text in texts.items():
                core_texts.append(text)
                seen.add(rel)
                inspected += 1
                findings.extend(kotlin_source_findings(rel, text, module.kind, ctx, declared, fields))
    visitors = kotlin_visitors(core_texts)
    functions = kotlin_public_functions(core_texts)
    types = kotlin_declared(core_texts)
    android = root / "android"
    if android.is_dir():
        for file in _walk(android, (".kt", ".kts")):
            rel = _rel(root, file)
            data = file.read_bytes()
            place, module = _android_place(rel, modules)
            for m in _KT_DOMAIN_PACKAGE.finditer(data):
                ctx, kind = m.group(1).decode(), m.group(2).decode()
                expected = f"android/core/{ctx}/domain/" if kind == "domain" else \
                    f"android/core/{ctx}/src/main/kotlin/persona/core/{ctx}/adapt/"
                if ctx != "domain" and module is None and not re.search(r"/src/\w*[tT]est\w*/", rel):
                    findings.append(Finding(rel, data.count(b"\n", 0, m.end()) + 1, "kotlin-discovery",
                                            f"is in package persona.core.{ctx}.{kind}, which the guard reads only in "
                                            f"{expected} of an included module"))
            for m in _KT_DOMAIN_IMPORT.finditer(data):
                ctx = (m.group(1) or b"").decode()
                missing = (m.group(2) and not kit_found) or (ctx and ctx != "domain" and ctx not in contexts)
                if missing:
                    name = "persona.core.domainkit" if m.group(2) else f"persona.core.{ctx}.domain"
                    findings.append(Finding(rel, data.count(b"\n", 0, m.end()) + 1, "kotlin-discovery",
                                            f"imports {name}, which no included module holds"))
            if _KT_KIT_PACKAGE.search(data) and not rel.startswith("android/core/domainkit/"):
                findings.append(Finding(rel, 0, "kotlin-discovery", "is in package persona.core.domainkit outside "
                                                                    "android/core/domainkit"))
            if rel in seen:
                continue
            discards = functions and re.search(rb"\bval\s+_\b", data)
            if b"DomainBoundary" not in data and b"ClockRead" not in data and place != "adapt" and not discards \
                    and not any(v.encode() in data for v in visitors):
                continue
            adapt = re.search(r"/persona/core/([a-z0-9_]+)/adapt/", rel)
            findings.extend(kotlin_outside_findings(rel, data.decode("utf-8", errors="replace"), place, visitors,
                                                    functions, adapt.group(1) if adapt else None, types))
    if build_logic:
        logic = root / "android" / "build-logic" / "convention" / "src" / "main" / "kotlin"
        files = {}
        for name in ("persona/buildlogic/ReturnValues.kt", "persona.android.application.gradle.kts",
                     "persona.android.library.gradle.kts", "persona.jvm.library.gradle.kts"):
            path = logic / name
            if path.is_file():
                files[_rel(root, path)] = _read(path)
        findings.extend(kotlin_build_logic_findings(files))
    return Scan(modules, sorted(set(findings)), inspected)
