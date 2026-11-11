package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.JsonElement
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.JsonCodec
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.generated.ExtensionAssistantHTMLActionInput
import persona.core.contracts.generated.ExtensionViewActionInput
import persona.core.contracts.api.AssistantViewAnswer
import persona.core.contracts.api.TasksApi
import persona.core.contracts.generated.ExtensionViewAction
import persona.core.designsystem.AppStyle
import persona.core.designsystem.LocalAssistantCards
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.ContentTokens
import persona.core.domain.views.AssistantCardActions
import persona.core.domain.views.AssistantCardsHost
import persona.core.domain.views.AssistantViewStore
import java.io.File

/**
 * An assistant's cards in a conversation (#2022) drawn on the JVM with Robolectric's native graphics: the shared
 * examples (schema/extension/examples/assistant/) to answer, answered, sent and failed, a live card after its update,
 * an HTML page's fallback and a block still coming in. Every style, light and dark, written to
 * `build/assistant-card-shots/` (or `-Dpersona.shots=<dir>`) to look at; and a tap's request, in the hub's words.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h2400dp-xhdpi")
class AssistantCardsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun example(name: String): String =
        generateSequence(File(System.getProperty("persona.repo.root") ?: System.getProperty("user.dir")).absoluteFile) { it.parentFile }
            .map { File(it, "schema/extension/examples/assistant/$name.json") }.first { it.exists() }.readText()

    private fun json(text: String): JsonElement = JsonCodec.parseToJsonElement(text)

    private class Api : TasksApi {
        override val features = kotlinx.coroutines.flow.MutableStateFlow(setOf("assistant_views", "assistant_html_bridge"))
        val sent = mutableListOf<Pair<String, ExtensionViewActionInput>>()
        var answer: (String, ExtensionViewActionInput) -> AssistantViewAnswer = { key, input ->
            AssistantViewAnswer.Recorded(ExtensionViewAction(key = key, action = input.action, values = input.values, clientMsgId = input.clientMsgId,
                status = "pending", revision = 1, at = "2026-11-05T10:00:00Z"))
        }
        override suspend fun todos() = emptyList<Todo>()
        override suspend fun feed(conversation: String, since: String?) = FeedResponse()
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
        override suspend fun viewAction(conversation: String, key: String, input: ExtensionViewActionInput): AssistantViewAnswer {
            sent += key to input
            return answer(key, input)
        }
        override suspend fun viewHtmlAction(conversation: String, key: String, input: ExtensionAssistantHTMLActionInput): AssistantViewAnswer = error("unused")
    }

    private fun host(api: Api = Api(), interactive: Boolean = true): AssistantCardsHost {
        val store = AssistantViewStore()
        return AssistantCardsHost("c1", store, AssistantCardActions(api, store, reload = {}), interactive)
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/assistant-card-shots").also { it.mkdirs() }

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val background = bitmap.getPixel(1, 1)
        var other = 0
        for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
        assertTrue("$name drew nothing", other > 100)
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private fun answer(key: String, action: String, status: String, values: String = "{}", error: String? = null) =
        json("""{"key":"$key","action":"$action","values":$values,"client_msg_id":"m","status":"$status","revision":2,
            ${error?.let { "\"error\":\"$it\"," } ?: ""}"at":"2026-11-05T10:00:00Z"}""")

    // A list card with its search box, filter and "Load more" (each a reply of the card).
    private val searchCard = """{"title":"挑一篇笔记","fallback":"回复要看的笔记，或换个文件夹、再来一批。",
        "search":{"placeholder":"搜笔记","value":"周报","action":{"id":"find","reply":"按这个搜"}},
        "filter":{"name":"folder","title":"文件夹","choices":[{"value":"all","title":"全部"},{"value":"work","title":"工作"}],"value":"work",
            "action":{"id":"pick","reply":"换文件夹"}},
        "sections":[{"items":[{"type":"row","title":"周报 11-03","subtitle":"工作","action":{"id":"n1","reply":"看周报 11-03"}},
            {"type":"row","title":"周报 10-27","subtitle":"工作","action":{"id":"n2","reply":"看周报 10-27"}}],
            "more":{"title":"再来一批","action":{"id":"more","reply":"再来一批"}}}]}"""

    // Only a search box: the search is sent on a tap of "Search".
    private val searchOnlyCard = """{"title":"找联系人","fallback":"回复要找的人名。",
        "search":{"placeholder":"输入名字","action":{"id":"who","reply":"找这个人"}},
        "sections":[{"items":[{"type":"text","text":"输入名字后点搜索，助手会接着找。","style":"secondary"}]}]}"""

    @Test fun drawsTheCardsInEveryStateAndStyle() {
        val open = host()
        open.store.snapshot("c1", emptyMap(), emptyMap(), listOf("i1/0", "i1/1"))
        val answered = host()
        answered.store.snapshot("c1", mapOf("view_p" to json("""{"id":"view_p","version":2,"view":${example("progress-updated")}}""")),
            mapOf("i2/0" to answer("i2/0", "choose-b", "sent"),
                "i2/1" to answer("i2/1", "submit", "failed", """{"task":"整理读书笔记","priority":"high","notify":"false"}""", "助手暂时不在线")),
            listOf("i2/0", "i2/1"))
        var scene by mutableStateOf(0)
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background)) {
                    Column(Modifier.fillMaxWidth().padding(Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(ContentTokens.legacyXl)) {
                        when (scene) {
                            0 -> CompositionLocalProvider(LocalAssistantCards provides open) {
                                AssistantBlock("i1/0", example("decision"), complete = true)
                                AssistantBlock("i1/1", example("form"), complete = true)
                            }
                            1 -> CompositionLocalProvider(LocalAssistantCards provides answered) {
                                AssistantBlock("i2/0", example("decision"), complete = true)
                                AssistantBlock("i2/1", example("form"), complete = true)
                                AssistantLiveCard("view_p")
                            }
                            3 -> CompositionLocalProvider(LocalAssistantCards provides open) {
                                AssistantBlock("i1/0", searchCard, complete = true)
                                AssistantBlock("i1/1", searchOnlyCard, complete = true)
                            }
                            else -> CompositionLocalProvider(LocalAssistantCards provides open) {
                                AssistantBlock("i3/0", example("html"), complete = true)
                                AssistantBlock("i3/1", """{"fallback":"正在整理三个方案的对比…","sections":[{"items":[{"type":"te""", complete = false)
                                AssistantBlock("i3/2", example("compare"), complete = true)
                            }
                        }
                    }
                }
            }
        }
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            for ((n, name) in listOf("open", "answered", "fallback", "search").withIndex()) {
                scene = n
                capture("assistant-cards-$name-${s.name.lowercase()}-${if (d) "dark" else "light"}")
            }
        }
    }

    @Test fun aTapSendsTheActionWithTheVersionSeenOnce() {
        val api = Api()
        val h = host(api)
        h.store.block("c1", "i1/0")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i1/0", example("decision"), complete = true) }
            }
        }
        rule.onNodeWithText("B 队列去重").performClick()
        rule.waitForIdle()
        assertEquals(1, api.sent.size)
        val (key, input) = api.sent.single()
        assertEquals("i1/0", key)
        assertEquals("choose-b", input.action)
        assertEquals(1L, input.expectedVersion)
        // Answered: the choice says so and the others no longer send.
        rule.onAllNodesWithText("已选").assertCountEquals(1)
        rule.onNodeWithText("A 重写同步层").performClick()
        rule.waitForIdle()
        assertEquals(1, api.sent.size)
    }

    @Test fun aConfirmShowsWhatGoesAndAChangedCardAsksAgain() {
        val api = Api()
        api.answer = { _, _ -> AssistantViewAnswer.ViewChanged(null, null) }
        val h = host(api)
        val card = """{"id":"view_1","version":3,"view":{"fallback":"删除旧分支","sections":[{"items":[
            {"type":"button","title":"删除","role":"destructive","action":{"id":"del","reply":"删除旧分支","confirm":"删除后不能恢复"}}]}]}}"""
        h.store.view("c1", json(card))
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantLiveCard("view_1") }
            }
        }
        rule.onNodeWithText("删除").performClick()
        rule.waitForIdle()
        assertTrue("Nothing goes before the confirm", api.sent.isEmpty())
        rule.onNodeWithText("删除后不能恢复\n\n将发送：删除旧分支").assertExists()
        confirmDialog()
        rule.waitForIdle()
        assertEquals(1, api.sent.size)
        assertEquals(true, api.sent.single().second.confirmed)
        assertEquals(3L, api.sent.single().second.expectedVersion)
        // The card changed: not sent again by itself; the next tap asks again, at the new version.
        h.store.view("c1", json(card.replace("\"version\":3", "\"version\":4")))
        rule.waitForIdle()
        assertEquals(1, api.sent.size)
        rule.onNodeWithText("删除").performClick()
        rule.waitForIdle()
        confirmDialog()
        rule.waitForIdle()
        assertEquals(listOf(3L, 4L), api.sent.map { it.second.expectedVersion })
    }

    /** The dialog's own "Delete" (the last of the card's button, the dialog's title and its button). */
    private fun confirmDialog() {
        val all = rule.onAllNodesWithText("删除")
        all[all.fetchSemanticsNodes().size - 1].performClick()
    }

    @Test fun searchFilterAndMoreAnswerOnlyWhenTapped() {
        val api = Api()
        val h = host(api)
        h.store.block("c1", "i1/0")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i1/0", searchCard, complete = true) }
            }
        }
        rule.mainClock.advanceTimeBy(2_000)
        rule.waitForIdle()
        assertTrue("Drawing 加载更多 or the search box sends nothing", api.sent.isEmpty())
        rule.onNodeWithText("再来一批").performClick()
        rule.waitForIdle()
        assertEquals("more", api.sent.single().second.action)
        assertEquals(null, api.sent.single().second.values)
    }

    @Test fun aFilterSendsTheSearchWithIt() {
        val api = Api()
        val h = host(api)
        h.store.block("c1", "i1/0")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i1/0", searchCard, complete = true) }
            }
        }
        rule.onAllNodesWithText("工作")[0].performClick()
        rule.waitForIdle()
        rule.onNodeWithText("全部").performClick()
        rule.waitForIdle()
        val (_, input) = api.sent.single()
        assertEquals("pick", input.action)
        assertEquals(mapOf("folder" to "all", "q" to "周报"), input.values)
    }

    // A hub that did not say assistant_views gets no request; once its hello says so the same card answers, with the
    // version the user saw.
    @Test fun aCardAnswersOnlyOnAHubThatSaysItTakesCards() {
        val api = Api()
        api.features.value = emptySet()
        val h = host(api)
        h.store.block("c1", "i1/0")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i1/0", example("decision"), complete = true) }
            }
        }
        rule.onNodeWithText("B 队列去重").performClick()
        rule.waitForIdle()
        assertTrue(api.sent.isEmpty())
        rule.onAllNodesWithText("已选").assertCountEquals(0)
        api.features.value = setOf("assistant_views")
        rule.waitForIdle()
        rule.onNodeWithText("B 队列去重").performClick()
        rule.waitForIdle()
        assertEquals(1, api.sent.size)
        assertEquals(1L, api.sent.single().second.expectedVersion)
        rule.onAllNodesWithText("已选").assertCountEquals(1)
    }

    @Test fun aReadOnlyChatSendsNothingOnAHubThatTakesCards() {
        val api = Api()
        val h = host(api, interactive = false)
        h.store.block("c1", "i1/0")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) {
                    AssistantBlock("i1/0", example("decision"), complete = true)
                    AssistantBlock("i1/1", searchOnlyCard, complete = true)
                }
            }
        }
        rule.onNodeWithText("B 队列去重").performClick()
        rule.waitForIdle()
        assertTrue(api.sent.isEmpty())
        rule.onAllNodesWithText("搜索").assertCountEquals(0)
    }

    @Test fun aSearchOnlyCardSendsOnOneTapOfSearch() {
        val api = Api()
        val h = host(api)
        h.store.block("c1", "i1/1")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i1/1", searchOnlyCard, complete = true) }
            }
        }
        rule.onNode(androidx.compose.ui.test.hasSetTextAction()).performTextInput("示例")
        rule.mainClock.advanceTimeBy(2_000)
        rule.waitForIdle()
        assertTrue("Typing and pausing sends nothing", api.sent.isEmpty())
        rule.onNodeWithText("搜索").performClick()
        rule.waitForIdle()
        val (key, input) = api.sent.single()
        assertEquals("i1/1", key)
        assertEquals("who", input.action)
        assertEquals(mapOf("q" to "示例"), input.values)
        assertEquals(1L, input.expectedVersion)
        // Answered: the box and its button are gone; nothing more can go.
        rule.onAllNodesWithText("搜索").assertCountEquals(0)
        assertEquals(1, api.sent.size)
    }

    @Test fun aBlockTheHubHasNotRegisteredOnlyDraws() {
        val api = Api()
        val h = host(api)
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                CompositionLocalProvider(LocalAssistantCards provides h) { AssistantBlock("i9/0", example("decision"), complete = true) }
            }
        }
        rule.onNodeWithText("B 队列去重").performClick()
        rule.waitForIdle()
        assertTrue(api.sent.isEmpty())
        // A read-only device's card the same, even when registered.
        val readOnly = host(api, interactive = false)
        readOnly.store.block("c1", "i9/0")
        assertTrue(api.sent.isEmpty())
    }
}
