package persona.feature.settings

import persona.core.contracts.api.*
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.JsonObject

/** An extension API whose every call fails unless a test overrides it: each fake implements only what it is about. */
internal abstract class ExtensionApiStub : ExtensionManagement {
    override val available = flowOf(true)
    private fun unused(): Nothing = throw AssertionError("not used in this test")
    override suspend fun extensions(): List<HubExtension> = unused()
    override suspend fun extension(id: String): ExtensionDetail = unused()
    override suspend fun setEnabled(id: String, enabled: Boolean, cascade: Boolean): ExtensionDetail = unused()
    override suspend fun setConfig(id: String, config: JsonObject): ExtensionDetail = unused()
    override suspend fun setSecret(id: String, name: String, value: String): ExtensionDetail = unused()
    override suspend fun clearSecret(id: String, name: String): ExtensionDetail = unused()
    override suspend fun uiPlaces(placement: String, launcher: String?, machine: String?): List<ExtensionUiPlace> = unused()
    override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView = unused()
    override suspend fun uiAction(extension: String, ui: String, action: String, context: ExtensionUiContext?, state: String?,
        values: Map<String, String>?): ExtensionUiResult = unused()
    override suspend fun markets(): List<ExtensionMarket> = unused()
    override suspend fun addMarket(spec: String, token: String?): ExtensionMarket = unused()
    override suspend fun removeMarket(id: String): Unit = unused()
    override suspend fun refreshMarket(id: String): ExtensionMarket = unused()
    override suspend fun setMarketToken(id: String, token: String): ExtensionMarket = unused()
    override suspend fun catalog(query: String, market: String?, refresh: Boolean): ExtensionCatalog = unused()
    override suspend fun previewFromMarket(market: String, extension: String): ExtensionPreview = unused()
    override suspend fun install(preview: ExtensionPreview): ExtensionDetail = unused()
    override suspend fun upgrade(id: String, preview: ExtensionPreview): ExtensionDetail = unused()
    override suspend fun uninstall(id: String, keepData: Boolean, cascade: Boolean, members: Boolean): ExtensionRemoval = unused()
}
