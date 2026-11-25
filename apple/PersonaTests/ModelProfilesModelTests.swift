import Foundation
import Testing
@testable import Persona
import PersonaWire

@MainActor
struct ModelProfilesModelTests {
    private static let catalogJSON = #"""
    {
      "revision": 4,
      "active": "performance",
      "profiles": [
        {
          "id": "performance",
          "name": "Performance",
          "description": "Fast models",
          "active": true,
          "changes": ["assistant", "gw.small_model"],
          "purposes": {
            "assistant": {"provider": "launcher.gw", "model": "old-model"}
          },
          "launchers": {
            "gw": {"model": "old-model", "small_model": "old-small"}
          },
          "tiers": {
            "strong": {"launcher": "gw", "model": "opus-4", "description": "Hard tasks"}
          }
        },
        {
          "id": "economy",
          "name": "Economy",
          "description": "Daily driver",
          "active": false,
          "changes": ["assistant", "kb", "gw.small_model"],
          "purposes": {
            "assistant": {"provider": "launcher.gw", "model": "sonnet-4"},
            "kb": {}
          },
          "launchers": {
            "gw": {"small_model": ""}
          },
          "tiers": {
            "standard": {"launcher": "gw", "model": "sonnet-4", "speed": "standard"}
          }
        }
      ],
      "events": []
    }
    """#

    private func decodeJSON(_ data: Data) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    // MARK: - Wire serialization & cross-version compatibility

    @Test func captureInputEmitsMinimalPayloadPreservingTiersAndOmittingBlankDescription() throws {
        let withDesc = ModelProfileSaveInput.capture(revision: 7, name: " Performance ", description: " Fast models ")
        let obj = try decodeJSON(JSON.encoder.encode(withDesc))
        #expect(Set(obj.keys) == ["revision", "name", "description", "capture"])
        #expect(obj["revision"] as? Int == 7)
        #expect(obj["name"] as? String == "Performance")
        #expect(obj["description"] as? String == "Fast models")
        #expect(obj["capture"] as? Bool == true)

        let noDesc = ModelProfileSaveInput.capture(revision: 2, name: "Economy", description: "   ")
        let objNoDesc = try decodeJSON(JSON.encoder.encode(noDesc))
        #expect(Set(objNoDesc.keys) == ["revision", "name", "capture"])
        #expect(objNoDesc["description"] == nil)
        #expect(objNoDesc["purposes"] == nil)
        #expect(objNoDesc["launchers"] == nil)
        #expect(objNoDesc["tiers"] == nil)
        #expect(objNoDesc["capture"] as? Bool == true)
    }

    @Test func saveInputOmitsDefaultStandardSpeedEmptyPermissionModeAndRuntimeForOlderHubs() throws {
        let profile = ModelProfile(
            id: "compat",
            name: "Compat",
            purposes: [
                "assistant": ModelBinding(provider: "launcher.claude", model: "sonnet-4", effort: "", runtime: "cli", permissionMode: ""),
                "kb": ModelBinding(),
            ],
            launchers: [
                "gw": ModelProfileLauncherModels(model: "sonnet-4", smallModel: ""),
            ],
            tiers: [
                "standard": ModelProfileTier(launcher: "claude", model: "sonnet-4", speed: "standard", permissionMode: ""),
                "quick": ModelProfileTier(launcher: "claude", speed: "fast", permissionMode: "plan"),
            ]
        )
        let draft = ModelProfilesLogic.profileToDraft(profile)
        let input = ModelProfilesLogic.draftToSaveInput(draft, revision: 4)
        let encoded = try JSON.encoder.encode(input)
        let obj = try decodeJSON(encoded)

        #expect(obj["capture"] == nil)
        let purposes = try #require(obj["purposes"] as? [String: [String: Any]])
        #expect(purposes["assistant"]?["provider"] as? String == "launcher.claude")
        #expect(purposes["assistant"]?["model"] as? String == "sonnet-4")
        #expect(purposes["assistant"]?["permission_mode"] == nil)
        #expect(purposes["assistant"]?["runtime"] == nil)
        #expect(purposes["assistant"]?["effort"] == nil)
        #expect(purposes["kb"]?.isEmpty == true, "empty binding stays {} to mean follow default")

        let launchers = try #require(obj["launchers"] as? [String: [String: Any]])
        #expect(launchers["gw"]?["model"] as? String == "sonnet-4")
        #expect(launchers["gw"]?["small_model"] as? String == "", "empty string explicitly clears launcher field on switch")
        #expect(launchers["gw"]?["subagent_model"] == nil)

        let tiers = try #require(obj["tiers"] as? [String: [String: Any]])
        #expect(tiers["standard"]?["launcher"] as? String == "claude")
        #expect(tiers["standard"]?["model"] as? String == "sonnet-4")
        #expect(tiers["standard"]?["speed"] == nil, "default standard speed must be omitted for older hubs")
        #expect(tiers["standard"]?["permission_mode"] == nil)
        #expect(tiers["quick"]?["speed"] as? String == "fast")
        #expect(tiers["quick"]?["permission_mode"] as? String == "plan")

        // Also check direct ModelProfileTier encoding omits standard speed even when constructed directly.
        let directTier = try decodeJSON(JSON.encoder.encode(ModelProfileTier(launcher: "claude", speed: "standard", permissionMode: "  ")))
        #expect(directTier["speed"] == nil)
        #expect(directTier["permission_mode"] == nil)
    }

    @Test func olderHubPermissionModeVisibilityHidesEmptyControlsButKeepsExistingValues() {
        let olderHubPurposes = [
            ModelPurpose(id: "assistant", name: "Main Chat", group: "Default", kind: "launcher",
                         override: ModelBinding(), effective: ModelBinding(), revision: 1,
                         fields: ["provider", "model", "effort"]),
            ModelPurpose(id: "agent.new", name: "Agents", group: "Default", kind: "launcher",
                         override: ModelBinding(), effective: ModelBinding(), revision: 1,
                         fields: ["provider", "model"]),
        ]
        #expect(!ModelProfileRules.hubSupportsPermissionMode(purposes: olderHubPurposes))
        #expect(!ModelProfileRules.showsPurposePermissionMode(
            purposeID: "assistant", currentValue: "", catalogPurpose: olderHubPurposes[0], hubSupportsPermissionMode: false))
        #expect(ModelProfileRules.showsPurposePermissionMode(
            purposeID: "assistant", currentValue: "plan", catalogPurpose: olderHubPurposes[0], hubSupportsPermissionMode: false))
        #expect(!ModelProfileRules.showsTierPermissionMode(currentValue: "", hubSupportsPermissionMode: false))
        #expect(ModelProfileRules.showsTierPermissionMode(currentValue: "auto", hubSupportsPermissionMode: false))

        let newerHubPurposes = [
            ModelPurpose(id: "assistant", name: "Main Chat", group: "Default", kind: "launcher",
                         override: ModelBinding(), effective: ModelBinding(), revision: 1,
                         fields: ["provider", "model", "effort", "permission_mode"]),
        ]
        #expect(ModelProfileRules.hubSupportsPermissionMode(purposes: newerHubPurposes))
        #expect(ModelProfileRules.showsPurposePermissionMode(
            purposeID: "assistant", currentValue: "", catalogPurpose: newerHubPurposes[0], hubSupportsPermissionMode: true))
        #expect(ModelProfileRules.showsTierPermissionMode(currentValue: "", hubSupportsPermissionMode: true))
        #expect(!ModelProfileRules.showsPurposePermissionMode(
            purposeID: "kb", currentValue: "", catalogPurpose: nil, hubSupportsPermissionMode: true))
    }

    // MARK: - Drift parsing, summarization & confirmation gating

    @Test func parsesDottedCategoryIDsAndDottedLauncherFieldChangesUnambiguously() {
        #expect(ModelProfilesLogic.parseChange("assistant") == .purpose(id: "assistant"))
        #expect(ModelProfilesLogic.parseChange("agent.new") == .purpose(id: "agent.new"))
        #expect(ModelProfilesLogic.parseChange("inbox.triage") == .purpose(id: "inbox.triage"))
        #expect(ModelProfilesLogic.parseChange("agent.delegate") == .purpose(id: "agent.delegate"))
        #expect(ModelProfilesLogic.parseChange("translate") == .purpose(id: "translate"))
        #expect(ModelProfilesLogic.parseChange("gw.small_model") == .launcher(launcher: "gw", field: "small_model"))
        #expect(ModelProfilesLogic.parseChange("relay.example.com.effort") == .launcher(launcher: "relay.example.com", field: "effort"))
        #expect(ModelProfilesLogic.parseChange("custom.unknown_field") == .other(raw: "custom.unknown_field"))
        #expect(ModelProfilesLogic.parseChange("extension.custom", knownPurposeIDs: ["extension.custom"]) == .purpose(id: "extension.custom"))
    }

    @Test func computesApplyDriftAndConfirmationRequirementAccurately() {
        let cleanPerf = ModelProfile(id: "performance", name: "Performance", active: true, changes: [])
        let cleanEcon = ModelProfile(id: "economy", name: "Economy", active: false, changes: [])
        let driftedPerf = ModelProfile(id: "performance", name: "Performance", active: true, changes: ["kb"])
        let driftedEcon = ModelProfile(id: "economy", name: "Economy", active: false, changes: ["assistant"])

        let zeroDrift = ModelProfilesLogic.applyDrift(target: cleanEcon, active: cleanPerf)
        #expect(!zeroDrift.hasDrift)
        #expect(!zeroDrift.hasActiveDrift)
        #expect(zeroDrift.canCaptureActiveFirst)
        #expect(!ModelProfilesLogic.shouldConfirmApply(target: cleanEcon, active: cleanPerf))

        let bothDrifted = ModelProfilesLogic.applyDrift(target: driftedEcon, active: driftedPerf)
        #expect(bothDrifted.targetChanges == ["assistant"])
        #expect(bothDrifted.activeDrift == ["kb"])
        #expect(bothDrifted.hasDrift && bothDrifted.hasActiveDrift && bothDrifted.canCaptureActiveFirst)
        #expect(ModelProfilesLogic.shouldConfirmApply(target: driftedEcon, active: driftedPerf))

        let activeDriftOnly = ModelProfilesLogic.applyDrift(target: cleanEcon, active: driftedPerf)
        #expect(activeDriftOnly.targetChanges.isEmpty)
        #expect(activeDriftOnly.activeDrift == ["kb"])
        #expect(activeDriftOnly.hasDrift && activeDriftOnly.hasActiveDrift && activeDriftOnly.canCaptureActiveFirst)
        #expect(ModelProfilesLogic.shouldConfirmApply(target: cleanEcon, active: driftedPerf))

        let reapplyDriftedActive = ModelProfilesLogic.applyDrift(target: driftedPerf, active: driftedPerf)
        #expect(reapplyDriftedActive.targetChanges == ["kb"])
        #expect(reapplyDriftedActive.activeDrift.isEmpty)
        #expect(reapplyDriftedActive.hasDrift && !reapplyDriftedActive.hasActiveDrift && !reapplyDriftedActive.canCaptureActiveFirst)

        let noActive = ModelProfilesLogic.applyDrift(target: driftedEcon, active: nil)
        #expect(noActive.hasDrift && !noActive.hasActiveDrift && !noActive.canCaptureActiveFirst)
    }

    @Test func describesDriftedPurposesAndLauncherOverridesWithTargetValues() {
        let providers = [ModelProvider(id: "launcher.gw", name: "Team gateway", kind: "launcher", agent: "claude")]
        let purposes = [
            ModelPurpose(id: "assistant", name: "Main Chat", group: "Default", kind: "launcher",
                         override: ModelBinding(), effective: ModelBinding(), revision: 1),
            ModelPurpose(id: "kb", name: "Knowledge", group: "Default", kind: "api",
                         override: ModelBinding(), effective: ModelBinding(), revision: 1),
        ]
        let economy = ModelProfile(
            id: "economy",
            name: "Economy",
            purposes: [
                "assistant": ModelBinding(provider: "launcher.gw", model: "sonnet-4", effort: "high"),
                "kb": ModelBinding(),
            ],
            launchers: ["gw": ModelProfileLauncherModels(smallModel: "", effort: "low")]
        )

        let assistantChange = ModelProfilesLogic.describeChange("assistant", profile: economy, purposes: purposes, providers: providers)
        #expect(assistantChange.label == "Main Chat")
        #expect(assistantChange.target?.contains("Team gateway") == true)
        #expect(assistantChange.target?.contains("sonnet-4") == true)

        let kbChange = ModelProfilesLogic.describeChange("kb", profile: economy, purposes: purposes, providers: providers)
        #expect(kbChange.label == "Knowledge")
        #expect(kbChange.target == String(localized: "Follow default [Model Profiles]"))

        let clearChange = ModelProfilesLogic.describeChange("gw.small_model", profile: economy, purposes: purposes, providers: providers)
        #expect(clearChange.label.contains("Team gateway"))
        #expect(clearChange.target == String(localized: "Clear (use default) [Model Profiles]"))

        let activeOnly = ModelProfilesLogic.describeChange("gw.effort", profile: nil, purposes: purposes, providers: providers)
        #expect(activeOnly.label.contains("Team gateway"))
        #expect(activeOnly.target == nil)
    }

    // MARK: - Draft validation & dirty detection

    @Test func validatesProfileDraftAndAllowsUnchangedLegacyTierModels() {
        var draft = ModelProfileDraft(
            name: "  ",
            description: String(repeating: "a", count: 301),
            purposeOrder: ["assistant"],
            purposes: ["assistant": ModelProfilePurposeDraft(included: true, effort: "bogus", permissionMode: "invalid")],
            tiers: [
                ModelProfileTierDraft(id: "1", name: "BadName", launcher: "", model: "bad model!", speed: "warp"),
                ModelProfileTierDraft(id: "2", name: "fast", launcher: "claude", model: "sonnet-4"),
                ModelProfileTierDraft(id: "3", name: "fast", launcher: "claude", model: "sonnet-4"),
            ]
        )
        let problems = ModelProfilesLogic.draftProblems(draft)
        #expect(problems.count >= 7)

        // Legacy tier model without spaces that fails ModelID stays valid until edited, matching server checkTierModel.
        let legacyProfile = ModelProfile(
            id: "legacy",
            name: "Legacy",
            tiers: ["old": ModelProfileTier(launcher: "claude", model: "legacy?model", speed: "standard")]
        )
        draft = ModelProfilesLogic.profileToDraft(legacyProfile)
        #expect(ModelProfilesLogic.draftProblems(draft).isEmpty)
        #expect(!ModelProfilesLogic.isDraftDirty(draft, original: ModelProfilesLogic.profileToDraft(legacyProfile)))
        draft.tiers[0].model = "still?invalid"
        #expect(!ModelProfilesLogic.draftProblems(draft).isEmpty)
        draft.tiers[0].model = "claude-sonnet-4-5"
        #expect(ModelProfilesLogic.draftProblems(draft).isEmpty)
        #expect(ModelProfilesLogic.isDraftDirty(draft, original: ModelProfilesLogic.profileToDraft(legacyProfile)))
    }

    // MARK: - ModelProfilesModel flows via FakeHub

    @Test func olderHub404MarksProfilesUnsupportedWithoutError() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/model-profiles") { _ in throw APIError(status: 404, message: "not found") }
        let model = ModelProfilesModel(deps: .init(api: { hub.client }))
        let result = await model.load()
        #expect(result == nil)
        #expect(model.loaded && model.unsupported && model.catalog == nil && model.error == nil)
    }

    @Test func captureFromCurrentSendsCaptureTrueHandles409ConflictAndRefreshesSyncKey() async throws {
        let hub = FakeHub()
        var revision: Int64 = 4
        var failConflict = true
        var updates = 0
        var capturedBodies: [[String: Any]] = []

        func currentJSON(changes: [String], model: String) -> String {
            #"""
            {
              "revision": \#(revision),
              "active": "performance",
              "profiles": [
                {
                  "id": "performance",
                  "name": "Performance",
                  "description": "Fast models",
                  "active": true,
                  "changes": \#(changes.isEmpty ? "[]" : #"["assistant","gw.small_model"]"#),
                  "purposes": {"assistant": {"provider": "launcher.gw", "model": "\#(model)"}},
                  "tiers": {"strong": {"launcher": "gw", "model": "opus-4", "description": "Hard tasks"}}
                }
              ],
              "events": []
            }
            """#
        }

        hub.on("GET", "/api/v1/model-profiles") { _ in
            Data(currentJSON(changes: ["assistant", "gw.small_model"], model: "old-model").utf8)
        }
        hub.on("PUT", "/api/v1/model-profiles/performance") { req in
            let body = try self.decodeJSON(try #require(req.body))
            capturedBodies.append(body)
            if failConflict {
                failConflict = false
                revision = 5
                throw APIError(status: 409, message: "conflict")
            }
            revision += 1
            return Data(currentJSON(changes: [], model: "live-model").utf8)
        }

        let model = ModelProfilesModel(deps: .init(api: { hub.client }, updated: { updates += 1 }))
        await model.load()
        let initialSync = model.syncKey
        let profile = try #require(model.profile(id: "performance"))

        // First capture hits 409 Conflict -> conflict flag set, error surfaced, syncKey unchanged.
        #expect(await model.capture(profile) == false)
        #expect(model.conflict && model.actionError != nil && model.syncKey == initialSync)
        #expect(capturedBodies.count == 1)
        #expect(Set(capturedBodies[0].keys) == ["revision", "name", "description", "capture"])
        #expect(capturedBodies[0]["revision"] as? Int == 4)
        #expect(capturedBodies[0]["capture"] as? Bool == true)

        // Reload fetches revision 5, clears conflict, then retry capture succeeds.
        await model.load()
        #expect(!model.conflict && model.catalog?.revision == 5)
        let syncBeforeRetry = model.syncKey
        #expect(await model.capture(try #require(model.profile(id: "performance"))) == true)
        #expect(capturedBodies.count == 2)
        #expect(capturedBodies[1]["revision"] as? Int == 5)
        #expect(capturedBodies[1]["capture"] as? Bool == true)
        #expect(model.catalog?.revision == 6)
        #expect(model.profile(id: "performance")?.changes.isEmpty == true)
        #expect(model.profile(id: "performance")?.purposes["assistant"]?.model == "live-model")
        #expect(model.profile(id: "performance")?.tiers["strong"]?.model == "opus-4")
        #expect(model.syncKey == syncBeforeRetry + 1)
        #expect(updates == 1)
    }

    @Test func captureActiveAndApplyCapturesActiveFirstThenAppliesTarget() async throws {
        let hub = FakeHub()
        var callLog: [String] = []
        var revision: Int64 = 4
        var active = "performance"
        var perfChanges = #"["assistant","gw.small_model"]"#
        var updates = 0

        func snapshot() -> Data {
            let json = #"""
            {
              "revision": \#(revision),
              "active": "\#(active)",
              "profiles": [
                {"id": "performance", "name": "Performance", "description": "Fast models", "active": \#(active == "performance"), "changes": \#(perfChanges), "tiers": {}},
                {"id": "economy", "name": "Economy", "description": "Daily driver", "active": \#(active == "economy"), "changes": \#(active == "economy" ? "[]" : #"["assistant"]"#), "tiers": {}}
              ],
              "events": []
            }
            """#
            return Data(json.utf8)
        }

        hub.on("GET", "/api/v1/model-profiles") { _ in snapshot() }
        hub.on("PUT", "/api/v1/model-profiles/performance") { req in
            let body = try self.decodeJSON(try #require(req.body))
            #expect(body["capture"] as? Bool == true)
            #expect(body["revision"] as? Int == 4)
            #expect(body["name"] as? String == "Performance")
            #expect(body["description"] as? String == "Fast models")
            callLog.append("PUT performance")
            revision += 1
            perfChanges = "[]"
            return snapshot()
        }
        hub.on("POST", "/api/v1/model-profiles/economy/apply") { _ in
            callLog.append("POST economy/apply")
            active = "economy"
            return snapshot()
        }

        let model = ModelProfilesModel(deps: .init(api: { hub.client }, updated: { updates += 1 }))
        await model.load()
        let perf = try #require(model.profile(id: "performance"))
        let econ = try #require(model.profile(id: "economy"))

        let result = await model.captureActiveAndApply(active: perf, target: econ)
        #expect(result == .applied)
        #expect(callLog == ["PUT performance", "POST economy/apply"])
        #expect(model.catalog?.active == "economy")
        #expect(model.profile(id: "performance")?.changes.isEmpty == true)
        #expect(model.actionError == nil && !model.busy)
        #expect(updates == 1)
    }

    @Test func captureActiveAndApplyAbortsApplyWhenCaptureFails() async throws {
        let hub = FakeHub()
        var applyCalled = false
        hub.on("GET", "/api/v1/model-profiles", json: Self.catalogJSON)
        hub.on("PUT", "/api/v1/model-profiles/performance") { _ in
            throw APIError(status: 400, message: "Capture rejected by hub")
        }
        hub.on("POST", "/api/v1/model-profiles/economy/apply") { _ in
            applyCalled = true
            return Data(Self.catalogJSON.utf8)
        }

        let model = ModelProfilesModel(deps: .init(api: { hub.client }))
        await model.load()
        let perf = try #require(model.profile(id: "performance"))
        let econ = try #require(model.profile(id: "economy"))

        let result = await model.captureActiveAndApply(active: perf, target: econ)
        #expect(result == .captureFailed)
        #expect(!applyCalled, "apply must be aborted when capture step fails")
        #expect(model.actionError?.contains("Capture rejected by hub") == true)
        #expect(model.catalog?.active == "performance")
        #expect(!model.busy)
    }

    @Test func captureActiveAndApplyRetainsCapturedStateWhenApplyFails() async throws {
        let hub = FakeHub()
        var updates = 0
        hub.on("GET", "/api/v1/model-profiles", json: Self.catalogJSON)
        hub.on("PUT", "/api/v1/model-profiles/performance") { _ in
            let capturedJSON = #"""
            {
              "revision": 5,
              "active": "performance",
              "profiles": [
                {"id": "performance", "name": "Performance", "description": "Fast models", "active": true, "changes": [], "tiers": {}},
                {"id": "economy", "name": "Economy", "description": "Daily driver", "active": false, "changes": ["assistant"], "tiers": {}}
              ],
              "events": []
            }
            """#
            return Data(capturedJSON.utf8)
        }
        hub.on("POST", "/api/v1/model-profiles/economy/apply") { _ in
            throw APIError(status: 400, message: "Target provider missing")
        }

        let model = ModelProfilesModel(deps: .init(api: { hub.client }, updated: { updates += 1 }))
        await model.load()
        let perf = try #require(model.profile(id: "performance"))
        let econ = try #require(model.profile(id: "economy"))

        let result = await model.captureActiveAndApply(active: perf, target: econ)
        #expect(result == .applyFailedAfterCapture)
        #expect(model.catalog?.revision == 5, "captured revision must be retained even when apply fails")
        #expect(model.profile(id: "performance")?.changes.isEmpty == true, "captured active profile must stay synced")
        #expect(model.actionError?.contains("Performance") == true)
        #expect(model.actionError?.contains("Target provider missing") == true)
        #expect(updates == 1)
    }

    @Test func directApplyRollbackAndSaveRespectManagerOwnerScoping() async throws {
        for (owner, expectedManager) in [(ModelSettingsOwner.defaultAssistant, nil), (.current, "work")] as [(ModelSettingsOwner, String?)] {
            let hub = FakeHub()
            hub.on("GET", "/api/v1/model-profiles", json: Self.catalogJSON)
            hub.on("POST", "/api/v1/model-profiles/economy/apply", json: Self.catalogJSON)
            hub.on("POST", "/api/v1/model-profiles/rollback", json: Self.catalogJSON)
            hub.on("PUT", "/api/v1/model-profiles/economy", json: Self.catalogJSON)

            let model = ModelProfilesModel(deps: .init(api: { hub.client.showing("work") }))
            model.owner = owner
            await model.load()
            #expect(await model.apply(id: "economy"))
            #expect(await model.rollback())
            #expect(await model.save(id: "economy", input: ModelProfileSaveInput(revision: 4, name: "Economy")))
            #expect(hub.requests.count == 4)
            #expect(hub.requests.allSatisfy { $0.headers[APIClient.managerHeader] == expectedManager })
        }
    }

    @Test func saveConflictPreservesConflictStateAndSurfacesAdjacentError() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/model-profiles", json: Self.catalogJSON)
        hub.on("PUT", "/api/v1/model-profiles/economy") { _ in
            throw APIError(status: 409, message: "conflict")
        }
        let model = ModelProfilesModel(deps: .init(api: { hub.client }))
        await model.load()
        let saved = await model.save(id: "economy", input: ModelProfileSaveInput(revision: 4, name: "Economy v2"))
        #expect(!saved)
        #expect(model.conflict)
        #expect(model.displayedError != nil)
        #expect(!model.busy)

        await model.load()
        #expect(!model.conflict && model.error == nil)
    }
}
