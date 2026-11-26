package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasScrollToNodeAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onLast
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextReplacement
import androidx.compose.ui.unit.Density
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.addJsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.ExtensionDetail
import persona.core.contracts.api.ExtensionEntrySecret
import persona.core.contracts.api.ExtensionField
import persona.core.contracts.api.ExtensionItems
import persona.core.contracts.api.ExtensionSecret
import persona.core.contracts.api.ExtensionSecretUser
import persona.core.contracts.api.HubExtension
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme

/**
 * Extension settings entry lists (#9504 / #12325), driving the production ExtensionPage, entry editor, removal
 * confirmation dialog, and write-only secret sheet. Captures cover all three styles, light/dark, and default/200% text
 * outside the repository via `-Dpersona.shots=<dir>`. These are Robolectric native JVM captures, not device runs.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2000dp-xhdpi")
class ExtensionEntryListsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun baseDetail(
        required: Boolean = false,
        minItems: Int? = null,
        maxItems: Int = 2,
        secretUsers: List<ExtensionSecretUser> = emptyList(),
    ): ExtensionDetail = ExtensionDetail(
        HubExtension("example-entries", "Example Entries", "connectors", "running", version = "1.0.0", source = "installed", enabled = true),
        form = listOf(
            ExtensionField(
                name = "accounts",
                type = "array",
                title = "Accounts",
                required = required,
                minItems = minItems,
                maxItems = maxItems,
                secret = ExtensionEntrySecret(name = "account_key", title = "Account key", hint = "last4"),
                items = ExtensionItems(
                    type = "object",
                    fields = listOf(
                        ExtensionField("name", "string", title = "Name", required = true),
                        ExtensionField("address", "string", title = "Address", format = "uri", required = true, clearsSecret = true),
                    ),
                ),
            ),
        ),
        config = buildJsonObject {
            putJsonArray("accounts") {
                addJsonObject {
                    put("_id", "entry_saved")
                    put("name", "One")
                    put("address", "https://one.example.com")
                }
            }
        },
        secrets = listOf(
            ExtensionSecret("entry_saved", title = "Account key", set = true, hint = "1234", list = "accounts", entryTitle = "One"),
        ),
        secretUsers = secretUsers,
    )

    private class Hub(var current: ExtensionDetail) : ExtensionApiStub() {
        val configs = mutableListOf<JsonObject>()
        val secrets = mutableListOf<Pair<String, String>>()
        override suspend fun extension(id: String): ExtensionDetail = current
        override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail {
            configs += config
            val savedAccounts = (config["accounts"] as? JsonArray)?.mapIndexed { index, el ->
                val obj = el as JsonObject
                if ("_id" in obj) obj else JsonObject(obj + ("_id" to JsonPrimitive("entry_new_$index")))
            }
            val nextConfig = if (savedAccounts != null) JsonObject(config + ("accounts" to JsonArray(savedAccounts))) else config
            val nextSecrets = savedAccounts.orEmpty().map { obj ->
                val entryId = obj["_id"]!!.jsonPrimitive.content
                current.secrets.firstOrNull { it.name == entryId }
                    ?: ExtensionSecret(entryId, title = "Account key", required = true, set = false, list = "accounts",
                        entryTitle = obj["name"]?.jsonPrimitive?.contentOrNull)
            }
            current = current.copy(config = nextConfig, secrets = nextSecrets)
            return current
        }
        override suspend fun setSecret(id: String, name: String, value: String): ExtensionDetail {
            secrets += name to value
            current = current.copy(secrets = current.secrets.map {
                if (it.name == name) it.copy(set = true, hint = value.takeLast(4)) else it
            })
            return current
        }
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/entry-list-shots").also { it.mkdirs() }

    private fun show(hub: Hub) {
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                    ExtensionPage(hub, hub.current.extension.id, onBack = {})
                }
            }
        }
        rule.waitUntil { rule.onAllNodes(hasTestTag("entry-list-accounts")).fetchSemanticsNodes().isNotEmpty() }
    }

    @Test fun addsAndEditsEntryWithoutLeakingLocalKeysOrSecretIntoSavedConfig() {
        val hub = Hub(baseDetail())
        show(hub)

        rule.onNodeWithText("Set · ends in 1234").assertExists()
        rule.onNodeWithText("Saving a changed address clears that entry's secret.", substring = true).assertExists()
        // Entry secrets must not appear again in a fixed Secrets section.
        rule.onAllNodesWithText("Secrets").assertCountEquals(0)
        rule.onAllNodesWithText("Secret").assertCountEquals(1)

        rule.onNodeWithText("Add entry").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("Add entry").assertIsNotEnabled()
        rule.onNodeWithText("Save this entry before setting its secret.").assertExists()
        rule.onNode(hasText("Secret") and hasAnyAncestor(hasTestTag("entry-card-new-entry-1"))).assertIsNotEnabled()

        val secondInputs = rule.onAllNodes(hasSetTextAction() and hasAnyAncestor(hasTestTag("entry-card-new-entry-1")))
        secondInputs.assertCountEquals(2)
        secondInputs[0].performTextReplacement("Two")
        secondInputs[1].performTextReplacement("https://two.example.com")
        rule.waitForIdle()

        // Stable key stays identical across edits.
        rule.onNodeWithTag("entry-card-new-entry-1").assertExists()

        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("Save"))
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil { hub.configs.isNotEmpty() }

        val sent = hub.configs.single()
        assertEquals(
            buildJsonObject {
                putJsonArray("accounts") {
                    addJsonObject {
                        put("_id", "entry_saved")
                        put("name", "One")
                        put("address", "https://one.example.com")
                    }
                    addJsonObject {
                        put("name", "Two")
                        put("address", "https://two.example.com")
                    }
                }
            },
            sent,
        )
        assertFalse(sent.toString().contains("new-entry-"))
        assertFalse(sent.toString().contains("account_key"))

        // Once saved, the hub minted _id so the second entry's Secret button becomes enabled.
        rule.waitUntil {
            rule.onAllNodes(hasTestTag("entry-card-entry_new_1")).fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNode(hasText("Secret") and hasAnyAncestor(hasTestTag("entry-card-entry_new_1"))).assertIsEnabled()
    }

    @Test fun clearingOnlyPopulatedFieldKeepsEntryCardAndSavedId() {
        val hub = Hub(baseDetail())
        show(hub)

        // Add an unsaved entry, populate only Name, then clear it: the row card stays for further editing.
        rule.onNodeWithText("Add entry").performClick()
        rule.waitForIdle()
        val secondInputs = rule.onAllNodes(hasSetTextAction() and hasAnyAncestor(hasTestTag("entry-card-new-entry-1")))
        secondInputs[0].performTextReplacement("Draft")
        rule.waitForIdle()
        rule.onNodeWithContentDescription("Remove Draft").assertExists()
        secondInputs[0].performTextReplacement("")
        rule.waitForIdle()
        rule.onNodeWithTag("entry-card-new-entry-1").assertExists()
        rule.onNodeWithText("Entry 2").assertExists()
        rule.onNodeWithContentDescription("Remove Entry 2").assertExists()

        // Clearing all fields on the saved entry keeps its card, host-minted _id, and Secret action intact.
        val firstInputs = rule.onAllNodes(hasSetTextAction() and hasAnyAncestor(hasTestTag("entry-card-entry_saved")))
        firstInputs[0].performTextReplacement("")
        firstInputs[1].performTextReplacement("")
        rule.waitForIdle()
        rule.onNodeWithTag("entry-card-entry_saved").assertExists()
        rule.onNodeWithText("Entry 1").assertExists()
        rule.onNodeWithContentDescription("Remove Entry 1").assertExists()
        rule.onNode(hasText("Secret") and hasAnyAncestor(hasTestTag("entry-card-entry_saved"))).assertIsEnabled()
    }

    @Test fun opensExistingWriteOnlySecretSheetForSavedEntryOnly() {
        val hub = Hub(baseDetail())
        show(hub)

        rule.onNode(hasText("Secret") and hasAnyAncestor(hasTestTag("entry-card-entry_saved"))).assertIsEnabled().performClick()
        rule.waitUntil { rule.onAllNodesWithText("One · Account key").fetchSemanticsNodes().isNotEmpty() }
        rule.onNode(hasSetTextAction() and hasAnyAncestor(isDialog())).performTextReplacement("placeholder-secret-5678")
        rule.onNode(hasText("Save") and hasAnyAncestor(isDialog())).performClick()
        rule.waitUntil { hub.secrets.isNotEmpty() }

        assertEquals(listOf("entry_saved" to "placeholder-secret-5678"), hub.secrets)
        assertTrue(hub.configs.isEmpty())
        rule.waitUntil { rule.onAllNodesWithText("Set · ends in 5678").fetchSemanticsNodes().isNotEmpty() }
        rule.onAllNodesWithText("placeholder-secret-5678", substring = true).assertCountEquals(0)
    }

    @Test fun confirmsRemovalAndOmitsEmptiedOptionalListOnSave() {
        val hub = Hub(baseDetail(required = false))
        show(hub)

        rule.onNodeWithContentDescription("Remove One").performClick()
        rule.onNodeWithText("Remove “One”?").assertExists()
        rule.onNodeWithText("The entry and its saved secret are deleted when you save.").assertExists()
        rule.onNodeWithText("Cancel").performClick()
        rule.onNodeWithTag("entry-card-entry_saved").assertExists()
        assertTrue(hub.configs.isEmpty())

        rule.onNodeWithContentDescription("Remove One").performClick()
        rule.onNodeWithText("Remove entry").performClick()
        rule.waitForIdle()
        rule.onAllNodes(hasTestTag("entry-card-entry_saved")).assertCountEquals(0)
        assertTrue(hub.configs.isEmpty())

        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("Save"))
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil { hub.configs.isNotEmpty() }
        assertEquals(JsonObject(emptyMap()), hub.configs.single())
    }

    @Test fun savesExplicitEmptyArrayForRequiredListWithZeroMinimum() {
        val hub = Hub(baseDetail(required = true, minItems = 0))
        show(hub)

        rule.onNodeWithContentDescription("Remove One").assertIsEnabled().performClick()
        rule.onNodeWithText("Remove entry").performClick()
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("Save"))
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil { hub.configs.isNotEmpty() }
        assertEquals(buildJsonObject { putJsonArray("accounts") {} }, hub.configs.single())
    }

    @Test fun optionalListWithPositiveMinimumStillAllowsReturningToAbsentState() {
        val hub = Hub(baseDetail(required = false, minItems = 1))
        show(hub)

        rule.onNodeWithContentDescription("Remove One").assertIsEnabled().performClick()
        rule.onNodeWithText("Remove entry").performClick()
        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("Save"))
        rule.onNodeWithText("Save").performClick()
        rule.waitUntil { hub.configs.isNotEmpty() }
        assertEquals(JsonObject(emptyMap()), hub.configs.single())
    }

    @Test fun requiredListAtDeclaredMinimumDisablesRemoveButton() {
        val hub = Hub(baseDetail(required = true, minItems = 1))
        show(hub)

        rule.onNode(hasContentDescription("Remove One")).assertIsNotEnabled()
    }

    @Test fun secretProviderUsedByNamesTheEntry() {
        val hub = Hub(
            baseDetail(
                secretUsers = listOf(
                    ExtensionSecretUser(
                        kind = "extension",
                        extension = "example-entries",
                        extensionName = "Example Entries",
                        secret = "entry_saved",
                        secretTitle = "Account key",
                        entryTitle = "One",
                        reference = "op://Dev/Accounts/one",
                    ),
                ),
            ),
        )
        show(hub)

        rule.onNode(hasScrollToNodeAction()).performScrollToNode(hasText("Used by"))
        rule.onNodeWithText("Example Entries › Account key (One)").assertExists()
        rule.onNodeWithText("One · op://Dev/Accounts/one").assertExists()
    }

    @Test fun capturesEveryStyleAndAppearance() = captureMatrix(large = false)

    @Test @Config(fontScale = 2f) fun capturesEveryStyleAndAppearanceAtLargestText() = captureMatrix(large = true)

    private fun captureMatrix(large: Boolean) {
        val hub = Hub(baseDetail(maxItems = 3))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var run by mutableStateOf(0)
        rule.setContent {
            val density = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(density.density, if (large) 2f else 1f)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background).testTag("shot")) {
                        key(run) {
                            ExtensionPage(hub, hub.current.extension.id, onBack = {})
                        }
                    }
                }
            }
        }
        fun capture(name: String, dialog: Boolean = false) {
            Snapshot.sendApplyNotifications()
            rule.waitForIdle()
            val node = if (dialog) rule.onAllNodes(isDialog()).onLast() else rule.onNodeWithTag("shot")
            val bitmap = node.captureToImage().asAndroidBitmap()
            assertTrue("$name drew an image", bitmap.width > 0 && bitmap.height > 0)
            File(out(), "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d; run++
            rule.waitUntil { rule.onAllNodes(hasTestTag("entry-list-accounts")).fetchSemanticsNodes().isNotEmpty() }
            rule.onNodeWithText("Add entry").performClick()
            rule.waitForIdle()
            val suffix = "${s.name.lowercase()}-${if (d) "dark" else "light"}-${if (large) "text-200" else "text-100"}"
            capture("entry-list-editor-$suffix")
            rule.onNode(hasText("Secret") and hasAnyAncestor(hasTestTag("entry-card-entry_saved"))).performClick()
            rule.waitUntil { rule.onAllNodesWithText("One · Account key").fetchSemanticsNodes().isNotEmpty() }
            capture("entry-list-secret-sheet-$suffix", dialog = true)
            // Re-key the page cleanly before opening the removal confirmation dialog.
            run++
            rule.waitUntil { rule.onAllNodes(hasTestTag("entry-list-accounts")).fetchSemanticsNodes().isNotEmpty() }
            rule.onNodeWithContentDescription("Remove One").performClick()
            rule.waitUntil { rule.onAllNodesWithText("Remove “One”?").fetchSemanticsNodes().isNotEmpty() }
            capture("entry-list-remove-dialog-$suffix", dialog = true)
        }
    }
}
