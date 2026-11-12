package persona.feature.assistant

import android.content.res.Resources
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantConfig

/** One item of the title menu (iOS `AssistantTopBar.menuItems`). */
internal enum class AssistantMenuItem { ConnectionDetails, Knowledge, Details, Voice, BackToMain, CopyDirectory, CopyInfo,
    CopySessionId, CopyDiagnostics, Compact, NewMain, SessionInfo, Interrupt }

/**
 * The title menu's items in iOS's order and sections (#4339 C18): while the hub is out of reach Connection details,
 * then Knowledge, Details (the assistant's own page, iOS's profile), Voice chat (Android's, while the hub runs a voice
 * extension) and Back to main chat (off the main conversation); the copies (the directory and the info always, the
 * session ID and diagnostics with a conversation open); the main conversation's Compact context and New main chat; Session
 * info and, while it works, Interrupt. The per-conversation launcher pick is the stored pick's (#5055), and Screen needs
 * a live screen Android doesn't have. Each list is one section; a rule goes above every section after the first.
 */
internal fun assistantMenu(offline: Boolean, voice: Boolean, main: SessionKey?, active: SessionKey?, compact: Boolean,
                           newMain: Boolean, working: Boolean): List<List<AssistantMenuItem>> = listOf(
    listOfNotNull(AssistantMenuItem.ConnectionDetails.takeIf { offline }, AssistantMenuItem.Knowledge, AssistantMenuItem.Details,
        AssistantMenuItem.Voice.takeIf { voice }, AssistantMenuItem.BackToMain.takeIf { main != null && main != active }),
    listOfNotNull(AssistantMenuItem.CopyDirectory, AssistantMenuItem.CopyInfo,
        AssistantMenuItem.CopySessionId.takeIf { active != null }, AssistantMenuItem.CopyDiagnostics.takeIf { active != null }),
    listOfNotNull(AssistantMenuItem.Compact.takeIf { compact }, AssistantMenuItem.NewMain.takeIf { newMain }),
    listOfNotNull(AssistantMenuItem.SessionInfo.takeIf { active != null }, AssistantMenuItem.Interrupt.takeIf { active != null && working }),
).filter { it.isNotEmpty() }

/** Where Persona keeps its files when neither the conversation nor the settings say (iOS `AssistantCopyText.cwd`). */
internal const val AssistantDefaultCwd = "~/.persona/assistant"

/**
 * What Copy directory and Copy info read (iOS `AssistantCopyText`) for [session], the conversation on screen (null on a
 * blank page), with [config] the assistant's settings as last read and [assistantMachine] its machine.
 */
internal class AssistantCopyText(private val session: Session?, private val key: SessionKey?, private val main: Boolean,
                                 private val config: AssistantConfig?, private val assistantMachine: String,
                                 private val machines: List<Machine>) {
    /** Where the conversation runs. */
    val cwd: String get() = session?.cwd?.takeIf(String::isNotBlank) ?: config?.cwd?.takeIf(String::isNotBlank) ?: AssistantDefaultCwd

    /** The conversation's launcher: the main conversation follows the settings, another keeps its own. */
    val launcher: String get() {
        val configured = config?.launcher?.takeIf(String::isNotBlank)
        val own = session?.launcher?.takeIf(String::isNotBlank)
        return (if (main) configured ?: own else own ?: configured) ?: "claude"
    }

    fun info(res: Resources): String {
        val machineId = key?.machineId ?: config?.machineId?.takeIf(String::isNotBlank) ?: assistantMachine
        return assistantCopyInfo(res, session?.title?.takeUnless { it.isBlank() || it == "Claude Code" }, main, key?.sessionId,
            machines.firstOrNull { it.id == machineId }?.name, machineId, cwd, launcher, modelShown(session?.model))
    }
}

/** A model as the menu shows it (iOS `Fmt.model`): the last part of a provider path. */
internal fun modelShown(model: String?): String? = model?.takeIf(String::isNotBlank)?.substringAfterLast('/')

/**
 * Copy info's text (iOS `AssistantCopyText.info`): enough for another agent, or the user at a terminal, to find the
 * conversation. Which one it is, its session ID, the machine (its name and id when they differ), the working directory,
 * the launcher and model, and how to go on with it there.
 */
internal fun assistantCopyInfo(res: Resources, title: String?, main: Boolean, sessionId: String?, machine: String?,
                               machineId: String, cwd: String, launcher: String, model: String?): String {
    val name = listOfNotNull(res.getString(R.string.assistant_main_chat).takeIf { main },
        title?.let { res.getString(R.string.assistant_copy_info_quoted, it) }).joinToString(" ")
    val place = machine?.let { if (it == machineId) it else res.getString(R.string.assistant_copy_info_machine_id, it, machineId) } ?: machineId
    return listOfNotNull(
        when {
            sessionId == null -> res.getString(R.string.assistant_copy_info_hub)
            name.isEmpty() -> res.getString(R.string.assistant_copy_info_chat)
            else -> res.getString(R.string.assistant_copy_info_chat_named, name)
        },
        sessionId?.let { res.getString(R.string.assistant_copy_info_session, it) },
        res.getString(R.string.assistant_copy_info_machine, place),
        res.getString(R.string.assistant_copy_info_cwd, cwd),
        model?.let { res.getString(R.string.assistant_copy_info_launcher_model, launcher, it) }
            ?: res.getString(R.string.assistant_copy_info_launcher, launcher),
        sessionId?.let { res.getString(R.string.assistant_copy_info_resume, cwd, it) },
    ).joinToString("\n")
}
