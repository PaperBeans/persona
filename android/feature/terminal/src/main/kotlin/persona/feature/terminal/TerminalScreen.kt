package persona.feature.terminal

import android.content.ClipboardManager
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.*
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.repeatOnLifecycle
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.terminal.ModifierState
import persona.core.terminal.TerminalModifiers

@Composable
fun TerminalScreen(model: TerminalViewModel, onBack: () -> Unit, sessionTitle: String = model.key.sessionId) {
    val state by model.state.collectAsStateWithLifecycle()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val context = LocalContext.current
    var keyboard by remember { mutableStateOf(false) }
    var wrap by remember { mutableStateOf(true) }
    var canvas by remember { mutableStateOf<TerminalCanvasView?>(null) }
    var paste by remember { mutableStateOf<String?>(null) }
    var selecting by remember { mutableStateOf<String?>(null) }
    var display by remember { mutableStateOf(false) }
    val active = state.driving
    // Keys work whenever the hub is reachable: straight into the stream while driving, through /keys while watching.
    val typing = state.online
    val palette = PersonaTheme.palette
    val feedback = LocalAppFeedback.current
    LaunchedEffect(model, lifecycle) { lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { model.run() } }
    val resources = LocalResources.current
    LaunchedEffect(model, feedback, resources) { model.notices.collect { feedback?.notice(resources.getString(it.text)) } }
    LaunchedEffect(model, feedback, resources) {
        model.resumeFailures.collect { feedback?.notice(it ?: resources.getString(R.string.terminal_resume_failed)) }
    }
    LaunchedEffect(typing) { if (!typing) { keyboard = false; paste = null } }
    DisposableEffect(model) { onDispose { canvas?.keyboard(false) } }
    fun pasteClipboard() {
        val clip = context.getSystemService(ClipboardManager::class.java).primaryClip ?: return
        if (clip.itemCount == 0) return
        val text = clip.getItemAt(0).coerceToText(context).toString()
        if (text.contains('\n') || text.contains('\r')) paste = text else model.type(text)
    }
    // A terminal redraws all the time (every frame of the program, every wheel line): glass over it re-blurred its
    // bars and edges on each of those frames, on top of a slow link (2026-11-01). Its page is opaque in every style.
    CompositionLocalProvider(LocalGlassLevel provides GlassLevel.Solid) {
    JPage(sessionTitle, modifier = Modifier.imePadding(), topBar = {
        JTopBar(sessionTitle, subtitle = terminalSubtitle(state), onBack = onBack, actions = {
            val watching = active || state.control == TerminalControl.Starting
            // Taking over needs the stream: a screen read once a second (an old hub or node) can only be watched.
            JChip(stringResource(when {
                active -> R.string.terminal_control_active; watching -> R.string.terminal_control_starting; else -> R.string.terminal_control_watching
            }), selected = active,
                onClick = if (watching) ({ model.watch() }) else if (state.online && state.streaming) ({ model.takeOver() }) else null)
            JIconButton(PhIcons.Copy, stringResource(R.string.terminal_select_text), { selecting = model.screenText() }, enabled = state.loaded)
            JIconButton(PhIcons.SlidersHorizontal, stringResource(R.string.terminal_display_settings), { display = true })
        })
    }, bottomBar = {
        TerminalKeyBar(state.modifiers, typing, { key, action, quick -> if (typing) model.modifier(key, action, quick) }, model::press, keyboard,
            toggleKeyboard = { if (typing) keyboard = !keyboard }, paste = { if (typing) pasteClipboard() })
    }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            // OSC 8 links open where every link opens: the shell's in-app card (other schemes are confirmed first).
            val uriHandler = LocalUriHandler.current
            // The notice sits above the terminal, never over its text.
            val notRunning = state.note == TerminalNote.NoTerminal
            state.note?.takeUnless { notRunning }?.let { note -> JNotice(stringResource(note.text), Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s),
                actionLabel = stringResource(R.string.terminal_retry), onAction = model::retry) }
            Box(Modifier.fillMaxWidth().weight(1f).background(palette.terminalBackground)) {
                AndroidView(factory = { ctx -> TerminalCanvasView(ctx, palette.terminalText.toArgb(), palette.terminalBackground.toArgb()).also { view ->
                    canvas = view
                    view.onGrid = model::grid; view.onText = model::type; view.onKey = model::press
                    view.onHardware = model::hardware; view.onMarked = model::marked; view.onWheel = model::wheel; view.onCell = model::click
                    view.onLink = { url -> runCatching { uriHandler.openUri(url) } }
                } }, modifier = Modifier.fillMaxSize().clipToBounds(), update = { view ->
                    view.font(state.fontSize)
                    view.render(state, wrap, palette.terminalText.toArgb(), palette.terminalBackground.toArgb()); view.keyboard(keyboard && typing)
                }, onRelease = { it.keyboard(false); canvas = null })
                if (notRunning) TerminalNotRunning(state.resumable, state.resuming, model::resume, Modifier.matchParentSize())
                else if (!state.loaded) JSpinner(Modifier.align(Alignment.Center))
            }
        }
    }
    }
    if (state.control == TerminalControl.Busy) JDialog(onDismiss = { model.watch(remember = false) }, title = stringResource(R.string.terminal_busy_title),
        text = stringResource(R.string.terminal_busy_text),
        confirm = stringResource(R.string.terminal_take_over) to { model.takeOver(force = true) },
        dismiss = stringResource(R.string.terminal_control_watching) to { model.watch(remember = false) })
    paste?.let { text -> JDialog(onDismiss = { paste = null }, title = stringResource(R.string.terminal_paste_title),
        content = { Text(text, style = PersonaTheme.type.code, color = PersonaTheme.palette.ink, maxLines = 8, overflow = TextOverflow.Ellipsis) },
        confirm = stringResource(R.string.terminal_paste) to { model.type(text); paste = null }) }
    selecting?.let { text -> TerminalSelectText(text) { selecting = null } }
    if (display) TerminalDisplaySheet(state.fontSize, model::font, wrap, { wrap = it }, wrapEnabled = !active, onDismiss = { display = false })
}

