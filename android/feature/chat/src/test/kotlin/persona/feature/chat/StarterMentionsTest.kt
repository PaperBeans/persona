package persona.feature.chat

import android.content.Context
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.api.SessionFileEntry

/** New chat's @ list (#4339 C16, iOS `StarterComposer`'s `MentionSource(machine:)` and `MentionRows`). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class StarterMentionsTest {
    private val res = ApplicationProvider.getApplicationContext<Context>().resources
    private val login = Session(machineId = "mbp", sessionId = "s1", title = "Fix the login page", cwd = "/w/app",
        updatedAt = "2026-11-12T09:00:00Z")
    private val photos = Session(machineId = "studio", sessionId = "s2", title = "Photo sync", cwd = "/w/photos", agent = "codex",
        updatedAt = "2026-11-12T10:30:00.5Z", lastText = "Synced 120 photos.")
    private val old = Session(machineId = "mbp", sessionId = "s3", title = "Release notes", cwd = "/w/site",
        updatedAt = "2026-11-10T08:00:00Z")
    private val persona = Session(machineId = "mbp", sessionId = "p", title = "Persona", assistant = true,
        updatedAt = "2026-11-12T11:00:00Z")
    private val folders = MentionFolders { _, _ -> emptyList() }
    private val home = listOf(SessionFileEntry("notes.md"), SessionFileEntry("code", dir = true), SessionFileEntry(".zshrc"),
        SessionFileEntry("Desktop", dir = true), SessionFileEntry("decor.txt"), SessionFileEntry("codex.log"), SessionFileEntry(".config", dir = true))

    @Test fun itListsEveryComputersAgentSessionsNewestFirstAndNoTasksOrFiles() {
        val source = starterMentionSource(listOf(old, persona, login, photos), folders)
        assertEquals(emptyList<String>(), source.files)
        assertNull(source.tasks)
        assertNull("nothing is this chat yet", source.except)
        // Persona's own conversations are not offered; the newest first, whichever computer it is on.
        assertEquals(listOf("session:studio/s2", "session:mbp/s1", "session:mbp/s3"), mentionRows(source, "").map { it.key })
        val many = (1..12).map { Session(machineId = "mbp", sessionId = "n$it", title = "Agent $it", updatedAt = "2026-11-0${it % 9 + 1}T00:00:00Z") }
        assertEquals("at most eight", 8, mentionRows(starterMentionSource(many, folders), "").size)
    }

    @Test fun theTypedWordNarrowsByTitleOrAgentAndFolder() {
        val source = starterMentionSource(listOf(old, persona, login, photos), folders)
        assertEquals(listOf("session:mbp/s1"), mentionRows(source, "LOGIN").map { it.key })
        assertEquals("by the agent", listOf("session:studio/s2"), mentionRows(source, "codex").map { it.key })
        assertEquals("by the folder", listOf("session:mbp/s3"), mentionRows(source, "site").map { it.key })
        assertEquals(emptyList<MentionRow>(), mentionRows(source, "nothing"))
    }

    @Test fun anAbsoluteOrHomePathBrowsesThatFolderOnly() {
        assertEquals(MentionBrowse("~", "~/", ""), mentionBrowse("~/", ""))
        assertEquals(MentionBrowse("~/code", "~/code/", "ap"), mentionBrowse("~/code/ap", ""))
        assertEquals(MentionBrowse("/", "/", "et"), mentionBrowse("/et", ""))
        assertEquals(MentionBrowse("/etc", "/etc/", ""), mentionBrowse("/etc/", ""))
        // A new conversation has no folder: a relative path or a plain word lists no folder.
        assertNull(mentionBrowse("code/", ""))
        assertNull(mentionBrowse("code", ""))
        assertEquals(MentionBrowse("/w/app/src", "src/", "ma"), mentionBrowse("src/ma", "/w/app/"))

        val source = starterMentionSource(listOf(login, photos), folders)
        // Folders first, then files; hidden ones only for a filter that starts with a dot; no sessions while browsing.
        assertEquals(listOf("code/", "Desktop/", "notes.md", "decor.txt", "codex.log"), mentionRows(source, "~/", home).map(::shown))
        assertEquals("names starting with the word first", listOf("code/", "codex.log", "decor.txt"),
            mentionRows(source, "~/CO", home).map(::shown))
        assertEquals(listOf(".config/"), mentionRows(source, "~/.c", home).map(::shown))
        assertEquals(listOf(".zshrc"), mentionRows(source, "~/.z", home).map(::shown))
        assertEquals("nothing until the folder is read", emptyList<MentionRow>(), mentionRows(source, "~/", null))
        assertEquals("a slash without folders narrows as before", emptyList<MentionRow>(),
            mentionRows(source.copy(folders = null), "~/", home))
        val big = (1..50).map { SessionFileEntry("f$it.txt") }
        assertEquals("at most forty", 40, mentionRows(source, "/tmp/", big).size)
    }

    @Test fun aPickTypesTheFileOrKeepsBrowsingTheFolderOrQuotesTheSession() {
        val source = starterMentionSource(listOf(login, photos), folders)
        val rows = mentionRows(source, "~/", home).filterIsInstance<MentionRow.Entry>()
        val folder = rows.first { it.name == "code" }
        val file = rows.first { it.name == "notes.md" }
        assertEquals("@~/code/", folder.insert)
        assertEquals("@~/notes.md ", file.insert)
        val typed = TextFieldValue("Look at @~/", TextRange(11))
        val token = requireNotNull(mentionAt(typed.text, 11))
        val picked = replaceMention(typed, token, folder.insert)
        assertEquals("Look at @~/code/", picked.text)
        // The caret stays after the slash: the list now browses that folder.
        assertEquals(MentionBrowse("~/code", "~/code/", ""), mentionBrowse(requireNotNull(mentionAt(picked.text, picked.selection.start)).query, ""))

        // A session goes in front of the first message as a <persona-context> tag, once, and the words after a blank line.
        val mentions = StarterMentions(mutableStateOf(emptyList()))
        val quote = sessionQuote(photos, res)
        mentions.add(quote)
        mentions.add(sessionQuote(photos, res))
        assertEquals(1, mentions.quotes.size)
        val message = mentions.message("Compare with this", res)
        assertEquals("<persona-context kind=\"context\" title=\"Photo sync\" url=\"persona://session/studio/s2\">\n" +
            "Another session: Photo sync (Codex)\nFolder: /w/photos\nIt last said: Synced 120 photos.\n</persona-context>\n\n" +
            "Compare with this", message)
        assertEquals("Compare with this", messageWords(message))
        assertEquals("only the quote", "See the content above.", messageWords(mentions.message("", res)))
        // Sent: out of the box; one picked meanwhile stays.
        mentions.add(sessionQuote(login, res))
        mentions.sent(listOf(quote))
        assertEquals(listOf("persona://session/mbp/s1"), mentions.quotes.map { it.url })
    }

    private fun shown(row: MentionRow) = (row as MentionRow.Entry).let { if (it.folder) "${it.name}/" else it.name }
}
