package persona.core.domain

/**
 * A message with attachments as the chat composer writes it (feature/chat `ChatComposerState.message`), for messages
 * written outside the composer: New Agent's first message (#2321). The words and the quoted file paths on one line, a
 * `<persona-file>` tag per video after a blank line, then the photos' paths in an `<attached_images>` block, the only
 * paths the apps draw as thumbnails above the message (docs/protocol/protocol.md, `<attached_images>`).
 */
object ComposerMessage {
    /**
     * [words], then [files] (paths on the computer) and [videos]; [photos] (paths on the computer) last. With no words
     * and no files, [photosOnly] stands in for photos and else [videosOnly] for videos, in the app's language.
     */
    fun text(
        words: String, photos: List<String>, files: List<String>, videos: List<AttachedFile> = emptyList(),
        photosOnly: String = "", videosOnly: String = "",
    ): String {
        val quoted = files.map(::quotePath)
        val said = when {
            words.isNotBlank() || quoted.isNotEmpty() -> words
            photos.isNotEmpty() -> photosOnly
            videos.isNotEmpty() -> videosOnly
            else -> words
        }
        val text = (listOf(said) + quoted).filter(String::isNotBlank).joinToString(" ")
        return text + (if (videos.isEmpty()) "" else "\n\n" + videos.joinToString("\n") { it.toXml() }) +
            if (photos.isEmpty()) "" else "\n\n<attached_images>\n${photos.joinToString("\n")}\n</attached_images>"
    }

    /** A path the shell would split or expand is single-quoted, so the agent reads it as one path. */
    fun quotePath(path: String): String =
        if (path.any { it.isWhitespace() || it in "'\"\\\$`;&|<>()[]{}*?!" }) "'${path.replace("'", "'\\''")}'" else path
}
