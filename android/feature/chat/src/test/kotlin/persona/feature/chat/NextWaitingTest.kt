package persona.feature.chat

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.Resolution
import persona.core.contracts.SessionKey

/** Which card another chat waits on you with is offered after this chat's last one (#4325, web #4091). */
class NextWaitingTest {
    private fun w(id: String, machine: String, session: String) = WaitingRequest(SessionKey(machine, session), id, "who-$id", "what-$id")
    private val here = SessionKey("m", "s")

    @Test fun theFirstCardOfAnotherChatInTheListsOrderCountingEveryCardOutsideThisChat() {
        val all = listOf(w("a", "m", "s"), w("b", "m", "t"), w("c", "m", "s"), w("d", "n", "u"))
        assertEquals(NextWaiting(all[1], 2), nextWaiting(all, here))
    }

    @Test fun theSameSessionIdOnAnotherComputerIsAnotherChat() {
        assertEquals("a", nextWaiting(listOf(w("a", "n", "s")), here)?.request?.requestId)
    }

    @Test fun nothingWhenOnlyThisChatHasCardsOrNoneDoes() {
        assertNull(nextWaiting(listOf(w("a", "m", "s")), here))
        assertNull(nextWaiting(emptyList(), here))
    }

    @Test fun onlyAnAnswerThatWentThroughOrOneTheHubHadAlreadyOffersTheNext() {
        val pick = Resolution(allow = true)
        assertTrue(InteractionReply(InteractionSubmission.Answered).offersNext(pick))
        assertTrue(InteractionReply(InteractionSubmission.AlreadyAnswered, effective = pick).offersNext(pick))
        // handed back to the computer, a changed screen (a new card comes at once), expired / withdrawn, not delivered
        assertFalse(InteractionReply(InteractionSubmission.Answered).offersNext(Resolution(dismiss = true)))
        assertFalse(InteractionReply(InteractionSubmission.PromptChanged).offersNext(pick))
        for (ended in listOf("expired", "cancelled", "dismissed", null))
            assertFalse(InteractionReply(InteractionSubmission.ResolvedElsewhere, endedStatus = ended).offersNext(pick))
        assertFalse(InteractionReply(InteractionSubmission.Unknown).offersNext(pick))
    }

    @Test fun showsOnlyRightAfterAnAnswerHereWithNoCardLeftTheKeyboardDownAndSomewhereToGo() {
        assertTrue(showsNextWaiting(offered = true, cardsHere = 0, readOnly = false, canOpen = true, keyboard = false))
        assertFalse(showsNextWaiting(offered = false, cardsHere = 0, readOnly = false, canOpen = true, keyboard = false))
        assertFalse(showsNextWaiting(offered = true, cardsHere = 1, readOnly = false, canOpen = true, keyboard = false))
        assertFalse(showsNextWaiting(offered = true, cardsHere = 0, readOnly = true, canOpen = true, keyboard = false))
        assertFalse(showsNextWaiting(offered = true, cardsHere = 0, readOnly = false, canOpen = false, keyboard = false))
        assertFalse(showsNextWaiting(offered = true, cardsHere = 0, readOnly = false, canOpen = true, keyboard = true))
    }
}
