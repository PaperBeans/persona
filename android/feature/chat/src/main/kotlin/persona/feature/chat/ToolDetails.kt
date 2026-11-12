package persona.feature.chat

import androidx.compose.foundation.clickable
import androidx.compose.ui.res.stringResource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.selection.DisableSelection
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.Item
import persona.core.designsystem.AppStyle
import persona.core.designsystem.JIcon
import persona.core.designsystem.JTag
import persona.core.designsystem.JTextButton
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.ToolNaming
import persona.core.domain.toolDetail
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull

// What an opened tool step shows above its output, by tool (iOS `ToolDetail`): a diff under the file it changed, a
// path to open, the plan as Markdown, the questions, the sub-agent's brief, a shell's command line; anything else as
// its input, on Glass as its arguments in words.

/** The input part of an opened tool step that has no diff and no checklist. */
@Composable
internal fun ToolInputDetail(item: Item, result: Item? = null) {
    val input = item.toolInput as? JsonObject
    val plain: @Composable () -> Unit = { CodeText(toolDetail(item), stringResource(R.string.chat_tool_details)) }
    // iOS's default case (#2986, #4339 C1): the input as `key: value` lines in words, its JSON under Raw input (the
    // reader actions below); Glass only, Brief and Board keep the input as code (decision 8).
    val listed: @Composable () -> Unit = {
        if (PersonaTheme.style == AppStyle.Glass && ToolArgumentRows.lists(item.toolName.orEmpty())) {
            val arguments = remember(item.toolInput) { ToolNaming.arguments(item.toolInput) }
            if (arguments.isNotEmpty()) ToolArguments(arguments) else plain()
        } else plain()
    }
    if (PersonaTools.isAskUser(item.toolName.orEmpty())) {
        AskUserAnswers.questions(input).takeIf { it.isNotEmpty() }?.let { AskUserDetail(it, result) } ?: plain()
        return
    }
    when (item.toolName) {
        "Read", "NotebookEdit" -> (input?.text("file_path") ?: input?.text("notebook_path"))?.takeIf { it.isNotBlank() }
            ?.let { PathLink(it) } ?: plain()
        "ExitPlanMode" -> input?.text("plan")?.takeIf { it.isNotBlank() }?.let { PlanDetail(it, item.id) } ?: plain()
        "AskUserQuestion" -> toolQuestions(item).takeIf { it.isNotEmpty() }?.let { QuestionsDetail(it) } ?: plain()
        "Task", "Agent" -> input?.text("prompt")?.takeIf { it.isNotBlank() }?.let { TaskDetail(it, input?.text("subagent_type")) } ?: plain()
        else -> listed()
    }
}

/** Which opened tools list their arguments on Glass: any but those whose detail is their own (iOS `ToolDetail`). */
private object ToolArgumentRows {
    /** A command line, a diff, a computer's steps (iOS `ComputerToolDetail`, which Android doesn't draw yet: its input as code). */
    private val own = setOf("Bash", "PowerShell", "Monitor", "Edit", "MultiEdit", "Write", "computer_run", "computer_state")

    fun lists(name: String): Boolean {
        val lower = name.lowercase()
        return ToolNaming.tableName(name) !in own && !lower.startsWith("mcp__cua") && "cua-driver" !in lower && "cua_driver" !in lower
    }
}

/**
 * An unknown tool's input as a short key/value list (iOS `ToolArgumentsDetail`, `ToolNaming.arguments`): each field in
 * words in a column as wide as its widest label (at most [Tokens.Size.box120], scaled with the text), the value beside it
 * on the label's baseline, a token, a number or JSON in the code face, a sentence in the text face, a few lines at most.
 * The values can be selected; each row reads as one ("Title, Crash on launch").
 */
