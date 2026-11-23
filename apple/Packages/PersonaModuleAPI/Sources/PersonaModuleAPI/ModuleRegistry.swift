import Foundation
import SwiftUI

/// What one module hands to `register(into:)`: one method per served slot. An entry for a slot the module's descriptor
/// does not list, or a composer action id already taken, is dropped and recorded in `ModuleRegistry.problems`.
@MainActor
public struct ModuleRegistrar<Host: ModuleHost> {
    let module: ModuleDescriptor
    var launchHooks: [() -> Void] = []
    var urlHandlers: [(URL) -> Bool] = []
    var composerActions: [ComposerAction<Host>] = []
    var problems: [String] = []

    init(module: ModuleDescriptor) { self.module = module }

    private mutating func allows(_ slot: ModuleSlot) -> Bool {
        if module.slots.contains(slot) { return true }
        problems.append("\(module.id): registers into \(slot.rawValue), which its descriptor does not declare")
        return false
    }

    /// Runs once at launch, after the core's own launch work (`ModuleSlot.launchHooks`). Keep it cheap: no IO before
    /// the first frame (docs/apple/performance.md, P10).
    public mutating func onLaunch(_ run: @escaping () -> Void) {
        guard allows(.launchHooks) else { return }
        launchHooks.append(run)
    }

    /// Claims the URLs the module owns (`ModuleSlot.deepLinks`): return true for one it took, false to pass it on.
    public mutating func handleURLs(_ handle: @escaping (URL) -> Bool) {
        guard allows(.deepLinks) else { return }
        urlHandlers.append(handle)
    }

    /// Adds an entry to the composer's + (`ModuleSlot.composerActions`).
    public mutating func composerAction<Sheet: View>(id: String, title: LocalizedStringResource, detail: LocalizedStringResource,
                                                     symbol: String,
                                                     @ViewBuilder sheet: @escaping (_ deliver: @escaping (Host.Attachment) -> Void) -> Sheet) {
        guard allows(.composerActions) else { return }
        composerActions.append(ComposerAction(id: id, module: module.id, title: title, detail: detail, symbol: symbol) { deliver in
            AnyView(sheet(deliver))
        })
    }
}

/// The active modules and their entries, built once from the app's module list. A module is active when it is in the
/// list and `isEnabled` says so; an inactive module registers nothing, so it leaves no row, no handler and no hook.
///
/// Lookups are plain array reads over what was built at launch: cheap and stable enough to call from `body`
/// (docs/apple/performance.md, P4 / P5). The registry is a value with no observable state.
@MainActor
public struct ModuleRegistry<Host: ModuleHost> {
    /// The active modules' descriptors, in list order.
    public let active: [ModuleDescriptor]
    /// Every composer action of the active modules, in list order, then registration order.
    public let composerActions: [ComposerAction<Host>]
    /// Registrations that were refused: an undeclared slot, a duplicate module id or composer action id, a composer
    /// action id the core uses itself, a malformed id. Empty in a correct build; the app asserts it in DEBUG and the tests check it.
    public let problems: [String]
    private let launchHooks: [() -> Void]
    private let urlHandlers: [(URL) -> Bool]

    /// - Parameters:
    ///   - modules: the module list, in the order entries should appear.
    ///   - isEnabled: whether a listed module is switched on. The default takes every module built into this binary.
    /// The composer's own attach source ids (photos, the camera, files, voice): a module's composer action may not take one.
    public static var coreComposerIDs: Set<String> { ["photo", "camera", "file", "voice"] }

    public init(modules: [any PersonaModule<Host>], isEnabled: @MainActor (any PersonaModule<Host>) -> Bool = { $0.isAvailable }) {
        var active: [ModuleDescriptor] = []
        var actions: [ComposerAction<Host>] = []
        var problems: [String] = []
        var hooks: [() -> Void] = []
        var handlers: [(URL) -> Bool] = []
        var seen = Set<ModuleID>()
        var actionIDs = Set<String>()
        for module in modules {
            let descriptor = module.descriptor
            guard descriptor.id.isWellFormed else {
                problems.append("\(descriptor.id): not a module id (lower-case words joined by hyphens)")
                continue
            }
            guard seen.insert(descriptor.id).inserted else {
                problems.append("\(descriptor.id): listed twice")
                continue
            }
            guard isEnabled(module) else { continue }
            var registrar = ModuleRegistrar<Host>(module: descriptor)
            module.register(into: &registrar)
            problems += registrar.problems
            for action in registrar.composerActions {
                if Self.coreComposerIDs.contains(action.id) {
                    problems.append("\(descriptor.id): composer action \(action.id) is one of the core's own")
                } else if actionIDs.insert(action.id).inserted {
                    actions.append(action)
                } else {
                    problems.append("\(descriptor.id): composer action \(action.id) is already taken")
                }
            }
            hooks += registrar.launchHooks
            handlers += registrar.urlHandlers
            active.append(descriptor)
        }
        self.active = active
        self.composerActions = actions
        self.problems = problems
        self.launchHooks = hooks
        self.urlHandlers = handlers
    }

    /// The module is in the list and switched on.
    public func isActive(_ id: ModuleID) -> Bool { active.contains { $0.id == id } }

    /// Runs the active modules' launch hooks, in list order. Call once, at launch.
    public func launch() {
        for hook in launchHooks { hook() }
    }

    /// Offers a URL to the active modules in list order; true when one took it.
    public func handle(_ url: URL) -> Bool {
        urlHandlers.contains { $0(url) }
    }

    /// The composer action with this id, nil when no active module offers it.
    public func composerAction(id: String) -> ComposerAction<Host>? {
        composerActions.first { $0.id == id }
    }
}
