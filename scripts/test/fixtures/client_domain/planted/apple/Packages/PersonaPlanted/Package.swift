// swift-tools-version: 6.2
// A planted domain package for test_client_domain_modules.py: every rule's compliant twin.
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
    name: "PersonaPlanted",
    platforms: [.macOS(.v15)],
    products: [
        .library(name: "PersonaPlantedDomain", targets: ["PersonaPlantedDomain"]),
        .library(name: "PersonaPlantedAdapt", targets: ["PersonaPlantedAdapt"]),
    ],
    dependencies: [.package(path: "../PersonaDomainKit")],
    targets: [
        .target(
            name: "PersonaPlantedDomain",
            dependencies: [.product(name: "PersonaDomainKit", package: "PersonaDomainKit")],
            swiftSettings: coreSettings
        ),
        .target(
            name: "PersonaPlantedAdapt",
            dependencies: ["PersonaPlantedDomain", .product(name: "PersonaDomainKit", package: "PersonaDomainKit")]
        ),
        .testTarget(
            name: "PersonaPlantedDomainTests",
            dependencies: [
                "PersonaPlantedDomain",
                .product(name: "PersonaDomainKit", package: "PersonaDomainKit"),
                .product(name: "PersonaDomainKitAdapt", package: "PersonaDomainKit"),
            ]
        ),
    ]
)
