// swift-tools-version: 6.2
// A planted domain kit for test_client_domain_modules.py: the compliant shape, not the product's kit.
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
    name: "PersonaDomainKit",
    platforms: [.macOS(.v15)],
    products: [
        .library(name: "PersonaDomainKit", targets: ["PersonaDomainKit"]),
        .library(name: "PersonaDomainKitAdapt", targets: ["PersonaDomainKitAdapt"]),
    ],
    targets: [
        .target(name: "PersonaDomainKit", swiftSettings: coreSettings),
        .target(name: "PersonaDomainKitAdapt", dependencies: ["PersonaDomainKit"]),
        .testTarget(name: "PersonaDomainKitTests", dependencies: ["PersonaDomainKit", "PersonaDomainKitAdapt"]),
    ]
)
