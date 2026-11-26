// swift-tools-version: 6.2
// A planted package whose Domain target the manifest names only through a helper: discovery must not pass it as
// "no domain modules".
import PackageDescription

func domain(_ context: String) -> Target { .target(name: "Persona\(context)Domain") }

let package = Package(name: "PersonaHidden", platforms: [.macOS(.v15)], targets: [domain("Hidden")])