/**
 * The session isn't running (#4345): in place of the terminal, as on iOS (#4328), with Resume session for a Claude Code
 * session; others resume with their next message.
 */
@Composable
private fun TerminalNotRunning(resumable: Boolean, resuming: Boolean, onResume: () -> Unit, modifier: Modifier) {
    Box(modifier.background(PersonaTheme.palette.terminalBackground), contentAlignment = Alignment.Center) {
        // The terminal is dark in either appearance, so what stands in for it is drawn in the dark palette, as on iOS.
        PersonaTheme(darkTheme = true) {
            JEmptyState(stringResource(R.string.terminal_no_terminal_title),
                stringResource(if (resumable) R.string.terminal_note_no_terminal_resume else R.string.terminal_note_no_terminal),
                icon = PhIcons.TerminalWindow,
                action = if (resumable) ({ JButton(stringResource(if (resuming) R.string.terminal_resuming else R.string.terminal_resume), onResume,
                    size = JButtonSize.Small, loading = resuming, enabled = !resuming) }) else null)
        }
    }
}

/** Under the title: the grid the phone drives, taking it over, the pane it mirrors, or the screen read once a second. */
@Composable
private fun terminalSubtitle(state: TerminalUiState): String? = when {
    state.driving && state.cols > 0 -> stringResource(R.string.terminal_subtitle_phone, state.cols, state.rows)
    state.online && state.control == TerminalControl.Starting -> stringResource(R.string.terminal_subtitle_starting)
    !state.loaded || !state.online -> null
    state.streaming && state.cols > 0 -> stringResource(R.string.terminal_subtitle_computer, state.cols, state.rows)
    !state.streaming -> stringResource(R.string.terminal_subtitle_polling)
    else -> null
}

/** "Display": the font size (a step at a time, the sheet stays open) and folding what is only watched. */
@Composable
private fun TerminalDisplaySheet(
    fontSize: Float, onFont: (Boolean) -> Unit, wrap: Boolean, onWrap: (Boolean) -> Unit, wrapEnabled: Boolean, onDismiss: () -> Unit,
) {
    val palette = PersonaTheme.palette
    JSheet(onDismiss, title = stringResource(R.string.terminal_display)) {
        JRow(stringResource(R.string.terminal_font_size), subtitle = stringResource(R.string.terminal_font_size_note), trailing = {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
                JButton(stringResource(R.string.terminal_font_down), { onFont(false) }, kind = JButtonKind.Secondary, size = JButtonSize.Small, enabled = fontSize > TerminalFontSize.MIN)
                val sizeDescription = stringResource(R.string.terminal_font_size_value, TerminalFontSize.label(fontSize))
                Text(TerminalFontSize.label(fontSize), style = PersonaTheme.type.micro, color = palette.ink, textAlign = TextAlign.Center,
                    maxLines = 1, modifier = Modifier.widthIn(min = Tokens.Space.xxl).semantics { contentDescription = sizeDescription })
                JButton(stringResource(R.string.terminal_font_up), { onFont(true) }, kind = JButtonKind.Secondary, size = JButtonSize.Small, enabled = fontSize < TerminalFontSize.MAX)
            }
        })
        JSwitchRow(stringResource(R.string.terminal_wrap), wrap, onWrap, enabled = wrapEnabled,
            subtitle = stringResource(if (wrapEnabled) R.string.terminal_wrap_on_note else R.string.terminal_wrap_off_note))
    }
}

