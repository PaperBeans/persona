package persona.feature.chat

import android.content.Context
import androidx.compose.runtime.saveable.SaverScope
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.Question
import persona.core.contracts.SessionKey
import persona.core.designsystem.DecisionKind
import persona.core.designsystem.Tokens

/**
 * The cards above a chat's composer (#4210 T6, #4339 C10, iOS `PendingCards` and `TaskQuestions`): their order, when they
 * stop fitting, each card's fold, and which of a Persona conversation's cards are in its stack.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class PendingStackTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val chat = SessionKey("hub", "c1")
    private val bash = Interaction("ix_bash", machineId = "hub", sessionId = "c1", kind = "permission", toolName = "Bash",
        createdAt = "2026-11-12T09:00:00Z")
    private val older = Interaction("ix_old", machineId = "mac", sessionId = "t1", kind = "question", createdAt = "2026-11-12T08:00:00Z",
        questions = listOf(Question("Which branch?")))
    private val newer = Interaction("ix_new", machineId = "mac", sessionId = "t2", kind = "permission", toolName = "Edit",
        createdAt = "2026-11-12T08:30:00Z")
    private val other = Interaction("ix_other", machineId = "mac", sessionId = "agent", kind = "plan", createdAt = "2026-11-12T07:00:00Z")
    private val tasks = setOf(SessionKey("mac", "t1"), SessionKey("mac", "t2"))
    private fun title(card: Interaction) = mapOf("t1" to "Fix the importer", "t2" to "Ship the release", "agent" to "Crash logs")[card.sessionId]

    @Test fun permissionsComeFirstThenTheNewest() {
        val q1 = Interaction("q1", kind = "question", createdAt = "2026-11-12T08:00:00Z")
        val q2 = Interaction("q2", kind = "question", createdAt = "2026-11-12T09:00:00Z")
        val p1 = Interaction("p1", kind = "permission", createdAt = "2026-11-12T07:00:00Z")
        val plan = Interaction("plan", kind = "plan")
        assertEquals(listOf("p1", "q2", "q1", "plan"), PendingStack.urgentFirst(listOf(q1, p1, plan, q2)).map { it.id })
        // no time: the later one first, as iOS's offset
        val a = Interaction("a", kind = "question")
        val b = Interaction("b", kind = "question")
        assertEquals(listOf("b", "a"), PendingStack.urgentFirst(listOf(a, b)).map { it.id })
    }

    @Test fun theyKeepTheirOrderWhileTheyFitAndTheUrgentLeadOnceTheyScroll() {
        val cards = listOf(older, newer, other)
        assertEquals(cards, PendingStack.order(cards, scrolling = false))
        assertEquals(listOf(newer, older, other), PendingStack.order(cards, scrolling = true))
        // the card opened from a notification leads in both
        assertEquals(listOf(other, older, newer), PendingStack.order(cards, scrolling = false, first = other.id))
        assertEquals(listOf(other, newer, older), PendingStack.order(cards, scrolling = true, first = other.id))
    }

    @Test fun theyTakeHalfThePageAndWithTheKeyboardUpWhatTheConversationHas() {
        assertEquals(380.dp, PendingStack.cap(760.dp, keyboard = false, rest = 120.dp, header = 100.dp, floor = 24.dp))
        // an open tray takes its room first: the composer never leaves the page
        assertEquals(136.dp, PendingStack.cap(760.dp, keyboard = false, rest = 500.dp, header = 100.dp, floor = 24.dp))
        assertEquals(156.dp, PendingStack.cap(400.dp, keyboard = true, rest = 120.dp, header = 100.dp, floor = 24.dp))
        // never less than a finger's height, however little the keyboard leaves
        assertEquals(Tokens.MinTouch.android, PendingStack.cap(240.dp, keyboard = true, rest = 120.dp, header = 100.dp, floor = 24.dp))
        // the footer not measured yet: half, as without the keyboard
        assertEquals(200.dp, PendingStack.cap(400.dp, keyboard = true, rest = 0.dp, header = 100.dp, floor = 24.dp))
    }

    @Test fun eachCardFoldsOnItsOwn() {
        val folds = RequestFolds()
        assertFalse(folds.isFolded(bash.id))
        folds.set(bash.id, true)
        assertTrue(folds.isFolded(bash.id))
        assertFalse(folds.isFolded(older.id))
        folds.set(older.id, true)
        folds.open(bash.id)
        assertFalse(folds.isFolded(bash.id))
        assertTrue(folds.isFolded(older.id))
        // kept across a configuration change
        val saved = with(RequestFolds.Saver) { SaverScope { true }.save(folds) }!!
        assertEquals(setOf(older.id), RequestFolds.Saver.restore(saved)!!.folded)
    }

    @Test fun personasStackHoldsItsOwnCardsAndTheOldestQuestionOfItsTasks() {
        val stack = assistantStack(listOf(bash, newer, older), chat, tasks, reviewed = null, titleOf = ::title)
        assertEquals(listOf(older, bash), stack.cards)
        assertEquals(mapOf(older.id to StackLabel("Fix the importer", more = 1)), stack.labels)
        // answered, the next one comes up
        val next = assistantStack(listOf(bash, newer), chat, tasks, reviewed = null, titleOf = ::title)
        assertEquals(listOf(newer, bash), next.cards)
        assertEquals(StackLabel("Ship the release", more = 0), next.labels[newer.id])
    }

    @Test fun aCardTheTrayOpenedComesFirstAndIsNeverDrawnTwice() {
        val opened = assistantStack(listOf(bash, older), chat, tasks, reviewed = other, titleOf = ::title)
        assertEquals(listOf(other, older, bash), opened.cards)
        assertEquals(StackLabel("Crash logs"), opened.labels[other.id])
        assertEquals(listOf(older, bash), assistantStack(listOf(bash, older), chat, tasks, reviewed = older, titleOf = ::title).cards)
        assertEquals(listOf(older, bash), assistantStack(listOf(bash, older), chat, tasks, reviewed = bash, titleOf = ::title).cards)
    }

    @Test fun withNothingAskedTheStackIsEmpty() {
        assertTrue(assistantStack(emptyList(), chat, tasks, reviewed = null, titleOf = ::title).cards.isEmpty())
    }

    @Config(qualifiers = "zh")
    @Test fun itSaysItWaitsForYourApprovalInChinese() {
        assertEquals("在等你批准", res.getString(DecisionKind.Command.labelRes))
        assertEquals("在等你批准", res.getString(DecisionKind.Tool.labelRes))
        assertEquals("3 个待处理", res.getQuantityString(persona.core.designsystem.R.plurals.designsystem_decision_pending, 3, 3))
        assertEquals("还有 2 件", res.getQuantityString(R.plurals.chat_stack_more, 2, 2))
    }

    @Config(qualifiers = "en")
    @Test fun itSaysItWaitsForYourApprovalInEnglish() {
        assertEquals("Waiting for your approval", res.getString(DecisionKind.Command.labelRes))
        assertEquals("3 pending", res.getQuantityString(persona.core.designsystem.R.plurals.designsystem_decision_pending, 3, 3))
        assertEquals("1 more", res.getQuantityString(R.plurals.chat_stack_more, 1, 1))
    }

    @Config(qualifiers = "ja")
    @Test fun itSaysItWaitsForYourApprovalInJapanese() {
        assertEquals("承認を待っています", res.getString(DecisionKind.Command.labelRes))
        assertEquals("保留中3件", res.getQuantityString(persona.core.designsystem.R.plurals.designsystem_decision_pending, 3, 3))
        assertEquals("ほか2件", res.getQuantityString(R.plurals.chat_stack_more, 2, 2))
    }
}
