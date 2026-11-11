package persona.core.designsystem

import org.junit.Assert.assertEquals
import org.junit.Test

/** How many option buttons of a decision stay on its one line (#3804): what does not fit goes to the menu, never to a second line. */
class DecisionAnswersTest {
    @Test fun everyOptionThatFitsStaysOnTheLine() {
        assertEquals(3, fittingOptions(listOf(100f, 100f, 100f), available = 320f, gap = 8f))
        assertEquals(2, fittingOptions(listOf(100f, 100f, 100f), available = 310f, gap = 8f))
        assertEquals(2, fittingOptions(listOf(100f, 100f, 100f), available = 208f, gap = 8f))
    }

    @Test fun theFirstOptionAlwaysStaysEvenWhenItIsWiderThanTheLine() {
        assertEquals(1, fittingOptions(listOf(400f, 50f), available = 300f, gap = 8f))
        assertEquals(1, fittingOptions(listOf(100f, 100f), available = 0f, gap = 8f))
        assertEquals(1, fittingOptions(listOf(100f), available = -40f, gap = 8f))
    }

    @Test fun anOptionThatDoesNotFitHidesTheOnesAfterItToo() {
        // the order is the hub's: a later, shorter option does not jump ahead of one that went to the menu
        assertEquals(1, fittingOptions(listOf(100f, 300f, 40f), available = 250f, gap = 8f))
    }

    @Test fun nothingToFit() {
        assertEquals(0, fittingOptions(emptyList(), available = 300f, gap = 8f))
    }
}
