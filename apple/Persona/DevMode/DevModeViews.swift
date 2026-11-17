import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// Settings › About's Developing Persona group (#9460, docs/operations/developing-persona.md): the switch, and while
/// it is on, how much is recorded and the way to export it. Turning it off with something recorded asks first and
/// offers to export.
struct DevModeSection: View {
    @Environment(AppModel.self) private var model
    @State private var confirmOff = false
    private let dev = DevMode.shared

    var body: some View {
        Section {
            Toggle(isOn: Binding(get: { dev.isOn }, set: switched)) {
                Text("Developing Persona").lineLimit(1)
            }
            .accessibilityIdentifier("dev-mode-switch")
            if dev.isOn {
                LabeledContent(String(localized: "Recorded [Developing Persona]"), value: recorded)
                    .accessibilityIdentifier("dev-mode-recorded")
                NavigationLink(value: SettingsPage.devExport) {
                    Text("Export Diagnostics…").lineLimit(1)
                }
                .accessibilityIdentifier("dev-mode-export")
            }
        } footer: {
            Text("When this is on, this device records logs, errors and energy use so you can export them for an Agent to look into. The data stays on this device; turning this off deletes it.")
        }
        .task(id: dev.isOn) { await dev.refresh() }
        .alert(String(localized: "Turn off and delete the \(Self.bytes(dev.size ?? 0)) recorded?"), isPresented: $confirmOff) {
            Button(String(localized: "Export First…")) { model.settingsPath.append(SettingsPage.devExport) }
            Button(String(localized: "Turn Off and Delete"), role: .destructive) { dev.setOn(false) }
            Button(String(localized: "Cancel"), role: .cancel) {}
        }
    }

    private func switched(_ on: Bool) {
        if on || (dev.size ?? 0) == 0 { dev.setOn(on) } else { confirmOff = true }
    }

    private var recorded: String {
        let since = dev.since?.formatted(.dateTime.month(.abbreviated).day().hour().minute()) ?? "–"
        guard let size = dev.size else { return String(localized: "Since \(since)") }
        return String(localized: "Since \(since) · \(Self.bytes(size))")
    }

    static func bytes(_ n: Int64) -> String { n.formatted(.byteCount(style: .file)) }
}

/// Export Diagnostics (Settings › About › Developing Persona): what the package holds, which of it to leave out, and
/// where it goes.
struct DevExportPage: View {
    @Environment(AppModel.self) private var model
    @State private var picked = Set(DevPackage.Category.allCases)
    @State private var busy = false
    #if os(iOS)
    @State private var sharing: DevExportFile?
    #endif
    private let dev = DevMode.shared

    var body: some View {
        SettingsForm {
            Section {
                HStack(spacing: Theme.Space.m) {
                    IconTile(Theme.Icon.fileArchive, tint: Theme.accent)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(window).font(Theme.Font.heading)
                        Text(details).font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("dev-export-summary")
            }
            Section {
                row(.logs, String(localized: "Logs"), String(localized: "App logs, screens, request errors"))
                row(.errors, String(localized: "Errors and Crashes"), String(localized: "Crashes, hangs, uncaught exceptions"))
                row(.energy, String(localized: "Energy and Performance"), String(localized: "MetricKit daily reports, heat, battery, requests and connections each minute"))
                HStack(spacing: Theme.Space.m) {
                    ThemedIcon(Theme.Icon.locked).foregroundStyle(.secondary)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text("Device and App Version")
                        Text("Model, system, app version (always included)").font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                }
                .accessibilityElement(children: .combine)
            } header: {
                Text("Includes")
            } footer: {
                Text("Never included: message text, knowledge base content, keys, tokens, passwords, cookies, pairing links.")
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Export Diagnostics"))
        .safeAreaInset(edge: .bottom) { bottomBar }
        .task { await dev.refresh() }
        #if os(iOS)
        .sheet(item: $sharing) { file in
            FileShareSheet(url: file.url) { sharing = nil }
        }
        #endif
    }

    private func row(_ category: DevPackage.Category, _ title: String, _ detail: String) -> some View {
        Button {
            if picked.contains(category) { picked.remove(category) } else { picked.insert(category) }
        } label: {
            HStack(spacing: Theme.Space.m) {
                ThemedIcon(picked.contains(category) ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                    .foregroundStyle(picked.contains(category) ? Theme.accent : .secondary)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(title).foregroundStyle(Color.primary) // not the button's tint
                    Text(detail).font(Theme.Font.caption).foregroundStyle(Color.secondary)
                }
            }
        }
        .accessibilityAddTraits(picked.contains(category) ? .isSelected : [])
        .accessibilityIdentifier("dev-export-\(category.rawValue)")
    }

    private var bottomBar: some View {
        VStack(spacing: Theme.Space.s) {
            Button(action: share) {
                Group {
                    if busy {
                        ProgressView()
                    } else {
                        Label { Text("Share File…").buttonLabelLine() } icon: { ThemedIcon(Theme.Icon.share) }
                    }
                }
                .frame(maxWidth: .infinity)
            }
            .themedButton(prominent: true)
            .disabled(busy)
            .accessibilityIdentifier("dev-export-share")
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.groupedPage)
    }

    private var window: String {
        let since = dev.since?.formatted(.dateTime.month(.abbreviated).day().hour().minute()) ?? "–"
        return String(localized: "\(since) – Now")
    }

    private var details: String {
        let size = dev.size.map(DevModeSection.bytes) ?? "–"
        #if canImport(UIKit)
        let system = "\(UIDevice.current.systemName) \(UIDevice.current.systemVersion)"
        #else
        let system = ProcessInfo.processInfo.operatingSystemVersionString
        #endif
        return String(localized: "About \(size) · zip · \(system)")
    }

    private func share() {
        busy = true
        let picked = picked
        Task {
            defer { busy = false }
            do {
                let url = try await dev.exportDiagnostics(picked)
                #if os(iOS)
                sharing = DevExportFile(url: url)
                #endif
            } catch {
                model.toast = Toast(String(localized: "Couldn’t export: \(error.localizedDescription)"), style: .error)
            }
        }
    }
}

#if os(iOS)
private struct DevExportFile: Identifiable {
    let url: URL
    var id: URL { url }
}
#endif

extension View {
    /// Tells Developing Persona this screen is in front while it is shown (#9460); free while the switch is off.
    func devScreen(_ name: String) -> some View { modifier(DevScreen(name: name)) }
}

private struct DevScreen: ViewModifier {
    let name: String
    @State private var token: UUID?

    func body(content: Content) -> some View {
        content
            .onAppear { token = DevMode.shared.enter(name) }
            .onDisappear { if let token { DevMode.shared.leave(token) }; token = nil }
    }
}
