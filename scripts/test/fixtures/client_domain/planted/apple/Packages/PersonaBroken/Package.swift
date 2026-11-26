// swift-tools-version: 6.2
// A planted package for test_client_domain_modules.py: every Swift rule broken once. expected.txt lists the findings.
import PackageDescription

let package = Package(
    name: "PersonaBroken",
    platforms: [.macOS(.v15)],
    dependencies: [.package(path: "../PersonaDomainKit"), .package(path: "../PersonaPlanted")],
    targets: [
        .target(name: "PersonaBrokenSupport"),
        .target(
            name: "PersonaBrokenDomain",
            dependencies: [
                "PersonaBrokenSupport",
                .product(name: "PersonaDomainKit", package: "PersonaDomainKit"),
                .product(name: "PersonaPlantedDomain", package: "PersonaPlanted"),
            ],
            swiftSettings: [.swiftLanguageMode(.v6)]
        ),
        .target(
            name: "PersonaBrokenAdapt",
            dependencies: ["PersonaBrokenDomain", .product(name: "PersonaPlantedAdapt", package: "PersonaPlanted")]
        ),
    ]
)
