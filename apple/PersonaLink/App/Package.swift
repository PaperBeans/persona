// swift-tools-version: 6.0
// Persona Link: the menu bar App on a Mac (docs/operations/link.md "Tray and menu bar App"), the Windows tray's
// counterpart. It shows the independently installed persona-link's connection and pairs it; it never runs the connector
// itself. A package of its own, apart from Persona Screen's (../Package.swift) and Persona Input's (../Input): building
// it never changes their binaries, and with them a permission the user gave (#7927). scripts/app/mac-link.sh builds the
// .app around it.
import PackageDescription

let package = Package(
    name: "PersonaLink",
    defaultLocalization: "en",
    platforms: [.macOS(.v15)],
    targets: [
        // Resources: the String Catalog (en / zh-Hans / ja, `linkText`) and the helper's icons
        // (scripts/assets/make_icon.py link); mac-link.sh copies the resource bundle into the app.
        // NativeCompatibility.swift links apple/Shared/NativeStateMigration.swift for the previous name's state root.
        .executableTarget(name: "PersonaLink", path: "Sources/PersonaLink", resources: [.process("Resources")],
                          swiftSettings: [.define("NATIVE_MIGRATION_STORAGE_ONLY")]),
        .testTarget(name: "PersonaLinkTests", dependencies: ["PersonaLink"], path: "Tests/PersonaLinkTests"),
    ],
    swiftLanguageModes: [.v5]
)
