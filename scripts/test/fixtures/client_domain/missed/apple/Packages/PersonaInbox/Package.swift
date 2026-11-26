// swift-tools-version: 6.2
// A planted package whose targets are named like domain targets without the Persona prefix: the guard checks them and
// fails their names, and a folder no target declares fails too.
import PackageDescription

let coreSettings: [SwiftSetting] = [
    .swiftLanguageMode(.v6),
    .defaultIsolation(nil),
    .enableUpcomingFeature("InternalImportsByDefault"),
    .enableUpcomingFeature("MemberImportVisibility"),
    .enableUpcomingFeature("ExistentialAny"),
    .unsafeFlags([
        "-warnings-as-errors",
        "-Xfrontend", "-disable-implicit-concurrency-module-import",
        "-Xfrontend", "-disable-implicit-string-processing-module-import",
        "-Xfrontend", "-reflection-metadata-for-debugger-only",
    ]),
]

let package = Package(
    name: "PersonaInbox",
    platforms: [.macOS(.v15)],
    targets: [
        .target(name: "InboxDomain", swiftSettings: coreSettings),
        .testTarget(name: "InboxDomainTests", dependencies: ["InboxDomain"]),
    ]
)
