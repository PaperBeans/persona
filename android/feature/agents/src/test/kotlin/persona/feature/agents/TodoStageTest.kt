package persona.feature.agents

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.contracts.JsonCodec
import persona.core.contracts.Todo

/** A to-do's stage (#4636), as iOS `TodoStageTests`: the hub's names in order, nothing for none or an unknown one. */
class TodoStageTest {
    @Test fun theHubsStagesInOrder() {
        assertEquals(listOf("plan", "develop", "review", "merge", "deploy", "verify"), TodoStage.entries.map { it.wire })
        assertEquals(TodoStage.Review, TodoStage.of("review"))
        assertNull(TodoStage.of(null))
        assertNull(TodoStage.of("ship"))
        assertEquals("develop", JsonCodec.decodeFromString<Todo>("""{"id":"t","stage":"develop"}""").stage)
        assertNull(JsonCodec.decodeFromString<Todo>("""{"id":"t"}""").stage)
    }
}
