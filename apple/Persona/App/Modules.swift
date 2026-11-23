import Foundation
import PersonaModuleAPI
import UIKit

// The app's feature modules (#13432, part of #13430; docs/apple/ios.md, "Modules"). This file is the seed of the
// `PersonaHostApp` layer (#13430 decision 4) and the only core file that names a module's type: everything else reads
// `Modules.registry`. scripts/test/test_apple_module_refs.py keeps it that way.

/// The host the modules plug into: what their entries hand back to the app.
enum PersonaHostApp: ModuleHost {
    /// A composer action's sheet hands the composer a picture.
    typealias Attachment = UIImage
}

/// A composer action of an active module (`ModuleSlot.composerActions`).
typealias ModuleComposerAction = ComposerAction<PersonaHostApp>

enum Modules {
    /// The built-in modules, in the order their entries appear. A module is active only when it is listed here and
    /// enabled (`isEnabled`).
    static var builtIn: [any PersonaModule<PersonaHostApp>] {
        [GlassesModule()]
    }

    /// Built once, at its first read in `application(_:didFinishLaunchingWithOptions:)`: describing the entries only,
    /// no IO (docs/apple/performance.md, P10). Reads are array reads, cheap in `body`.
    static let registry: ModuleRegistry<PersonaHostApp> = {
        let registry = ModuleRegistry(modules: builtIn, isEnabled: isEnabled)
        assert(registry.problems.isEmpty, "module registrations refused: \(registry.problems)")
        return registry
    }()

    /// The switch. A module is enabled when its build includes it: the build-time overlay or config that compiles its
    /// SDK in (for glasses `apple/meta-wearables.yml`, `app.meta_wearables.enabled`). There is no everyday setting.
    /// DEBUG builds take `-showModules <ids>`: the comma-separated modules to turn on, all others off (`none` for
    /// none), so screenshots and tests can show a module on or off in any build; an entry of a module whose SDK is
    /// not built in then opens its sheet with the module's own "not supported" state.
    static func isEnabled(_ module: any PersonaModule<PersonaHostApp>) -> Bool {
        #if DEBUG
        if let list = UserDefaults.standard.string(forKey: "showModules") {
            return list.split(separator: ",").contains { $0.trimmingCharacters(in: .whitespaces) == module.descriptor.id.rawValue }
        }
        #endif
        return module.isAvailable
    }
}
