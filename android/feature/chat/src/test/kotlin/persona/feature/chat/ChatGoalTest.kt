package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.GoalInfo

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh")
class ChatGoalTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun everyStatusHasItsWords() {
        val labels = listOf("active", "complete", "paused", "blocked", "usage_limited", "budget_limited", "later")
            .map { goalStatusLabel(res, GoalInfo(objective = "x", status = it)) }
        assertEquals(listOf("进行中", "已达成", "已暂停", "受阻", "用量到上限", "预算用完", "later"), labels)
    }

    @Test fun usageSaysTokensBudgetAndTime() {
        assertEquals("1.2 万 / 50 万 tokens · 10 分钟",
            goalUsage(res, GoalInfo(objective = "x", status = "active", tokensUsed = 12345, tokenBudget = 500_000, seconds = 600)))
        assertEquals("950 tokens · 1 分钟", goalUsage(res, GoalInfo(objective = "x", tokensUsed = 950, tokenBudget = 0, seconds = 20)))
        assertEquals("2 小时 5 分", goalUsage(res, GoalInfo(objective = "x", seconds = 7500)))
        assertNull(goalUsage(res, GoalInfo(objective = "x", tokensUsed = 0)))
    }

    @Config(qualifiers = "en")
    @Test fun usageCountsTokensInEnglishUnits() {
        assertEquals("12.3K / 500K tokens", goalUsage(res, GoalInfo(objective = "x", tokensUsed = 12345, tokenBudget = 500_000)))
        assertEquals("1.2M / 5M tokens", goalUsage(res, GoalInfo(objective = "x", tokensUsed = 1_200_000, tokenBudget = 5_000_000)))
    }
}
