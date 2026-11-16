package persona.feature.agents

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import persona.core.contracts.api.AgentCreationService
import persona.core.contracts.api.AgentWorkspace

/** One pairing's read-only Paseo catalog. A failed refresh keeps its last source state; a successful empty read clears it. */
internal class AgentsWorkspaceCatalog {
    private val _rows = MutableStateFlow<List<AgentWorkspace>>(emptyList())
    val rows = _rows.asStateFlow()
    private var generation = 0

    suspend fun refresh(service: AgentCreationService, machines: List<Pair<String, Boolean>>) = coroutineScope {
        val read = ++generation
        val old = _rows.value.groupBy { it.machineId }
        val next = machines.map { (machine, online) -> async {
            if (!online) return@async old[machine].orEmpty().map { it.copy(online = false, available = false) }
            try {
                service.workspaceCatalog(machine)
                    .filter { it.machineId == machine && it.source == "paseo" && it.sourceId.isNotEmpty() }
                    .distinctBy { it.sourceId }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { old[machine].orEmpty() }
        } }.awaitAll().flatten().sortedWith(compareBy<AgentWorkspace> { it.machineId }.thenBy { it.sourceId })
        if (read == generation) _rows.value = next
    }
}
