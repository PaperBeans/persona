package persona.core.domain

import java.util.Locale

private val claudeModel = Regex("""^claude-([a-z]+)-(\d+)(?:-(\d+))?$""", RegexOption.IGNORE_CASE)
private val gptModel = Regex("""^gpt-([\d.]+)((?:-[a-z0-9]+)*)$""", RegexOption.IGNORE_CASE)

/**
 * A model's short name, as a row or a chip names it (web `modelChipLabel`; `DESIGN_SYSTEM.md` section 5.6: never the raw
 * model ID on a primary screen): a Claude id as its family and version ("claude-haiku-4-5-20251001" is "Haiku 4.5"), a GPT
 * id as OpenAI writes it ("gpt-5.4-mini" is "GPT-5.4 Mini", "gpt-5-codex" "GPT-5 Codex"), anything else in title case; a gateway's "<provider>/" and a
 * cloud route's "anthropic." prefix dropped. Empty without a model.
 */
fun modelShortName(model: String?): String {
    val id = model.orEmpty().trim().substringAfterLast('/')
        .replace(Regex("""\[1m]$""", RegexOption.IGNORE_CASE), "").replace(Regex("""-\d{8}$"""), "")
        .replace(Regex("""^(?:[a-z]{2}\.)?anthropic\.""", RegexOption.IGNORE_CASE), "")
    if (id.isEmpty()) return ""
    fun word(w: String) = w.lowercase(Locale.ROOT).replaceFirstChar { it.titlecase(Locale.ROOT) }
    claudeModel.matchEntire(id)?.let { m ->
        val (family, major, minor) = m.destructured
        return word(family) + " " + if (minor.isNotEmpty()) "$major.$minor" else major
    }
    gptModel.matchEntire(id)?.let { m ->
        return "GPT-" + m.groupValues[1] + m.groupValues[2].split('-').filter(String::isNotEmpty).joinToString("") { " " + word(it) }
    }
    return id.split('-').filter(String::isNotEmpty).joinToString(" ", transform = ::word)
}
