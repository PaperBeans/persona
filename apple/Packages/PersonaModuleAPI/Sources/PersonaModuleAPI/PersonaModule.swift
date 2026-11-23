import Foundation
import SwiftUI

/// The app that hosts modules: the types a module hands back to it. The iOS and Vision apps' host is `PersonaHostApp`
/// (#13430 decision 4); tests use their own.
public protocol ModuleHost {
    /// What a composer action's sheet hands back to the composer: a picture in the apps.
    associatedtype Attachment
}

/// A feature module. Its type is named in one place only, the app's module list; the core reaches it through the
/// `ModuleRegistry` built from that list.
@MainActor
public protocol PersonaModule<Host> {
    associatedtype Host: ModuleHost
    var descriptor: ModuleDescriptor { get }
    /// The module is built into this binary (its SDK compiled in, its build overlay on). An unavailable module is
    /// never active, whatever else says so.
    var isAvailable: Bool { get }
    /// Adds the module's entries. Called once, when the registry is built; do no work here beyond describing entries.
    func register(into registrar: inout ModuleRegistrar<Host>)
}

/// An entry in the composer's + (`ModuleSlot.composerActions`): a row with a title and an icon that opens a sheet;
/// the sheet hands its result to `deliver`.
public struct ComposerAction<Host: ModuleHost>: Identifiable {
    /// Unique among all composer actions. The composer uses it for the prompt bar's source id and, as `attach-<id>`,
    /// for the + panel's row (and its accessibility identifier).
    public let id: String
    public let module: ModuleID
    /// The row's title and the prompt bar's subtitle, resolved where they are drawn, as before modules.
    public let title: LocalizedStringResource
    public let detail: LocalizedStringResource
    /// A design-system symbol name (`Theme.Icon`), never a literal SF Symbol in the module.
    public let symbol: String
    private let makeSheet: @MainActor (@escaping (Host.Attachment) -> Void) -> AnyView

    public init(id: String, module: ModuleID, title: LocalizedStringResource, detail: LocalizedStringResource, symbol: String,
                sheet: @escaping @MainActor (@escaping (Host.Attachment) -> Void) -> AnyView) {
        self.id = id
        self.module = module
        self.title = title
        self.detail = detail
        self.symbol = symbol
        self.makeSheet = sheet
    }

    /// The sheet's content; `deliver` takes what it produced.
    @MainActor public func sheet(deliver: @escaping (Host.Attachment) -> Void) -> AnyView { makeSheet(deliver) }
}
