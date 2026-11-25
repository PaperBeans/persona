package persona.feature.chat

/** The sender and body of a conversation relay as the hub writes it in the queue (queuedRelays). */
internal data class QueuedRelay(val source: String, val text: String, val noWake: Boolean) {
    companion object {
        private val prefix = Regex("""^来自「([^\n]*?)」(（不唤醒）)?：""") // i18n-ignore: hub queue label

        fun parse(raw: String): QueuedRelay? {
            val match = prefix.find(raw) ?: return null
            val source = match.groupValues[1].trim()
            if (source.isEmpty()) return null
            return QueuedRelay(source, raw.substring(match.range.last + 1), match.groups[2] != null)
        }
    }
}
