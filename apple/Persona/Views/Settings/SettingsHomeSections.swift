import SwiftUI

struct SettingsAssistantHeading: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if model.managerRoster?.isMulti == true, let manager = model.currentAssistant {
                AssistantSwitcherHeader(manager: manager, nameFont: Theme.Font.bodyStrong, showsSwitchLabel: true, openSettings: {})
                Text(String(localized: "\(manager.name)'s settings")).font(Theme.Font.labelStrong)
            } else {
                HStack(spacing: Theme.Space.m) {
                    PersonaAvatar(size: Theme.Size.avatar)
                    Text(model.persona.cardTitle).font(Theme.Font.bodyStrong).lineLimit(1)
                }
                Text("Current Assistant's Settings").font(Theme.Font.labelStrong)
            }
            Text("Only the current assistant's settings are shown. Switch assistants to change another's settings.")
                .font(Theme.Font.label).foregroundStyle(.secondary)
        }
        .textCase(nil)
        .accessibilityIdentifier("settings-group-assistant")
    }
}

struct SettingsPairingPage: View {
    @Environment(AppModel.self) private var model
    @State private var showPair = false
    @State private var confirmUnpair = false

    var body: some View {
        SettingsForm {
            Section {
                NavigationLink(value: SettingsPage.devices) {
                    SettingsRow(title: String(localized: "Devices"), symbol: Theme.Icon.credentials, tint: Theme.Hue.gray)
                }
                .settingsListRow(SettingsPage.devices)
                Button { showPair = true } label: {
                    SettingsRow(title: String(localized: "Pair Again"), symbol: Theme.Icon.pairing, tint: Theme.accent)
                }
                .modifier(SettingsButtonLook())
                .disabled(model.hubSignOut != nil)
            }
            #if os(iOS) || os(visionOS)
            if !model.isDemo {
                Section {
                    Button { Task { await syncWatch() } } label: {
                        SettingsRow(title: String(localized: "Sync to Apple Watch"), symbol: Theme.Icon.watch, tint: Theme.accent)
                    }
                    .modifier(SettingsButtonLook())
                    .accessibilityIdentifier("settings-watch-sync")
                } footer: {
                    Text("Persona on Apple Watch connects to the hub directly using your iPhone's pairing. If the watch shows “Not Paired”, tap here, or tap “Sync from iPhone” on the watch.")
                }
            }
            #endif
            if !model.isDemo {
                Section {
                    Button(role: .destructive) { confirmUnpair = true } label: {
                        SettingsRow(title: model.isMultiHub ? String(localized: "Disconnect All Hubs") : String(localized: "Disconnect and Forget Hub"),
                                    symbol: Theme.Icon.end, tint: Theme.danger)
                            .trailingProgress(model.hubSignOut == .all)
                    }
                    .modifier(SettingsButtonLook())
                    .disabled(model.hubSignOut != nil)
                }
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Devices & Pairing"))
        .confirmationDialog("After disconnecting, you'll need to scan the code to pair again.", isPresented: $confirmUnpair, titleVisibility: .visible) {
            Button("Disconnect", role: .destructive) { Task { await model.disconnectAll() } }
            Button("Cancel", role: .cancel) {}
        }
        .sheet(isPresented: $showPair) { PairView() }
    }

    #if os(iOS) || os(visionOS)
    private func syncWatch() async {
        guard model.isPaired else { return }
        guard WatchSync.shared.watchAppInstalled else {
            Haptics.error()
            model.toast = Toast(String(localized: "No Apple Watch with Persona found. Install it first in the Watch app on your iPhone."), style: .error)
            return
        }
        await model.upgradeCredentials()
        WatchSync.shared.push(summary: model.watchSummary())
        if model.watchConfigs().missing?.isEmpty == false {
            Haptics.error()
            model.toast = Toast(String(localized: "Couldn't get credentials for the watch. Check your network and try again; if the hub is too old, update it first."), style: .error)
        } else {
            Haptics.success()
            model.toast = Toast(String(localized: "Sent to Apple Watch. Open Persona on your watch."), style: .success)
        }
    }
    #endif
}