@Composable
private fun TerminalKeyBar(
    modifiers: Map<TerminalModifiers.Key, ModifierState>, enabled: Boolean, modifierAction: (TerminalModifiers.Key, String, Boolean) -> Unit,
    press: (String) -> Unit, keyboard: Boolean, toggleKeyboard: () -> Unit, paste: () -> Unit,
) {
    val currentModifierAction by rememberUpdatedState(modifierAction)
    val currentToggle by rememberUpdatedState(toggleKeyboard)
    val currentPress by rememberUpdatedState(press)
    val keyboardDescription = stringResource(if (keyboard) R.string.terminal_keyboard_hide_description else R.string.terminal_keyboard_show_description)
    val modifierStates = ModifierState.entries.associateWith { stringResource(it.description) }
    val keyNames = keyNameSources.mapValues { (_, name) -> if (name is Int) stringResource(name) else name.toString() }
    ComposerFrame(trailing = {
        JChip(stringResource(if (keyboard) R.string.terminal_keyboard_hide else R.string.terminal_keyboard_show), selected = keyboard, icon = PhIcons.Keyboard,
                modifier = Modifier.heightIn(min = 48.dp).semantics {
                    role = Role.Button; contentDescription = keyboardDescription
                    onClick { currentToggle(); true }
                }.pointerInput(Unit) { detectTapGestures(onTap = { currentToggle() }, onDoubleTap = { currentPress("enter") }) })
    }, field = {
        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(Tokens.Space.xs), verticalAlignment = Alignment.CenterVertically) {
            TerminalModifiers.Key.entries.forEach { key ->
                val value = modifiers[key] ?: ModifierState.OFF
                val label = when (key) { TerminalModifiers.Key.CTRL -> "Ctrl"; TerminalModifiers.Key.ALT -> "Alt"; TerminalModifiers.Key.SHIFT -> "⇧" }
                val name = when (key) { TerminalModifiers.Key.CTRL -> "Ctrl"; TerminalModifiers.Key.ALT -> "Alt"; TerminalModifiers.Key.SHIFT -> "Shift" }
                JKeyCap(label, state = value.keyCap, enabled = enabled, modifier = Modifier
                    .semantics {
                        role = Role.Button; contentDescription = name; selected = value != ModifierState.OFF
                        stateDescription = modifierStates.getValue(value)
                        onClick { currentModifierAction(key, "tap", false); true }
                    }.pointerInput(key) {
                        var lastTap = Long.MIN_VALUE / 2
                        awaitEachGesture {
                            val down = awaitFirstDown(requireUnconsumed = false); down.consume()
                            var canceled = false
                            val up = withTimeoutOrNull(viewConfiguration.longPressTimeoutMillis) {
                                waitForUpOrCancellation().also { canceled = it == null }
                            }
                            if (canceled) return@awaitEachGesture
                            if (up != null) {
                                currentModifierAction(key, "tap", up.uptimeMillis - lastTap <= viewConfiguration.doubleTapTimeoutMillis)
                                lastTap = up.uptimeMillis; up.consume()
                            } else {
                                currentModifierAction(key, "hold", false)
                                try { waitForUpOrCancellation()?.consume() } finally { currentModifierAction(key, "release", false) }
                                lastTap = Long.MIN_VALUE / 2
                            }
                        }
                    })
            }
            // ⇧Tab cycles Claude Code's modes (one key, not ⇧ then Tab).
            listOf("Esc" to "esc", "Tab" to "tab", "⇧Tab" to "shift+tab", "⌃C" to "ctrl+c", "←" to "left", "↓" to "down", "↑" to "up",
                "→" to "right", "↵" to "enter", "⌫" to "backspace").forEach { (label, key) ->
                JKeyCap(label, onClick = { press(key) }, enabled = enabled, contentDescription = keyNames[key])
            }
            JKeyCap(stringResource(R.string.terminal_paste), onClick = paste, enabled = enabled)
        }
    })
}

/** What a screen reader says a modifier is doing. */
private val ModifierState.description: Int get() = when (this) {
    ModifierState.OFF -> R.string.terminal_modifier_off
    ModifierState.ONCE -> R.string.terminal_modifier_once
    ModifierState.HELD -> R.string.terminal_modifier_held
    ModifierState.LOCKED -> R.string.terminal_modifier_locked
}

private val ModifierState.keyCap: KeyCapState get() = when (this) {
    ModifierState.OFF -> KeyCapState.Off
    ModifierState.ONCE -> KeyCapState.Once
    ModifierState.HELD -> KeyCapState.Held
    ModifierState.LOCKED -> KeyCapState.Locked
}

/** What a screen reader says for the keys whose labels are symbols: key names, or resources for the words. */
private val keyNameSources: Map<String, Any> = mapOf("shift+tab" to "Shift Tab", "ctrl+c" to "Control C",
    "left" to R.string.terminal_key_left, "down" to R.string.terminal_key_down, "up" to R.string.terminal_key_up,
    "right" to R.string.terminal_key_right, "enter" to R.string.terminal_key_enter, "backspace" to R.string.terminal_key_backspace)