@Composable
internal fun ToolArguments(arguments: List<ToolNaming.Argument>) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val density = LocalDensity.current
    val measurer = rememberTextMeasurer()
    val cap = Tokens.Size.box120 * density.fontScale
    val column = remember(arguments, type.micro, density) {
        with(density) { arguments.maxOf { measurer.measure(it.label, type.micro, maxLines = 1).size.width }.toDp() }
    }.coerceAtMost(cap)
    SelectionContainer {
        Column(Modifier.fillMaxWidth().semantics { testTag = "tool-arguments" }, verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            arguments.forEach { argument ->
                Row(Modifier.fillMaxWidth().semantics(mergeDescendants = true) {}, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    DisableSelection {
                        Text(argument.label, Modifier.width(column).alignByBaseline(), style = type.micro, color = palette.ink2,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    Text(argument.value, Modifier.weight(1f).alignByBaseline(), style = if (argument.code) type.code else type.caption,
                        color = palette.ink, maxLines = argument.lines, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}

/** A path in a tool's input; opens the file on the session's machine when the page can. */
@Composable
internal fun PathLink(path: String) {
    val open = LocalChatRowActions.current?.openPath
    val palette = PersonaTheme.palette
    val tint = if (open != null) palette.accent else palette.ink2
    val viewFile = stringResource(R.string.chat_tool_view_file)
    Row(
        Modifier.clip(PersonaTheme.shapes.controlShape)
            .then(if (open != null) Modifier.clickable(role = Role.Button, onClickLabel = viewFile) { open(path, null) } else Modifier)
            .padding(vertical = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs),
    ) {
        JIcon(fileIcon(path), null, size = Tokens.Space.l, tint = tint)
        Text(path, Modifier.weight(1f, fill = false), style = PersonaTheme.type.code, color = tint, maxLines = 2, overflow = TextOverflow.Ellipsis)
        if (open != null) JIcon(PhIcons.ArrowSquareOut, null, size = Tokens.Space.l, tint = tint)
    }
}

/** The file a diff is in, above the diff: its name, folder and +N −N; a tap opens it (iOS `DiffHeader`). */
@Composable
internal fun DiffHeader(path: String, added: Int, removed: Int) {
    val open = LocalChatRowActions.current?.openPath
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val viewFile = stringResource(R.string.chat_tool_view_file)
    Row(
        Modifier.fillMaxWidth().clip(PersonaTheme.shapes.controlShape)
            .then(if (open != null) Modifier.clickable(role = Role.Button, onClickLabel = viewFile) { open(path, null) } else Modifier)
            .padding(vertical = Tokens.Space.xs),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s),
    ) {
        JIcon(fileIcon(path), null, size = Tokens.Space.l, tint = palette.ink2)
        Text(fileLabel(path), style = type.caption.copy(fontWeight = FontWeight.SemiBold), color = palette.ink, maxLines = 1)
        Text(path.trimEnd('/').substringBeforeLast('/', ""), Modifier.weight(1f), style = type.caption, color = palette.ink2,
            maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (added > 0) Text("+$added", style = type.micro, color = palette.diffAdd, maxLines = 1)
        if (removed > 0) Text("−$removed", style = type.micro, color = palette.diffDel, maxLines = 1)
        if (open != null) JIcon(PhIcons.ArrowSquareOut, viewFile, size = Tokens.Space.l, tint = palette.accent)
    }
}

/** ExitPlanMode's plan as Markdown, the way it was proposed. */
@Composable
private fun PlanDetail(plan: String, id: String) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        ChatMarkdown(plan, "$id/plan", PersonaTheme.type.callout)
        ReaderEntry(plan, stringResource(R.string.chat_tool_plan_title))
    }
}

/** One question of an AskUserQuestion call, with its choices. */
internal data class ToolQuestion(val question: String, val header: String?, val multiSelect: Boolean, val options: List<Pair<String, String?>>)

/** The questions an AskUserQuestion call asked (empty when its input has none). */
internal fun toolQuestions(item: Item): List<ToolQuestion> {
    val questions = (item.toolInput as? JsonObject)?.get("questions") as? JsonArray ?: return emptyList()
    return questions.mapNotNull { value ->
        val question = value as? JsonObject ?: return@mapNotNull null
        val text = question.text("question")?.takeIf { it.isNotBlank() } ?: return@mapNotNull null
        val multi = ((question["multiSelect"] ?: question["multi_select"]) as? JsonPrimitive)?.booleanOrNull == true
        val options = (question["options"] as? JsonArray).orEmpty().mapNotNull { option ->
            val choice = option as? JsonObject ?: return@mapNotNull null
            val label = choice.text("label")?.takeIf { it.isNotBlank() } ?: return@mapNotNull null
            label to choice.text("description")?.takeIf { it.isNotBlank() }
        }
        ToolQuestion(text, question.text("header")?.takeIf { it.isNotBlank() }, multi, options)
    }
}

@Composable
private fun QuestionsDetail(questions: List<ToolQuestion>) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        questions.forEachIndexed { index, question ->
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    question.header?.let { JTag(it) }
                    if (question.multiSelect) JTag(stringResource(R.string.chat_tool_multi_select))
                }
                Text((if (questions.size > 1) "${index + 1}. " else "") + question.question, style = type.callout, color = palette.ink)
                question.options.forEach { (label, description) ->
                    Text("· " + (description?.let { stringResource(R.string.chat_label_value, label, it) } ?: label), style = type.caption, color = palette.ink2)
                }
            }
        }
    }
}

