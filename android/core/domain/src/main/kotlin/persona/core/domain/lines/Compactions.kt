package persona.core.domain.lines

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText

/**
 * A compaction as the chat shows it (docs/assistant/assistant.md "Manual compaction"): one divider where the context was compacted, with
 * how it came about ([trigger]: `manual`, `auto`, `idle`; null when unknown) and the context's size before it. [notes]
 * are the notices folded into it, in transcript order.
 */
data class Compaction(val trigger: String? = null, val preTokens: Long? = null, val notes: List<String> = emptyList()) {
    /** "Compacted above · manual · 181K tokens before", leaving out what is not known. */
    val label: CoreText get() = CoreText.joined(" · ", listOfNotNull(
        CoreText.of(Key.CompactedAbove),
        when (trigger) { "manual" -> Key.CompactManual; "auto" -> Key.CompactAuto; "idle" -> Key.CompactIdle; else -> null }?.let(CoreText::of),
        preTokens?.takeIf { it > 0 }?.let { CoreText.of(Key.CompactBefore, tokenCount(it)) },
    ))
}

/** What the hub writes for a compaction boundary without its own `compact` (Codex), and for the summary after it. */
const val CompactedNotice = "上下文已压缩" // i18n-ignore: the hub's notice it matches
const val ContinuedNotice = "已从压缩的上下文继续" // i18n-ignore: the hub's notice it matches
/** A /compact sent without instructions (the app's "Compact context", the node's idle compaction): a notice, not a message. */
const val CompactCommandNotice = "压缩上下文" // i18n-ignore: the node's notice it matches

/**
 * 181000 → 18.1 ten-thousands in Chinese and Japanese, "181K" in English, 1200000 → "1.2M" in English; under the language's unit the number itself. The unit
 * (ten thousand in Chinese and Japanese, a thousand in English) is the language's: [CoreText.Key.CompactCount] carries the count
 * ([persona.core.contracts.compactCount]).
 */
fun tokenCount(tokens: Long): CoreText = CoreText.of(Key.CompactCount, tokens)

private fun LineItem.isNotice(text: String) =
    kind == "notice" && task == null && item.task == null && item.wake != true && this.text == text
private fun LineItem.isBoundary() = kind == "notice" && (item.compact != null || isNotice(CompactedNotice))
/** Claude Code's own line after a compaction ("Compacted …", "Compacted while idle …"). */
private fun LineItem.isCompactedNote() = kind == "notice" && task == null && item.wake != true &&
    text.orEmpty().startsWith("Compacted")
/** Items that draw nothing and so do not part a boundary from its notes. */
private val silent = setOf("tool_result", "turn_duration", "unknown")

/**
 * A compaction is one item: the boundary keeps the notices that came with it as [Compaction.notes] — the /compact
 * before or after it ([CompactCommandNotice]), the summary it carried on with ([ContinuedNotice]) and Claude Code's line about
 * it — and they are no rows of their own. A summary whose boundary is on an older page stands in for it. Ids, texts and
 * the order of everything else stay as they were.
 */
fun foldCompactions(items: List<LineItem>): List<LineItem> = foldCompactions(items, hasCompactions(items))

/** [items] have a compaction for [foldCompactions] to fold: without one, it returns them as they are. */
internal fun hasCompactions(items: List<LineItem>): Boolean = items.any { it.isBoundary() || it.isNotice(ContinuedNotice) }

/**
 * [foldCompactions] on part of a chat ([ChatLinesMemo]): [compacting] is [hasCompactions] of the whole chat, since even
 * a part without a boundary changes when it folds (a "Compact context" waits past the silent items after it).
 */
internal fun foldCompactions(items: List<LineItem>, compacting: Boolean): List<LineItem> {
    if (!compacting) return items
    val out = ArrayList<LineItem>(items.size)
    var open: Int? = null // the boundary in `out` the notes after it still join
    var held: LineItem? = null // a "Compact context" waiting for the boundary after it
    fun release() { held?.let(out::add); held = null }
    fun join(note: LineItem, idle: Boolean = false) {
        val at = open ?: return
        val mark = out[at].compaction ?: Compaction()
        out[at] = out[at].copy(compaction = mark.copy(notes = mark.notes + note.text.orEmpty(),
            trigger = if (idle) "idle" else mark.trigger))
    }
    for (it in items) {
        when {
            it.isBoundary() -> {
                val notes = listOfNotNull(held?.text) + (it.text ?: CompactedNotice)
                held = null
                out += it.copy(compaction = Compaction(it.item.compact?.trigger?.takeIf(String::isNotBlank),
                    it.item.compact?.preTokens, notes))
                open = out.lastIndex
            }
            it.isNotice(CompactCommandNotice) -> if (open != null) join(it) else { release(); held = it }
            it.isNotice(ContinuedNotice) -> if (open != null) join(it) else {
                release()
                out += it.copy(compaction = Compaction(notes = listOf(it.text.orEmpty())))
                open = out.lastIndex
            }
            it.isCompactedNote() && open != null -> join(it, idle = it.text.orEmpty().startsWith("Compacted while idle"))
            it.kind in silent -> out += it
            else -> {
                release()
                open = null
                out += it
            }
        }
    }
    release()
    return out
}
