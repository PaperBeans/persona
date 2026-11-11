package persona.feature.agents

/** New Agent's ultracode switch (#582, iOS `Ultracode.startOffered` / `startBlocked`). */
internal object NewAgentUltracode {
    /** The hub takes the switch (hello `features`). */
    const val FEATURE = "ultracode"
    /** The Claude Code version that has it (the node's own rule). */
    val MIN_VERSION = listOf(2, 1, 284)
    private val version = Regex("""^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$""")

    /** Offered: the hub has it, the agent is Claude Code, and the model that will run takes an effort. */
    fun offered(features: Set<String>, agentType: String, efforts: List<String>?): Boolean =
        FEATURE in features && agentType == "claude" && efforts?.isEmpty() != true

    /** A known Claude Code version older than the switch's: an unknown one may be tried. */
    fun tooOld(claudeVersion: String?): Boolean {
        val m = version.matchEntire(claudeVersion?.trim().orEmpty()) ?: return false
        val nums = m.groupValues.drop(1).map { it.toInt() }
        for ((n, least) in nums.zip(MIN_VERSION)) if (n != least) return n < least
        return false
    }
}

/**
 * The workflow mark beside a list row's title (iOS `Ultracode.rowMark`): the node saw ultracode on for a Claude Code agent
 * of the user's, and the row is not offline (the node keeps the last value it saw; a resume starts it off).
 */
internal fun ultracodeMark(session: persona.core.contracts.Session): Boolean =
    session.ultracode == true && !session.assistant && session.isClaude && session.agentState != persona.core.contracts.AgentState.Offline
