package persona.feature.settings

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.hub.HubError
import persona.core.hub.HubFailure

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2200dp-xhdpi")
class ModelProfilesScreenTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private val providers = listOf(
        ModelProvider("launcher.claude", "Claude Code", kind = "launcher"),
        ModelProvider("api.fast", "Fast API", kind = "api", api = "responses"),
    )
    private val purposes = listOf(
        ModelPurpose("assistant", "Main chat", "Everyday uses", effective = ModelBinding(provider = "launcher.claude", model = "opus")),
        ModelPurpose("agent.new", "New agent", "Agents", effective = ModelBinding(provider = "launcher.claude", model = "sonnet")),
    )

    @Test fun profilesSectionSwitchesEditsAndRollsBack() {
        var currentView = ModelProfilesView(
            revision = 2,
            active = "performance",
            rollback = ModelProfileUndo(to = "economy", changes = listOf("assistant")),
            profiles = listOf(
                ModelProfile(
                    id = "performance",
                    name = "Performance",
                    description = "Strong models for complex tasks",
                    purposes = mapOf("assistant" to ModelBinding(provider = "launcher.claude", model = "opus", effort = "high")),
                    tiers = mapOf("strong" to ModelProfileTier(launcher = "claude", model = "opus", effort = "high", description = "Complex work")),
                    active = true,
                    changes = emptyList(),
                ),
                ModelProfile(
                    id = "economy",
                    name = "Economy",
                    description = "Lower cost models",
                    purposes = mapOf("assistant" to ModelBinding(provider = "launcher.claude", model = "sonnet", effort = "medium")),
                    tiers = mapOf("standard" to ModelProfileTier(launcher = "claude", model = "sonnet", description = "Routine work")),
                    active = false,
                    changes = listOf("assistant"),
                ),
            ),
        )
        val applied = mutableListOf<String>()
        var rollbacks = 0
        val savedInputs = mutableListOf<Pair<String, ModelProfileSaveInput>>()
        var catalogReads = 0

        val api = object : ModelConfiguration {
            override suspend fun catalog(): ModelCatalog {
                catalogReads++
                return ModelCatalog(purposes, providers)
            }
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles(): ModelProfilesView = currentView
            override suspend fun applyProfile(id: String): ModelProfilesView {
                applied += id
                currentView = currentView.copy(
                    active = id,
                    profiles = currentView.profiles.map { it.copy(active = it.id == id) },
                )
                return currentView
            }
            override suspend fun rollbackProfile(): ModelProfilesView {
                rollbacks++
                currentView = currentView.copy(active = "economy", rollback = null)
                return currentView
            }
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                savedInputs += id to input
                currentView = currentView.copy(
                    revision = currentView.revision + 1,
                    profiles = currentView.profiles.map { if (it.id == id) it.copy(name = input.name, description = input.description) else it },
                )
                return ModelProfileSave.Saved(currentView)
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        // Profiles and Rollback are rendered
        rule.onNodeWithTag("model-profile-row-performance").assertIsDisplayed()
        rule.onNodeWithTag("model-profile-row-economy").assertIsDisplayed()
        rule.onNodeWithTag("model-profile-rollback").assertIsDisplayed()

        // Open Economy profile detail, inspect, then edit and save
        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("standard").assertIsDisplayed()
        rule.onNodeWithTag("model-profile-edit-button").performClick()
        rule.waitForIdle()

        rule.onNode(hasSetTextAction() and hasText("Economy")).performTextReplacement("Economy Plus")
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-save-button").performClick()
        rule.waitForIdle()

        assertEquals(1, savedInputs.size)
        assertEquals("economy", savedInputs.single().first)
        assertEquals("Economy Plus", savedInputs.single().second.name)
        assertEquals(2L, savedInputs.single().second.revision)

        // Apply Economy Plus profile from detail page (drift exists -> confirm -> apply directly)
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()
        assertTrue("should wait for confirmation when drift exists", applied.isEmpty())
        rule.onNodeWithTag("model-profile-apply-confirm").assertIsDisplayed()
        rule.onNodeWithTag("model-profile-apply-drift-list").assertIsDisplayed()
        rule.onNode(hasText("Main chat") and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNodeWithTag("model-profile-apply-direct").performClick()
        rule.waitForIdle()
        assertEquals(listOf("economy"), applied)
        assertTrue("catalog should refresh after save/apply", catalogReads >= 3)

        // Trigger rollback from main page
        rule.onAllNodesWithText(rule.activity.getString(R.string.settings_model_profiles_rollback)).onLast().assertIsDisplayed().performClick()
        rule.waitForIdle()
        assertEquals(1, rollbacks)
    }

    @Test fun appliesDirectlyWithoutConfirmationWhenZeroDrift() {
        var currentView = ModelProfilesView(
            revision = 2,
            active = "performance",
            profiles = listOf(
                ModelProfile(id = "performance", name = "Performance", active = true, changes = emptyList()),
                ModelProfile(id = "economy", name = "Economy", active = false, changes = emptyList()),
            ),
        )
        val applied = mutableListOf<String>()
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles(): ModelProfilesView = currentView
            override suspend fun applyProfile(id: String): ModelProfilesView {
                applied += id
                currentView = currentView.copy(
                    active = id,
                    profiles = currentView.profiles.map { it.copy(active = it.id == id) },
                )
                return currentView
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()

        assertEquals(listOf("economy"), applied)
        rule.onNodeWithTag("model-profile-apply-confirm").assertDoesNotExist()
        rule.onNodeWithTag("model-profile-row-economy").assertIsDisplayed()
    }

    @Test fun detailPageShowsNamedDriftCapturesFromCurrentAndRecoversFrom409() {
        var revision = 4L
        var failFirstConflict = true
        val savedInputs = mutableListOf<Pair<String, ModelProfileSaveInput>>()
        val gwProviders = listOf(
            ModelProvider("launcher.gw", "Team gateway", kind = "launcher"),
        )
        var perfProfile = ModelProfile(
            id = "performance",
            name = "Performance",
            description = "Fast models",
            active = true,
            changes = listOf("assistant", "gw.small_model"),
            purposes = mapOf("assistant" to ModelBinding(provider = "launcher.gw", model = "old-model")),
            launchers = mapOf("gw" to ModelProfileLauncherModels(model = "old-model", smallModel = "old-small")),
            tiers = mapOf("strong" to ModelProfileTier(launcher = "gw", model = "opus-4", description = "Hard tasks")),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, gwProviders)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, gwProviders)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = ModelProfilesView(revision = revision, active = "performance", profiles = listOf(perfProfile))
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                savedInputs += id to input
                if (failFirstConflict) {
                    failFirstConflict = false
                    revision = 5L
                    return ModelProfileSave.Conflict
                }
                revision++
                perfProfile = perfProfile.copy(
                    changes = emptyList(),
                    purposes = mapOf("assistant" to ModelBinding(provider = "launcher.gw", model = "live-model")),
                    launchers = mapOf("gw" to ModelProfileLauncherModels(model = "live-model", smallModel = "live-small")),
                )
                return ModelProfileSave.Saved(
                    ModelProfilesView(revision = revision, active = "performance", profiles = listOf(perfProfile)),
                )
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-performance").performClick()
        rule.waitForIdle()

        // Named drift fields appear directly on the detail screen.
        rule.onNodeWithTag("model-profile-changes").assertIsDisplayed()
        rule.onNode(hasText("Main chat") and hasAnyAncestor(hasTestTag("model-profile-changes"))).assertIsDisplayed()
        rule.onNode(hasText("Team gateway · Small model") and hasAnyAncestor(hasTestTag("model-profile-changes"))).assertIsDisplayed()

        // First capture hits 409 Conflict -> pinned error and Reload button sit inside detail footer.
        rule.onNodeWithTag("model-profile-capture-button").performClick()
        rule.waitForIdle()
        assertEquals(1, savedInputs.size)
        assertEquals("performance", savedInputs[0].first)
        assertEquals(ModelProfileSaveInput.capture(4L, "Performance", "Fast models"), savedInputs[0].second)
        rule.onNode(hasTestTag("model-profile-action-error") and hasAnyAncestor(hasTestTag("model-profile-detail-footer"))).assertIsDisplayed()
        rule.onNode(hasTestTag("model-profile-reload-button") and hasAnyAncestor(hasTestTag("model-profile-detail-footer"))).performClick()
        rule.waitForIdle()

        // Retry capture after reload succeeds at revision 5, clears drift, refreshes detail, and preserves tiers.
        rule.onNodeWithTag("model-profile-capture-button").performClick()
        rule.waitForIdle()
        assertEquals(2, savedInputs.size)
        assertEquals(5L, savedInputs[1].second.revision)
        assertTrue(savedInputs[1].second.capture)
        rule.onNodeWithTag("model-profile-changes").assertDoesNotExist()
        rule.onNodeWithText("Team gateway · live-model", substring = true).assertIsDisplayed()
        rule.onNodeWithText("Small model: live-small", substring = true).assertIsDisplayed()
        rule.onNodeWithText("strong").assertIsDisplayed()
    }

    @Test fun preApplyDriftPreviewCapturesActiveFirstAndHandlesCancel() {
        val calls = mutableListOf<String>()
        var revision = 2L
        var active = "performance"
        var perfChanges = listOf("assistant", "agent.delegate")
        val gwProviders = listOf(ModelProvider("launcher.gw", "Team gateway", kind = "launcher"))
        val richPurposes = listOf(
            ModelPurpose("assistant", "Main chat", "Default", kind = "launcher"),
            ModelPurpose("kb", "Information processing", "Default", kind = "api"),
            ModelPurpose("agent.delegate", "Answering agents' questions", "Default", kind = "api"),
        )
        fun snapshot() = ModelProfilesView(
            revision = revision,
            active = active,
            profiles = listOf(
                ModelProfile(
                    id = "performance",
                    name = "Performance",
                    description = "Max quality",
                    active = active == "performance",
                    changes = perfChanges,
                    purposes = mapOf("assistant" to ModelBinding(provider = "launcher.gw", model = "opus-4")),
                ),
                ModelProfile(
                    id = "economy",
                    name = "Economy",
                    description = "Daily driver",
                    active = active == "economy",
                    changes = if (active == "economy") emptyList() else listOf("assistant", "kb", "gw.small_model"),
                    purposes = mapOf(
                        "assistant" to ModelBinding(provider = "launcher.gw", model = "sonnet-4"),
                        "kb" to ModelBinding(),
                    ),
                    launchers = mapOf("gw" to ModelProfileLauncherModels(smallModel = "")),
                ),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(richPurposes, gwProviders)
            override suspend fun purpose(id: String) = ModelPurposeDetail(richPurposes.first { it.id == id }, gwProviders)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = snapshot()
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                calls += "PUT:$id:capture=${input.capture}:rev=${input.revision}"
                revision++
                perfChanges = emptyList()
                return ModelProfileSave.Saved(snapshot())
            }
            override suspend fun applyProfile(id: String): ModelProfilesView {
                calls += "APPLY:$id"
                active = id
                return snapshot()
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()

        // Confirmation lists target changes and active drift without writing yet.
        assertTrue(calls.isEmpty())
        rule.onNodeWithTag("model-profile-apply-confirm").assertIsDisplayed()
        rule.onNode(hasText("Main chat") and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("sonnet-4", substring = true) and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("Information processing") and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("Follow default", substring = true) and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("Team gateway · Small model") and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("Clear to default", substring = true) and hasAnyAncestor(hasTestTag("model-profile-apply-drift-list"))).assertIsDisplayed()
        rule.onNode(hasText("Main chat: Team gateway · opus-4") and hasAnyAncestor(hasTestTag("model-profile-active-drift-notice"))).assertIsDisplayed()
        rule.onNode(hasText("Answering agents' questions") and hasAnyAncestor(hasTestTag("model-profile-active-drift-notice"))).assertIsDisplayed()

        // Cancel returns to detail page cleanly with zero writes.
        rule.onNodeWithTag("model-profile-confirm-cancel").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-confirm").assertDoesNotExist()
        assertTrue(calls.isEmpty())

        // Re-open confirm and capture active profile first, then apply target.
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-capture-and-apply").performClick()
        rule.waitForIdle()
        assertEquals(listOf("PUT:performance:capture=true:rev=2", "APPLY:economy"), calls)
        rule.onNodeWithTag("model-profile-apply-confirm").assertDoesNotExist()
    }

    @Test fun captureFirstFailureAbortsApplyAndPartialFailureKeepsCapturedState() {
        var failCapture = true
        val calls = mutableListOf<String>()
        var revision = 3L
        var perfChanges = listOf("assistant")
        fun snapshot() = ModelProfilesView(
            revision = revision,
            active = "performance",
            profiles = listOf(
                ModelProfile(id = "performance", name = "Performance", active = true, changes = perfChanges),
                ModelProfile(id = "economy", name = "Economy", active = false, changes = listOf("assistant")),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = snapshot()
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                calls += "PUT:$id"
                if (failCapture) {
                    failCapture = false
                    throw HubFailure(HubError.Server, "Capture rejected by hub", 400)
                }
                revision++
                perfChanges = emptyList()
                return ModelProfileSave.Saved(snapshot())
            }
            override suspend fun applyProfile(id: String): ModelProfilesView {
                calls += "APPLY:$id"
                throw HubFailure(HubError.Server, "Target provider missing", 400)
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()

        // Step 1 fails -> apply is aborted and error sits in pinned confirm footer.
        rule.onNodeWithTag("model-profile-capture-and-apply").performClick()
        rule.waitForIdle()
        assertEquals(listOf("PUT:performance"), calls)
        rule.onNode(
            hasTestTag("model-profile-action-error") and
                hasText("Capture rejected by hub", substring = true) and
                hasAnyAncestor(hasTestTag("model-profile-confirm-footer")),
        ).assertIsDisplayed()

        // Step 1 succeeds, step 2 fails -> active drift notice disappears and error explains apply failed after capture.
        rule.onNodeWithTag("model-profile-capture-and-apply").performClick()
        rule.waitForIdle()
        assertEquals(listOf("PUT:performance", "PUT:performance", "APPLY:economy"), calls)
        rule.onNodeWithTag("model-profile-active-drift-notice").assertDoesNotExist()
        rule.onNode(
            hasTestTag("model-profile-action-error") and
                hasText("Performance", substring = true) and
                hasText("Target provider missing", substring = true) and
                hasAnyAncestor(hasTestTag("model-profile-confirm-footer")),
        ).assertIsDisplayed()
    }

    @Test fun reapplyingDriftedActiveProfileOffersUpdateFromCurrent() {
        val calls = mutableListOf<String>()
        var revision = 3L
        var perfChanges = listOf("assistant")
        fun snapshot() = ModelProfilesView(
            revision = revision,
            active = "performance",
            profiles = listOf(
                ModelProfile(id = "performance", name = "Performance", active = true, changes = perfChanges),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = snapshot()
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                calls += "PUT:$id:capture=${input.capture}"
                revision++
                perfChanges = emptyList()
                return ModelProfileSave.Saved(snapshot())
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-performance").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-confirm-capture").performClick()
        rule.waitForIdle()

        assertEquals(listOf("PUT:performance:capture=true"), calls)
        rule.onNodeWithTag("model-profile-apply-confirm").assertDoesNotExist()
        rule.onNodeWithTag("model-profile-changes").assertDoesNotExist()
    }

    @Test fun editorPinsErrorsNextToSaveButtonOmitsOlderHubFieldsAndSupportsCapture() {
        val savedPayloads = mutableListOf<String>()
        var revision = 1L
        var economy = ModelProfile(
            id = "economy",
            name = "Economy",
            description = "Older hub profile",
            active = true,
            changes = listOf("assistant"),
            purposes = mapOf("assistant" to ModelBinding(provider = "launcher.claude", model = "sonnet-4")),
            tiers = mapOf("standard" to ModelProfileTier(launcher = "claude", model = "sonnet-4", speed = "standard")),
        )
        // Older hub: launcher purposes omit permission_mode from fields.
        val olderHubPurposes = listOf(
            ModelPurpose(
                id = "assistant",
                name = "Main chat",
                group = "Default",
                kind = "launcher",
                fields = listOf("provider", "model", "effort"),
                providerIds = listOf("launcher.claude"),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(olderHubPurposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(olderHubPurposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = ModelProfilesView(revision = revision, active = "economy", profiles = listOf(economy))
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                savedPayloads += input.toJson().toString()
                if (input.capture) {
                    revision++
                    economy = economy.copy(
                        changes = emptyList(),
                        purposes = mapOf("assistant" to ModelBinding(provider = "launcher.claude", model = "captured-model")),
                    )
                    return ModelProfileSave.Saved(ModelProfilesView(revision = revision, active = "economy", profiles = listOf(economy)))
                }
                throw HubFailure(HubError.Server, """json: unknown field "speed"""", 400)
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-edit-button").performClick()
        rule.waitForIdle()

        // Older hub hides empty permission_mode pickers on both purposes and tiers.
        rule.onNodeWithTag("purpose-permission-mode-assistant").assertDoesNotExist()
        rule.onNodeWithTag("tier-permission-mode-tier-0-standard").assertDoesNotExist()

        // Validation problem sits directly inside the pinned save bar next to Save.
        rule.onNode(hasSetTextAction() and hasText("Economy")).performTextReplacement("   ")
        rule.waitForIdle()
        rule.onNode(hasTestTag("model-profile-editor-problems") and hasAnyAncestor(hasTestTag("model-profile-editor-save-bar"))).assertIsDisplayed()
        rule.onNode(hasTestTag("model-profile-save-button") and hasAnyAncestor(hasTestTag("model-profile-editor-save-bar"))).assertIsNotEnabled()

        // Valid name -> save omits standard speed and empty permission_mode; 400 error sits inside pinned save bar and keeps draft.
        rule.onNode(hasSetTextAction() and hasText("   ")).performTextReplacement("Economy v2")
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-save-button").performClick()
        rule.waitForIdle()

        assertEquals(1, savedPayloads.size)
        assertFalse(savedPayloads[0].contains("permission_mode"))
        assertFalse(savedPayloads[0].contains("\"speed\""))
        rule.onNode(
            hasTestTag("model-profile-save-error") and
                hasText("unknown field", substring = true) and
                hasAnyAncestor(hasTestTag("model-profile-editor-save-bar")),
        ).assertIsDisplayed()
        rule.onNode(hasSetTextAction() and hasText("Economy v2")).assertIsDisplayed()

        // Capture directly from inside the editor refreshes draft and clears dirty state.
        rule.onNodeWithTag("model-profile-editor-capture-button").performClick()
        rule.waitForIdle()
        assertEquals(2, savedPayloads.size)
        assertTrue(savedPayloads[1].contains("\"capture\":true"))
        rule.onNode(hasSetTextAction() and hasText("captured-model")).assertIsDisplayed()
        rule.onNodeWithTag("model-profile-save-button").assertIsNotEnabled()
    }

    @Test fun activeDriftOnlyTriggersConfirmationEvenWhenTargetMatchesCurrent() {
        val calls = mutableListOf<String>()
        var revision = 5L
        var active = "performance"
        var perfChanges = listOf("translate")
        fun snapshot() = ModelProfilesView(
            revision = revision,
            active = active,
            profiles = listOf(
                ModelProfile(id = "performance", name = "Performance", active = active == "performance", changes = perfChanges),
                ModelProfile(id = "economy", name = "Economy", active = active == "economy", changes = emptyList()),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = snapshot()
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                calls += "PUT:$id:capture=${input.capture}"
                revision++
                perfChanges = emptyList()
                return ModelProfileSave.Saved(snapshot())
            }
            override suspend fun applyProfile(id: String): ModelProfilesView {
                calls += "APPLY:$id"
                active = id
                return snapshot()
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-economy").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-apply-button").performClick()
        rule.waitForIdle()

        assertTrue(calls.isEmpty())
        rule.onNodeWithTag("model-profile-apply-confirm").assertIsDisplayed()
        rule.onNodeWithTag("model-profile-apply-drift-list").assertDoesNotExist()
        rule.onNode(hasText("Translation") and hasAnyAncestor(hasTestTag("model-profile-active-drift-notice"))).assertIsDisplayed()
        rule.onNodeWithTag("model-profile-capture-and-apply").performClick()
        rule.waitForIdle()
        assertEquals(listOf("PUT:performance:capture=true", "APPLY:economy"), calls)
    }

    @Test
    @Config(sdk = [35], qualifiers = "en-w400dp-h640dp-xhdpi")
    fun editorSaveBarKeepsErrorsAndReloadVisibleWithoutScrollingOnCompactPhone() {
        var revision = 2L
        var conflictOnce = true
        val longProfile = ModelProfile(
            id = "long",
            name = "Long Profile",
            description = "Covers all categories and multiple presets",
            active = true,
            purposes = ModelProfileRules.CATEGORY_IDS.associateWith {
                ModelBinding(provider = "launcher.claude", model = "sonnet-4", effort = "high")
            },
            tiers = mapOf(
                "strong" to ModelProfileTier(launcher = "claude", model = "opus-4", description = "Hard tasks"),
                "standard" to ModelProfileTier(launcher = "claude", model = "sonnet-4", description = "Daily work"),
                "fast" to ModelProfileTier(launcher = "claude", model = "haiku-4", description = "Bulk work"),
            ),
        )
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles() = ModelProfilesView(revision = revision, active = "long", profiles = listOf(longProfile))
            override suspend fun saveProfile(id: String, input: ModelProfileSaveInput): ModelProfileSave {
                if (conflictOnce) {
                    conflictOnce = false
                    revision = 3L
                    return ModelProfileSave.Conflict
                }
                revision++
                return ModelProfileSave.Saved(ModelProfilesView(revision = revision, active = "long", profiles = listOf(longProfile.copy(name = input.name))))
            }
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithTag("model-profile-row-long").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-edit-button").performClick()
        rule.waitForIdle()

        // Without scrolling on a 640dp viewport, trigger a 409 conflict on Save -> error + reload are immediately visible next to Save.
        rule.onNode(hasSetTextAction() and hasText("Long Profile")).performTextReplacement("Long Profile v2")
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-save-button").performClick()
        rule.waitForIdle()

        rule.onNode(hasTestTag("model-profile-save-error") and hasAnyAncestor(hasTestTag("model-profile-editor-save-bar"))).assertIsDisplayed()
        rule.onNode(hasTestTag("model-profile-reload-button") and hasAnyAncestor(hasTestTag("model-profile-editor-save-bar"))).assertIsDisplayed()
        rule.onNode(hasTestTag("model-profile-save-button") and hasAnyAncestor(hasTestTag("model-profile-editor-save-bar"))).assertIsNotEnabled()

        // Reload clears conflict and resets draft to fresh revision 3.
        rule.onNodeWithTag("model-profile-reload-button").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("model-profile-save-error").assertDoesNotExist()
        rule.onNode(hasSetTextAction() and hasText("Long Profile")).assertIsDisplayed()
    }

    @Test fun oldHubWithoutProfilesHidesProfilesSectionCleanly() {
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(purposes, providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purposes.first { it.id == id }, providers)
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft) = ModelPurposeSave.Conflict
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun profiles(): ModelProfilesView? = null
        }

        rule.setContent {
            CompositionLocalProvider(LocalManagerRoster provides null) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    ModelConfigurationPage(api, onBack = {}, onPurpose = { _, _ -> })
                }
            }
        }
        rule.waitForIdle()

        rule.onNodeWithText("Main chat").assertIsDisplayed()
        rule.onNodeWithText(rule.activity.getString(R.string.settings_model_profiles_title)).assertDoesNotExist()
    }
}
