package persona.core.domain

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.runners.Parameterized
import persona.core.contracts.Item

/** Tool names, subjects and arguments (#4339 C1) against spec/fixtures/tool-names, iOS's `ToolNamingTests` cases. */
@RunWith(Parameterized::class)
class ToolNamingFixtureTest(private val name: String, private val fixture: FixtureReader.Case) {
    @Test fun reads() {
        val case = fixture.value
        val input = case["input"] ?: JsonNull
        val expected = case.getValue("expected")
        fun text() = input.jsonPrimitive.content
        fun wanted() = if (expected == JsonNull) null else expected.jsonPrimitive.content
        when (val check = case.getValue("check").jsonPrimitive.content) {
            "humanize" -> assertEquals(name, wanted(), ToolNaming.humanize(text()))
            "server_name" -> assertEquals(name, wanted(), ToolNaming.serverName(text()))
            "table_name" -> assertEquals(name, wanted(), ToolNaming.tableName(text()))
            "mcp_label" -> {
                assertEquals(name, wanted(), ToolNaming.mcpLabel(text()))
                // The row says the same words.
                wanted()?.let { assertEquals(name, it, toolVerb(text()).zh()) }
            }
            "subject" -> assertEquals(name, wanted(), ToolNaming.subject(input))
            "tool_subject" -> assertEquals(name, wanted(), toolSubject(Item("call", kind = "tool_use", toolName = tool(), toolInput = input)))
            "command_line" -> assertEquals(name, wanted(), ToolNaming.commandLine(tool(), input))
            "arguments" -> {
                val skipping = case["skipping"]?.jsonArray?.map { it.jsonPrimitive.content }?.toSet().orEmpty()
                val want = expected.jsonArray.map { it.jsonObject.argument() }
                assertEquals(name, want, ToolNaming.arguments(input, skipping))
            }
            else -> error("Unknown check $check in $name")
        }
    }

    private fun tool() = fixture.value.getValue("tool").jsonPrimitive.content

    private fun JsonObject.argument() = ToolNaming.Argument(
        getValue("id").jsonPrimitive.content, getValue("label").jsonPrimitive.content, getValue("value").jsonPrimitive.content,
        getValue("code").jsonPrimitive.boolean, getValue("lines").jsonPrimitive.int,
    )

    companion object {
        @JvmStatic @Parameterized.Parameters(name = "{0}")
        fun cases(): List<Array<Any>> = FixtureReader.read("tool-names").map {
            arrayOf(it.value.getValue("name").jsonPrimitive.content, it)
        }
    }
}
