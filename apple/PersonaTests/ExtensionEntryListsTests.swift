import Foundation
import SwiftUI
import Testing
import UIKit
@testable import Persona
import PersonaWire

/// Extension settings entry lists (#9504 / #12325): opt-in `entry_lists=1` and `preserve_entries=1` wire contract,
/// entry-list schema/secret/provider decoding, add/edit/remove draft lifecycle, stable UI keys that never leak into JSON,
/// required empty-array handling, indexed validation errors, immediate fixed-field preservation, and per-entry sealed secrets.
@MainActor
struct ExtensionEntryListsTests {
    private static func makeDetail(
        required: Bool = false,
        minItems: Int? = nil,
        maxItems: Int? = 2,
        entriesJSON: String = #"[{"_id":"entry_saved","name":"One","address":"https://one.example.com"}]"#,
        secretsJSON: String = #"[{"name":"fixed_key","title":"Fixed key","set":true,"hint":"9999"},{"name":"entry_saved","title":"Account key","required":true,"set":true,"hint":"1234","list":"accounts","entry_title":"One"}]"#,
        secretUsersJSON: String = #"[]"#,
        includeMachines: Bool = true,
        includeLauncherField: Bool = false,
        clearsSecret: Bool = true
    ) -> String {
        let minPart = minItems.map { #","min_items":\#($0)"# } ?? ""
        let maxPart = maxItems.map { #","max_items":\#($0)"# } ?? ""
        let launcherSub = includeLauncherField ? #",{"name":"launcher","type":"string","format":"launcher","title":"Launcher"}"# : ""
        let machinesField = includeMachines ? #",{"name":"machines","type":"array","title":"Machines","items":{"type":"string","format":"machine","os":["darwin"]}}"# : ""
        return #"""
        {
          "id": "example-entries",
          "name": "Example Entries",
          "kind": "connectors",
          "status": "running",
          "source": "installed",
          "enabled": true,
          "form": {
            "fields": [
              {
                "name": "accounts",
                "type": "array",
                "title": "Accounts",
                "required": \#(required ? "true" : "false")\#(minPart)\#(maxPart),
                "secret": {"name": "account_key", "title": "Account key", "description": "API token", "required": true, "hint": "last4"},
                "items": {
                  "type": "object",
                  "fields": [
                    {"name": "name", "type": "string", "title": "Name", "required": true},
                    {"name": "address", "type": "string", "title": "Address", "format": "uri", "required": true, "clears_secret": \#(clearsSecret ? "true" : "false")}\#(launcherSub)
                  ]
                }
              }\#(machinesField)
            ]
          },
          "config": {"accounts": \#(entriesJSON)},
          "secrets": \#(secretsJSON),
          "secret_users": \#(secretUsersJSON),
          "permissions": []
        }
        """#
    }

    // MARK: 1. Wire contract: entry_lists=1 & preserve_entries=1

    @Test func everyDetailRequestOptsIntoEntryListsAndImmediateSavesPreserveEntries() async throws {
        for extensionsAPI in [false, true] {
            let root = extensionsAPI ? "/api/v1/extensions" : "/api/v1/plugins"
            let hub = FakeHub()
            let sample = Self.makeDetail()
            hub.on("GET", "\(root)/example-entries/-/", json: sample)
            hub.on("PUT", "\(root)/example-entries/-/enabled", json: sample)
            hub.on("PUT", "\(root)/example-entries/-/contributions/ui/page", json: sample)
            hub.on("PUT", "\(root)/example-entries/-/auto-upgrade", json: sample)
            hub.on("PUT", "\(root)/example-entries/-/config", json: sample)
            hub.on("PUT", "\(root)/example-entries/-/secrets/entry_saved", json: sample)
            hub.on("DELETE", "\(root)/example-entries/-/secrets/entry_saved", json: sample)
            hub.on("DELETE", "\(root)/example-entries/-/handoff-rules/rule_1", json: sample)
            hub.on("POST", "\(root)/-/install", json: sample)
            hub.on("POST", "\(root)/example-entries/-/upgrade", json: sample)

            var client = hub.client
            client.extensionsAPI = extensionsAPI

            _ = try await client.plugin("example-entries")
            _ = try await client.setExtension("example-entries", enabled: false)
            _ = try await client.setExtensionContribution("example-entries", point: "ui", id: "page", enabled: false)
            _ = try await client.setExtension("example-entries", autoUpgrade: true)
            _ = try await client.setExtension("example-entries", config: ["x": .number(1)])
            _ = try await client.setExtension("example-entries", config: ["x": .number(2)], preserveEntries: true)
            _ = try await client.setExtension("example-entries", secret: "entry_saved", value: "placeholder-5678")
            _ = try await client.setExtension("example-entries", secret: "entry_saved", reference: "op://Dev/Accounts/one")
            _ = try await client.clearExtension("example-entries", secret: "entry_saved")
            _ = try await client.removeHandoffRule("example-entries", rule: "rule_1")
            let preview = try JSON.decoder.decode(ExtensionPreview.self, from: Data(#"{"preview_id":"pv","permissions_digest":"dig","manifest":{"id":"example-entries"},"permissions":[]}"#.utf8))
            _ = try await client.installExtension(preview: preview)
            _ = try await client.upgradeExtension("example-entries", preview: preview)

            #expect(hub.sent("GET", "\(root)/example-entries/-/").singleQuery("entry_lists") == "1")
            #expect(hub.sent("PUT", "\(root)/example-entries/-/enabled").singleQuery("entry_lists") == "1")
            #expect(hub.sent("PUT", "\(root)/example-entries/-/contributions/ui/page").singleQuery("entry_lists") == "1")
            #expect(hub.sent("PUT", "\(root)/example-entries/-/auto-upgrade").singleQuery("entry_lists") == "1")
            #expect(hub.sent("DELETE", "\(root)/example-entries/-/secrets/entry_saved").singleQuery("entry_lists") == "1")
            #expect(hub.sent("DELETE", "\(root)/example-entries/-/handoff-rules/rule_1").singleQuery("entry_lists") == "1")
            #expect(hub.sent("POST", "\(root)/-/install").singleQuery("entry_lists") == "1")
            #expect(hub.sent("POST", "\(root)/example-entries/-/upgrade").singleQuery("entry_lists") == "1")

            let secretPuts = hub.sent("PUT", "\(root)/example-entries/-/secrets/entry_saved")
            #expect(secretPuts.count == 2)
            #expect(secretPuts.allSatisfy { $0.query["entry_lists"] == "1" })

            let configPuts = hub.sent("PUT", "\(root)/example-entries/-/config")
            #expect(configPuts.count == 2)
            #expect(configPuts[0].query == ["entry_lists": "1"])
            #expect(configPuts[1].query == ["entry_lists": "1", "preserve_entries": "1"])

            // Verify URL formatting puts both query parameters cleanly in canonical order.
            let built = client.url(client.extensionPath("/example-entries/-/config"), query: APIClient.preserveEntriesQuery)
            #expect(built.query == "entry_lists=1&preserve_entries=1")
        }
    }

    // MARK: 2. Decoding & Display Helpers

    @Test func entryListSchemaDynamicSecretsAndProviderUsersDecode() throws {
        let users = #"[{"kind":"extension","extension":"example-entries","extension_name":"Example Entries","secret":"entry_saved","secret_title":"Account key","entry_title":"One","reference":"op://Dev/Accounts/one"},{"kind":"launcher","launcher":"gateway","launcher_name":"Gateway","machine":"macbook","reference":"op://Dev/Gateway/key"}]"#
        let detail = try JSON.decoder.decode(HubExtensionDetail.self, from: Data(Self.makeDetail(maxItems: 4, secretUsersJSON: users).utf8))

        let listField = try #require(detail.form.first)
        #expect(listField.isEntryList)
        #expect(listField.maxItems == 4)
        #expect(listField.secret?.name == "account_key")
        #expect(listField.secret?.title == "Account key")
        #expect(listField.secret?.description == "API token")
        #expect(listField.secret?.required == true)
        #expect(listField.secret?.hint == "last4")
        #expect(listField.items?.fields?.map(\.name) == ["name", "address"])
        #expect(listField.items?.fields?[1].clearsSecret == true)
        // A regular string array is not an entry list.
        #expect(!ExtensionField(name: "tags", type: "array", items: ExtensionItems(type: "string")).isEntryList)
        // An object array without x-secret or without fields is not an entry list.
        #expect(!ExtensionField(name: "raw", type: "array", items: ExtensionItems(type: "object", fields: [ExtensionField(name: "a", type: "string")])).isEntryList)
        #expect(!ExtensionField(name: "empty", type: "array", items: ExtensionItems(type: "object", fields: []), secret: ExtensionEntrySecret(name: "k", title: "K")).isEntryList)

        // Dynamic entry secrets are distinguished from fixed secrets.
        #expect(detail.secrets.count == 2)
        #expect(!detail.secrets[0].isEntrySecret)
        #expect(detail.secrets[0].hint == "9999")
        #expect(detail.secrets[1].isEntrySecret)
        #expect(detail.secrets[1].list == "accounts")
        #expect(detail.secrets[1].entryTitle == "One")
        #expect(detail.secrets[1].hint == "1234")
        #expect(ExtensionText.secretState(detail.secrets[1]) == "已设置 · 末 4 位 1234")
        #expect(ExtensionText.secretSheetTitle(detail.secrets[0]) == "Fixed key")
        #expect(ExtensionText.secretSheetTitle(detail.secrets[1]) == "One · Account key")
        let untitledSecret = ExtensionSecret(name: "entry_2", title: "Account key", set: false, list: "accounts")
        #expect(ExtensionText.secretSheetTitle(untitledSecret, fallbackEntryTitle: "Two") == "Two · Account key")

        // Provider "Used by" rows include the entry title for extension entry secrets and leave launchers untouched.
        let extUser = detail.secretUsers[0]
        #expect(extUser.entryTitle == "One")
        #expect(ExtensionText.secretUserLabel(extUser, machineName: { $0 }) == "Example Entries › Account key (One)")
        #expect(ExtensionText.secretUserDetail(extUser) == "One · op://Dev/Accounts/one")

        let launcherUser = detail.secretUsers[1]
        #expect(launcherUser.entryTitle == nil)
        #expect(ExtensionText.secretUserDetail(launcherUser) == "op://Dev/Gateway/key")
        #expect(ExtensionText.secretUserLabel(launcherUser, machineName: { _ in "MacBook" }) == String(localized: "\("Gateway") key on \("MacBook")"))
    }

    @Test func entryTitlePrefersNameThenFirstNonEmptyFieldThenNumberedLabel() {
        let list = ExtensionField(
            name: "accounts",
            type: "array",
            items: ExtensionItems(type: "object", fields: [
                ExtensionField(name: "address", type: "string", title: "Address"),
                ExtensionField(name: "name", type: "string", title: "Name"),
            ]),
            secret: ExtensionEntrySecret(name: "k", title: "K")
        )
        #expect(ExtensionText.entryTitle(list: list, entry: ["_id": .string("entry_1"), "address": .string("https://a.example.com"), "name": .string(" Primary ")], index: 0) == "Primary")
        #expect(ExtensionText.entryTitle(list: list, entry: ["_id": .string("entry_1"), "address": .string("https://a.example.com"), "name": .string("   ")], index: 0) == "https://a.example.com")
        // Host-minted `_id` is never used as the display title; falls back to localized numbered label.
        #expect(ExtensionText.entryTitle(list: list, entry: ["_id": .string("entry_1")], index: 1) == "条目 2")
        #expect(ExtensionText.entryTitle(list: list, entry: ["_id": .string("entry_1"), "name": .number(42)], index: 0) == "条目 1")
    }

    // MARK: 3. Path formatting & Compaction

    @Test func fieldPathFormatsIndexedAndDottedPaths() {
        #expect(ExtensionModel.fieldPath(["accounts"]) == "accounts")
        #expect(ExtensionModel.fieldPath(["accounts", "0", "address"]) == "accounts[0].address")
        #expect(ExtensionModel.fieldPath(["accounts", "12", "_id"]) == "accounts[12]._id")
        #expect(ExtensionModel.fieldPath(["proxy", "host"]) == "proxy.host")
        #expect(ExtensionModel.fieldPath(["accounts", "01", "name"]) == "accounts.01.name")
    }

    @Test func compactPreservesEmptyArrayOnlyForRequiredEntryLists() {
        let empty: [String: JSONValue] = ["accounts": .array([]), "tags": .array([]), "note": .string("")]
        #expect(ExtensionModel.compact(empty) == [:])
        #expect(ExtensionModel.compact(empty, requiredEntryLists: ["accounts"]) == ["accounts": .array([])])
    }

    // MARK: 4. Full Entry-List Lifecycle, Stable Keys, Validation, Preserving Immediate Writes & Secrets

    @Test func entryListAddEditSaveSecretAndValidationLifecycle() async throws {
        let hub = FakeHub()
        var currentJSON = Self.makeDetail(maxItems: 2)
        var sentConfigs: [JSONValue] = []
        var preservedConfigs: [JSONValue] = []
        var sentSecrets: [(String, String)] = []

        hub.on("GET", "/api/v1/plugins/example-entries/-/") { _ in Data(currentJSON.utf8) }
        var refuseNext: APIError?
        hub.on("PUT", "/api/v1/plugins/example-entries/-/config") { req in
            let body = try JSON.decoder.decode(JSONValue.self, from: #require(req.body))
            let cfg = try #require(body["config"]?.object)
            if req.query["preserve_entries"] == "1" {
                preservedConfigs.append(.object(cfg))
                return Data(currentJSON.utf8)
            }
            if let refuse = refuseNext {
                throw refuse
            }
            sentConfigs.append(.object(cfg))
            // Simulate hub minting `_id` for new entries and deriving dynamic secret rows.
            let accounts = (cfg["accounts"]?.array ?? []).enumerated().map { idx, el -> JSONValue in
                var obj = el.object ?? [:]
                if obj["_id"] == nil { obj["_id"] = .string("entry_minted_\(idx)") }
                return .object(obj)
            }
            let encodedAccounts = String(decoding: try JSON.encoder.encode(JSONValue.array(accounts)), as: UTF8.self)
            let secrets: [[String: Any]] = accounts.map { el in
                let obj = el.object ?? [:]
                let id = obj["_id"]?.string ?? ""
                let title = obj["name"]?.string ?? ""
                return [
                    "name": id,
                    "title": "Account key",
                    "required": true,
                    "set": id == "entry_saved",
                    "hint": id == "entry_saved" ? "1234" : "",
                    "list": "accounts",
                    "entry_title": title,
                ]
            }
            let encodedSecrets = String(decoding: try JSONSerialization.data(withJSONObject: secrets), as: UTF8.self)
            currentJSON = Self.makeDetail(maxItems: 2, entriesJSON: encodedAccounts, secretsJSON: encodedSecrets)
            return Data(currentJSON.utf8)
        }
        hub.on("PUT", "/api/v1/plugins/example-entries/-/secrets/entry_saved") { req in
            let body = try JSON.decoder.decode([String: String].self, from: #require(req.body))
            let value = try #require(body["value"])
            sentSecrets.append(("entry_saved", value))
            currentJSON = currentJSON.replacingOccurrences(of: #""hint":"1234""#, with: #""hint":"5678""#)
            return Data(currentJSON.utf8)
        }

        let drafts = ExtensionDrafts()
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }, drafts: drafts))
        await page.load()
        #expect(!page.dirty)
        #expect(page.entryKeys["accounts"] == ["entry_saved"])

        // Add up to maxItems = 2; a third add is ignored.
        page.addEntry(to: "accounts")
        page.addEntry(to: "accounts")
        #expect(ExtensionModel.entryItems(in: page.draft, at: ["accounts"]).count == 2)
        let localKey = try #require(page.entryKeys["accounts"]?[1])
        #expect(localKey.hasPrefix("new-entry-"))

        // Editing fields keeps the stable local key and preserves host-minted `_id` on the saved entry.
        page.set(.string("Two"), at: ["accounts", "1", "name"])
        page.set(.string("not-a-uri"), at: ["accounts", "1", "address"])
        #expect(page.entryKeys["accounts"]?[1] == localKey)
        #expect(page.value(["accounts", "0", "_id"]) == .string("entry_saved"))

        // Clearing a field in an unsaved entry keeps the entry object instead of removing the row.
        page.set(nil, at: ["accounts", "1", "name"])
        #expect(ExtensionModel.entryItems(in: page.draft, at: ["accounts"]).count == 2)
        page.set(.string("Two"), at: ["accounts", "1", "name"])

        // Rebuilding the model preserves the unsaved entry draft and its local key without colliding on future adds.
        let rebuilt = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }, drafts: drafts))
        await rebuilt.load()
        #expect(rebuilt.entryKeys["accounts"] == ["entry_saved", localKey])
        #expect(rebuilt.value(["accounts", "1", "name"]) == .string("Two"))

        // Immediate fixed-field save uses preserve_entries=1 and keeps unsaved entry edits and keys intact.
        await rebuilt.saveNow(.array([.string("mac-a")]), at: ["machines"])
        #expect(preservedConfigs.count == 1)
        #expect(sentConfigs.isEmpty)
        #expect(rebuilt.entryKeys["accounts"] == ["entry_saved", localKey])
        #expect(rebuilt.value(["accounts", "1", "name"]) == .string("Two"))

        // Setting the saved entry's secret updates its last4 hint and preserves unsaved entry edits.
        #expect(await rebuilt.setSecret("entry_saved", value: "placeholder-secret-5678") == .saved)
        #expect(sentSecrets.count == 1 && sentSecrets[0].0 == "entry_saved" && sentSecrets[0].1 == "placeholder-secret-5678")
        #expect(rebuilt.detail?.secrets.first(where: { $0.name == "entry_saved" })?.hint == "5678")
        #expect(rebuilt.value(["accounts", "1", "name"]) == .string("Two"))

        // Indexed validation refusal maps to accounts[1].address and clears when edited.
        refuseNext = APIError(status: 400, message: "accounts[1].address: must be a URL",
                              fields: ["code": "invalid_config", "field": "accounts[1].address", "message": "must be a URL"])
        #expect(await rebuilt.save() == false)
        #expect(rebuilt.fieldError(["accounts", "1", "address"]) == "must be a URL")
        #expect(rebuilt.error == nil && rebuilt.formError == nil)

        refuseNext = nil
        rebuilt.set(.string("https://two.example.com"), at: ["accounts", "1", "address"])
        #expect(rebuilt.fieldError(["accounts", "1", "address"]) == nil)

        // Saving sends _id on saved entry, omits local keys and secret fields, and adopts minted _id.
        #expect(await rebuilt.save())
        let savedPayload = try #require(sentConfigs.last)
        let rawSaved = String(decoding: try JSON.encoder.encode(savedPayload), as: UTF8.self)
        #expect(!rawSaved.contains("new-entry-"))
        #expect(!rawSaved.contains("account_key"))
        #expect(!rawSaved.contains("placeholder-secret-5678"))
        let savedAccounts = try #require(savedPayload["accounts"]?.array)
        #expect(savedAccounts.count == 2)
        #expect(savedAccounts[0]["_id"] == .string("entry_saved"))
        #expect(savedAccounts[1]["_id"] == nil)
        #expect(rebuilt.entryKeys["accounts"] == ["entry_saved", "entry_minted_1"])
        #expect(!rebuilt.dirty)
    }

    @Test func defaultMaxEntriesCapsAt32WhenSchemaOmitsMaxItems() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail(maxItems: nil, entriesJSON: "[]", secretsJSON: "[]"))
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
        await page.load()
        for _ in 0..<40 { page.addEntry(to: "accounts") }
        #expect(ExtensionModel.entryItems(in: page.draft, at: ["accounts"]).count == 32)
        #expect(Set(page.entryKeys["accounts"] ?? []).count == 32)
    }

    @Test func requiredAndOptionalEmptyListRemovalRules() async throws {
        // 1. Optional list with positive minItems still allows returning to valid absent state ({}).
        do {
            let hub = FakeHub()
            let json = Self.makeDetail(required: false, minItems: 1)
            hub.on("GET", "/api/v1/plugins/example-entries/-/", json: json)
            var sent: JSONValue?
            hub.on("PUT", "/api/v1/plugins/example-entries/-/config") { req in
                sent = try JSON.decoder.decode(JSONValue.self, from: #require(req.body))
                return Data(Self.makeDetail(required: false, minItems: 1, entriesJSON: "[]", secretsJSON: "[]").utf8)
            }
            let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
            await page.load()
            page.removeEntry(from: "accounts", key: "entry_saved")
            #expect(page.dirty && page.canSave)
            #expect(await page.save())
            #expect(sent == .object(["config": .object([:])]))
        }

        // 2. Required list with minItems 0 sends explicit {"accounts": []}.
        do {
            let hub = FakeHub()
            let json = Self.makeDetail(required: true, minItems: 0)
            hub.on("GET", "/api/v1/plugins/example-entries/-/", json: json)
            var sent: JSONValue?
            hub.on("PUT", "/api/v1/plugins/example-entries/-/config") { req in
                sent = try JSON.decoder.decode(JSONValue.self, from: #require(req.body))
                return Data(Self.makeDetail(required: true, minItems: 0, entriesJSON: "[]", secretsJSON: "[]").utf8)
            }
            let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
            await page.load()
            page.removeEntry(from: "accounts", key: "entry_saved")
            #expect(page.dirty && page.canSave)
            #expect(await page.save())
            #expect(sent == .object(["config": .object(["accounts": .array([])])]))
        }

        // 3. Required list at declared minItems 1 refuses removal.
        do {
            let hub = FakeHub()
            hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail(required: true, minItems: 1))
            let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
            await page.load()
            page.removeEntry(from: "accounts", key: "entry_saved")
            #expect(ExtensionModel.entryItems(in: page.draft, at: ["accounts"]).count == 1)
            #expect(!page.dirty)
        }
    }

    @Test func cleanImmediateSaveAdoptsRemoteEntriesAddedElsewhere() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail(maxItems: 4))
        hub.on("PUT", "/api/v1/plugins/example-entries/-/config") { req in
            #expect(req.query["entry_lists"] == "1")
            #expect(req.query["preserve_entries"] == "1")
            // Hub preserved a second entry added elsewhere while applying the machine switch.
            return Data(Self.makeDetail(
                maxItems: 4,
                entriesJSON: #"[{"_id":"entry_saved","name":"One","address":"https://one.example.com"},{"_id":"entry_remote","name":"Remote","address":"https://remote.example.com"}]"#,
                secretsJSON: #"[{"name":"entry_saved","title":"Account key","set":true,"list":"accounts","entry_title":"One"},{"name":"entry_remote","title":"Account key","set":false,"list":"accounts","entry_title":"Remote"}]"#
            ).utf8)
        }
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
        await page.load()
        #expect(!page.dirty)
        await page.saveNow(.array([.string("mac-a")]), at: ["machines"])
        #expect(ExtensionModel.entryItems(in: page.draft, at: ["accounts"]).count == 2)
        #expect(page.entryKeys["accounts"] == ["entry_saved", "entry_remote"])
        #expect(!page.dirty)
    }

    @Test func setEnabledAndRebuildPreserveSavedDraftAndEntryKeys() async throws {
        let hub = FakeHub()
        var enabled = true
        hub.on("GET", "/api/v1/plugins/example-entries/-/") { _ in
            Data(Self.makeDetail(maxItems: 4).replacingOccurrences(of: #""enabled": true"#, with: #""enabled": \#(enabled)"#).utf8)
        }
        hub.on("PUT", "/api/v1/plugins/example-entries/-/enabled") { _ in
            enabled.toggle()
            return Data(Self.makeDetail(maxItems: 4).replacingOccurrences(of: #""enabled": true"#, with: #""enabled": \#(enabled)"#).utf8)
        }

        let drafts = ExtensionDrafts()
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }, drafts: drafts))
        await page.load()
        page.addEntry(to: "accounts")
        let localKey = try #require(page.entryKeys["accounts"]?[1])
        page.set(.string("Unsaved"), at: ["accounts", "1", "name"])
        #expect(page.dirty)

        // Toggling enabled keeps live unsaved edits and their stable entry keys.
        await page.setEnabled(false)
        #expect(page.detail?.plugin.enabled == false)
        #expect(page.value(["accounts", "1", "name"]) == .string("Unsaved"))
        #expect(page.entryKeys["accounts"] == ["entry_saved", localKey])

        // Rebuilding the model after setEnabled restores the saved draft and its entry keys.
        let rebuilt = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }, drafts: drafts))
        await rebuilt.load()
        #expect(rebuilt.detail?.plugin.enabled == false)
        #expect(rebuilt.value(["accounts", "1", "name"]) == .string("Unsaved"))
        #expect(rebuilt.entryKeys["accounts"] == ["entry_saved", localKey])
        #expect(rebuilt.dirty)
    }

    @Test func launcherFieldsInsideEntryListsTriggerLauncherLoading() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail(includeLauncherField: true))
        hub.on("GET", "/api/v1/launchers", json: #"{"launchers":[{"name":"claude","source":"builtin"}]}"#)
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
        await page.load()
        await page.loadLaunchersIfNeeded()
        #expect(page.launchers?.map(\.name) == ["claude"])
    }

    @Test func undrawnInvalidConfigRoutesToFormErrorAndEntryIdErrorRoutesToFieldError() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail())
        var errorField = "accounts[0]._id"
        hub.on("PUT", "/api/v1/plugins/example-entries/-/config") { _ in
            throw APIError(status: 400, message: "bad config", fields: ["code": "invalid_config", "field": errorField, "message": "Refused by hub"])
        }
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
        await page.load()
        page.set(.string("Updated"), at: ["accounts", "0", "name"])

        // Drawn entry `_id` path goes to fieldError.
        #expect(await page.save() == false)
        #expect(page.fieldError(["accounts", "0", "_id"]) == "Refused by hub")
        #expect(page.formError == nil)

        // Undrawn/empty field path goes to formError instead of disappearing.
        errorField = ""
        #expect(await page.save() == false)
        #expect(page.formError == "Refused by hub")
        #expect(page.fieldErrors.isEmpty)
    }

    // MARK: 5. Real Simulator Visual Render Check (Light/Dark × Default/Largest Accessibility Text)

    @Test func entryListSectionRendersLightDarkDefaultAndLargestText() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/plugins/example-entries/-/", json: Self.makeDetail(maxItems: 3))
        let page = ExtensionModel(id: "example-entries", deps: .init(api: { hub.client }))
        await page.load()
        // Add a second unsaved entry so both saved (Set · ends in 1234, enabled Secret) and unsaved (save-first, disabled Secret) states show.
        page.addEntry(to: "accounts")
        let candidate = page.detail?.form.first { $0.isEntryList }
        let listField = try #require(candidate)

        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first {
            $0.activationState == .foregroundActive
        }, "No foreground UIWindowScene in simulator host")
        let shotsDir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"].map { URL(fileURLWithPath: $0, isDirectory: true) }
        if let shotsDir {
            try FileManager.default.createDirectory(at: shotsDir, withIntermediateDirectories: true)
        }

        let matrix: [(name: String, scheme: ColorScheme, style: UIUserInterfaceStyle, size: DynamicTypeSize)] = [
            ("entry-list-light-default", .light, .light, .large),
            ("entry-list-dark-default", .dark, .dark, .large),
            ("entry-list-light-ax5", .light, .light, .accessibility5),
            ("entry-list-dark-ax5", .dark, .dark, .accessibility5),
        ]

        for variant in matrix {
            let window = UIWindow(windowScene: scene)
            window.frame = scene.screen.bounds
            window.overrideUserInterfaceStyle = variant.style
            let host = UIHostingController(rootView: EntryListVisualHost(field: listField, page: page)
                .environment(AppModel.shared)
                .environment(\.colorScheme, variant.scheme)
                .environment(\.dynamicTypeSize, variant.size))
            window.rootViewController = host
            window.makeKeyAndVisible()
            defer { window.isHidden = true }
            for _ in 0..<12 {
                window.layoutIfNeeded()
                try await Task.sleep(for: .milliseconds(25))
            }
            let format = UIGraphicsImageRendererFormat()
            format.scale = window.screen.scale
            let image = UIGraphicsImageRenderer(bounds: window.bounds, format: format).image { _ in
                #expect(window.drawHierarchy(in: window.bounds, afterScreenUpdates: true))
            }
            let png = try #require(image.pngData())
            #expect(png.count > 2000, "\(variant.name) should render non-empty entry-list UI")
            if let shotsDir {
                try png.write(to: shotsDir.appendingPathComponent("\(variant.name).png"))
            }
        }
    }
}

private struct EntryListVisualHost: View {
    var field: ExtensionField
    @Bindable var page: ExtensionModel
    @FocusState private var numberFocus: String?

    var body: some View {
        NavigationStack {
            SettingsForm {
                ExtensionEntryListSection(
                    field: field,
                    page: page,
                    templates: [],
                    numberFocus: $numberFocus,
                    onSecret: { _, _ in },
                    onRequestRemove: { _ in }
                )
            }
            .themedPage()
            .navigationTitle("Example Entries")
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}

private extension Array where Element == LocalRequest {
    func singleQuery(_ key: String) -> String? {
        count == 1 ? first?.query[key] : nil
    }
}