/**
 * The assistant's ask_user once opened (iOS `AskUserDetail`): each question with its options, what the user picked
 * ticked. The card is gone once answered, so this is where the conversation keeps what was asked and what came back.
 */
@Composable
private fun AskUserDetail(questions: List<AskUserQuestion>, result: Item?) {
    val palette = PersonaTheme.palette
    val type = PersonaTheme.type
    val answers = remember(questions, result?.text) { result?.text?.let { AskUserAnswers.parse(it, questions) }.orEmpty() }
    Column(Modifier.fillMaxWidth().semantics(mergeDescendants = false) { testTag = "ask-user-detail" },
        verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
        questions.forEach { question ->
            val answer = answers[question.question]
            Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                Text(question.question, style = type.callout.copy(fontWeight = FontWeight.Medium), color = palette.ink)
                question.options.forEach { label -> AskUserChoice(label, AskUserAnswers.picked(label, answer, question.multiSelect)) }
                AskUserAnswers.typed(question, answer)?.let { AskUserChoice(it, picked = true) }
            }
        }
        if (result != null && result.isError != true && answers.isEmpty())
            Text(stringResource(R.string.chat_tool_no_answer), style = type.caption, color = palette.ink2)
    }
}

@Composable
private fun AskUserChoice(label: String, picked: Boolean) {
    val palette = PersonaTheme.palette
    Row(Modifier.semantics(mergeDescendants = true) { selected = picked },
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
        JIcon(if (picked) PhIcons.CheckCircle else PhIcons.CircleDashed, null, size = Tokens.Space.l,
            tint = if (picked) palette.accent else palette.ink3, selected = picked)
        Text(label, style = PersonaTheme.type.caption, color = if (picked) palette.ink else palette.ink2)
    }
}

/**
 * "View sub-agent" under a Task / Agent step in Brief and Board (Glass draws the task card instead): its sub-agent's
 * read-only conversation, running or done; nothing when the session does not list a sub-agent for the call or it cannot
 * be opened here.
 */
@Composable
internal fun SubagentEntry(toolUseId: String?) {
    val links = LocalChatSubagents.current ?: return
    val open = links.open ?: return
    val target = toolUseId?.let { links.keys[it] } ?: return
    JTextButton(stringResource(R.string.chat_tool_open_subagent), { open(target) }, icon = PhIcons.ArrowSquareOut)
}

/** What a sub-agent was asked to do: its brief, the first lines of it, all of it in the reader. */
@Composable
private fun TaskDetail(prompt: String, agentType: String?) {
    val long = remember(prompt) { prompt.lineSequence().count() > 8 || prompt.length > 600 }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        agentType?.takeIf { it.isNotBlank() }?.let { JTag(it) }
        Text(prompt, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 8, overflow = TextOverflow.Ellipsis)
        ReaderEntry(prompt, stringResource(R.string.chat_tool_task_brief), always = long)
    }
}

private fun JsonObject.text(key: String): String? = (get(key) as? JsonPrimitive)?.contentOrNull
