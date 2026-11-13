package persona.core.designsystem

import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.indication
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.material3.ripple
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import kotlin.math.PI
import kotlin.math.abs
import kotlin.math.sin

// ------------------------------------------------------------------ voice conversation (voice chat)

/** What the orb of a voice conversation shows. */
enum class VoiceOrbMode { Connecting, Listening, Speaking, Muted, Ended }

private const val SpeakingBeat = 420
private const val ListeningBreath = 1600

/**
 * The orb of a voice conversation: a quick beat while Persona speaks, a slow breath while it listens, growing with the
 * microphone's [level] (0…1; read while drawing, so a new level redraws without recomposing), a spinner while
 * connecting. Glass is a lit sphere in the status light; brief an ink disc that the highlighter circles while Persona
 * speaks; the board a status tile with a level meter. It takes the room it is given (a square in the middle) and is
 * decorative: say the state in text beside it ([VoiceStatus]). Still when motion is reduced.
 */
@Composable
fun VoiceOrb(mode: VoiceOrbMode, modifier: Modifier = Modifier, level: () -> Float = { 0f }) {
    val moving = !PersonaTheme.reduceMotion
    val live = mode == VoiceOrbMode.Speaking || mode == VoiceOrbMode.Listening
    // Only what moves runs: a still orb (muted, ended, reduced motion) asks for no frames at all.
    val transition = rememberInfiniteTransition(label = "voice-orb")
    val pulse = if (moving && live) transition.animateFloat(
        -1f, 1f,
        infiniteRepeatable(tween(if (mode == VoiceOrbMode.Speaking) SpeakingBeat else ListeningBreath, easing = FastOutSlowInEasing), RepeatMode.Reverse),
        label = "pulse",
    ) else null
    val turn = if (moving && mode == VoiceOrbMode.Speaking && PersonaTheme.style == AppStyle.Board) {
        transition.animateFloat(0f, (2 * PI).toFloat(), infiniteRepeatable(tween(SpeakingBeat * 2, easing = LinearEasing)), label = "turn")
    } else null
    val scale: () -> Float = {
        val beat = pulse?.value ?: 0f
        val heard = if (moving && mode == VoiceOrbMode.Listening) level().coerceIn(0f, 1f) else 0f
        when (mode) {
            VoiceOrbMode.Speaking -> 1.04f + beat * 0.05f
            VoiceOrbMode.Listening -> 1f + beat * 0.02f + heard * 0.3f
            VoiceOrbMode.Muted -> 0.92f
            VoiceOrbMode.Connecting -> 0.88f
            VoiceOrbMode.Ended -> 0.84f
        }
    }
    BoxWithConstraints(modifier.clearAndSetSemantics {}, contentAlignment = Alignment.Center) {
        val core = (minOf(maxWidth, maxHeight) * 0.6f).coerceAtMost(176.dp)
        when (PersonaTheme.style) {
            AppStyle.Glass -> GlassOrb(mode, core, scale)
            AppStyle.Brief -> BriefOrb(mode, core, scale)
            AppStyle.Board -> BoardOrb(mode, core, if (moving) level else ({ 0.35f }), { turn?.value ?: 0f }, moving)
        }
    }
}

/** A lit sphere: blue while it listens, mint coming in while it speaks, a halo that breathes with it. */
@Composable
private fun GlassOrb(mode: VoiceOrbMode, core: Dp, scale: () -> Float) {
    val palette = PersonaTheme.palette
    val spec = tween<Color>(PersonaTheme.motion.standard)
    val (from, to) = when (mode) {
        VoiceOrbMode.Speaking -> palette.done.dot to palette.working.dot
        VoiceOrbMode.Listening -> palette.accent to palette.working.dot
        VoiceOrbMode.Muted -> palette.idle.dot to palette.offline.dot
        VoiceOrbMode.Connecting -> palette.working.dot.copy(alpha = 0.75f) to palette.idle.dot
        VoiceOrbMode.Ended -> palette.offline.dot to palette.offline.dot.copy(alpha = 0.6f)
    }
    val top by animateColorAsState(from, spec, label = "top")
    val bottom by animateColorAsState(to, spec, label = "bottom")
    val glow by animateColorAsState(if (mode == VoiceOrbMode.Speaking) palette.done.dot else palette.working.dot, spec, label = "glow")
    val strength by animateFloatAsState(
        when (mode) { VoiceOrbMode.Speaking -> 0.6f; VoiceOrbMode.Listening -> 0.45f; VoiceOrbMode.Connecting -> 0.22f; VoiceOrbMode.Muted -> 0.12f; VoiceOrbMode.Ended -> 0f },
        tween(PersonaTheme.motion.standard), label = "strength",
    )
    val edge = Brush.verticalGradient(listOf(palette.strokeTop, palette.strokeBottom))
    Box(contentAlignment = Alignment.Center) {
        Box(Modifier.size(core * 1.6f).graphicsLayer { val s = scale(); scaleX = s; scaleY = s }.drawBehind {
            drawCircle(Brush.radialGradient(listOf(glow.copy(alpha = strength), glow.copy(alpha = strength * 0.28f), Color.Transparent)))
        })
        Box(
            Modifier.size(core).graphicsLayer { val s = scale(); scaleX = s; scaleY = s }.clip(CircleShape).drawBehind {
                drawCircle(Brush.linearGradient(listOf(top, bottom), start = Offset.Zero, end = Offset(size.width, size.height)))
                // The sphere's sheen, up and to the left.
                val shine = Offset(size.width * 0.34f, size.height * 0.28f)
                drawCircle(Brush.radialGradient(listOf(Color.White.copy(alpha = 0.42f), Color.Transparent), center = shine, radius = size.width * 0.5f), size.width * 0.5f, shine)
            }.border(1.dp, edge, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            if (mode == VoiceOrbMode.Connecting) JSpinner(Modifier.size(core * 0.22f), color = Color.White, stroke = 2.5.dp)
        }
    }
}

/** An ink disc on paper; the highlighter circles it while Persona speaks, a fine ring follows the voice it hears. */
@Composable
private fun BriefOrb(mode: VoiceOrbMode, core: Dp, scale: () -> Float) {
    val palette = PersonaTheme.palette
    val disc by animateColorAsState(
        when (mode) { VoiceOrbMode.Muted -> palette.ink3; VoiceOrbMode.Ended -> palette.surfaceStrong; else -> palette.action },
        tween(PersonaTheme.motion.standard), label = "disc",
    )
    val marked by animateFloatAsState(if (mode == VoiceOrbMode.Speaking) 1f else 0f, tween(PersonaTheme.motion.standard), label = "marker")
    val ink = if (mode == VoiceOrbMode.Ended) palette.ink3 else palette.onAction
    Box(contentAlignment = Alignment.Center) {
        Box(Modifier.size(core * 1.36f).graphicsLayer { val s = scale(); scaleX = s; scaleY = s; alpha = marked }.background(palette.marker, CircleShape))
        if (mode == VoiceOrbMode.Listening) Box(
            Modifier.size(core * 1.2f).graphicsLayer { val s = scale(); scaleX = s; scaleY = s }.border(2.dp, palette.ink3.copy(alpha = 0.55f), CircleShape),
        )
        Box(Modifier.size(core).clip(CircleShape).background(disc), contentAlignment = Alignment.Center) {
            when (mode) {
                VoiceOrbMode.Connecting -> JSpinner(Modifier.size(core * 0.22f), color = palette.onAction, stroke = 2.5.dp)
                VoiceOrbMode.Muted -> JIcon(PhIcons.MicrophoneSlash, null, size = core * 0.3f, tint = ink)
                else -> JIcon(PhIcons.Waveform, null, size = core * 0.34f, tint = ink)
            }
        }
    }
}

/** The board's status tile: the colour is the state, five bars are the level meter. */
@Composable
private fun BoardOrb(mode: VoiceOrbMode, core: Dp, level: () -> Float, turn: () -> Float, moving: Boolean) {
    val palette = PersonaTheme.palette
    val tone = when (mode) {
        VoiceOrbMode.Speaking -> palette.done
        VoiceOrbMode.Listening, VoiceOrbMode.Connecting -> palette.working
        VoiceOrbMode.Muted -> palette.idle
        VoiceOrbMode.Ended -> palette.offline
    }
    val spec = tween<Color>(PersonaTheme.motion.standard)
    val fill by animateColorAsState(if (mode == VoiceOrbMode.Connecting) palette.surface else tone.dot, spec, label = "tile")
    val bars by animateColorAsState(tone.onDot, spec, label = "bars")
    val side = core * 0.9f
    val shape = PersonaTheme.shapes.cardShape
    Box(Modifier.size(side).clip(shape).background(fill).border(1.dp, palette.line, shape), contentAlignment = Alignment.Center) {
        if (mode == VoiceOrbMode.Connecting) JSpinner(Modifier.size(side * 0.24f), color = palette.ink, stroke = 2.5.dp)
        else Canvas(Modifier.fillMaxSize()) {
            val width = size.width * 0.085f
            val gap = size.width * 0.06f
            val left = (size.width - (MeterProfile.size * width + (MeterProfile.size - 1) * gap)) / 2
            val tallest = size.height * 0.56f
            // Read only while listening: speaking moves by itself, and the level would redraw it 50 times a second.
            val heard = if (mode == VoiceOrbMode.Listening) level().coerceIn(0f, 1f) else 0f
            MeterProfile.forEachIndexed { i, profile ->
                val fraction = when (mode) {
                    VoiceOrbMode.Speaking -> if (moving) (0.3f + 0.7f * abs(sin(turn() + i * 1.1f))) * profile else 0.7f * profile
                    VoiceOrbMode.Listening -> (0.16f + heard * 0.84f) * profile
                    else -> 0.1f
                }
                val height = (tallest * fraction).coerceAtLeast(width)
                drawRoundRect(
                    bars, topLeft = Offset(left + i * (width + gap), (size.height - height) / 2), size = Size(width, height),
                    cornerRadius = CornerRadius(width / 2),
                )
            }
        }
    }
}

private val MeterProfile = listOf(0.55f, 0.8f, 1f, 0.8f, 0.55f)

/** How a [VoiceControl] stands out: plain, filled with the action colour (on, or the way on), or destructive ("Hang up"). */
enum class VoiceControlKind { Plain, Filled, Danger }

/**
 * A round control of the voice page with its label underneath ("Mute", "Interrupt", "Hang up"): a glass circle, a wash disc, or a
 * bordered square; [VoiceControlKind.Filled] uses the action color, [VoiceControlKind.Danger] red words/icon on a neutral disc.
 * The label is one line; the whole thing is one button for touch and TalkBack.
 */
@Composable
fun VoiceControl(
    icon: PhIcon,
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    kind: VoiceControlKind = VoiceControlKind.Plain,
    enabled: Boolean = true,
) {
    val palette = PersonaTheme.palette
    val style = PersonaTheme.style
    val shape = if (style == AppStyle.Board) RoundedCornerShape(PersonaTheme.shapes.card) else CircleShape
    val (fill, ink) = when (kind) {
        VoiceControlKind.Plain -> (if (style == AppStyle.Board) palette.surface else palette.surfaceStrong) to palette.ink
        VoiceControlKind.Filled -> palette.action to palette.onAction
        VoiceControlKind.Danger -> palette.statusSurface to palette.danger.text
    }
    val surface = when {
        kind == VoiceControlKind.Plain && style == AppStyle.Glass -> Modifier.floatingSurface(CircleShape)
        kind == VoiceControlKind.Plain && style == AppStyle.Board -> Modifier.clip(shape).background(fill).border(1.dp, palette.line, shape)
        else -> Modifier.clip(shape).background(fill)
    }
    val interaction = remember { MutableInteractionSource() }
    Column(
        modifier.widthIn(min = 72.dp)
            .clickable(interaction, indication = null, enabled = enabled, role = Role.Button, onClick = onClick)
            .semantics(mergeDescendants = true) {}
            .stableAlpha(if (enabled) 1f else 0.4f),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(
            Modifier.size(if (style == AppStyle.Board) 60.dp else 64.dp).then(surface).clip(shape).indication(interaction, ripple()),
            contentAlignment = Alignment.Center,
        ) { JIcon(icon, null, size = 26.dp, tint = ink, selected = kind != VoiceControlKind.Plain) }
        Spacer(Modifier.height(8.dp.dense))
        Text(
            label, style = PersonaTheme.type.caption, color = when (kind) {
                VoiceControlKind.Plain -> palette.ink2
                VoiceControlKind.Danger -> palette.danger.text
                VoiceControlKind.Filled -> palette.ink
            },
            maxLines = 1, softWrap = false, overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * The row of [VoiceControl]s along the bottom of the voice page, evenly spread: floating over the captions in glass,
 * on the page's paper in brief, on the board's grey under a rule.
 */
@Composable
fun VoiceControlBar(modifier: Modifier = Modifier, content: @Composable RowScope.() -> Unit) {
    val palette = PersonaTheme.palette
    val frame = when (PersonaTheme.style) {
        AppStyle.Glass -> Modifier
        AppStyle.Brief -> Modifier.background(palette.background)
        AppStyle.Board -> Modifier.background(palette.background).drawBehind { drawLine(palette.line, Offset(0f, 0f), Offset(size.width, 0f), 1.dp.toPx()) }
    }
    Row(
        modifier.fillMaxWidth().then(frame).padding(horizontal = 24.dp.dense).padding(top = 14.dp.dense, bottom = 18.dp.dense),
        horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.Top, content = content,
    )
}

/**
 * One line of the conversation's captions: what the user said in their bubble on the right, what Persona said as words
 * on the page. A line still being heard or spoken ([final] false) is faint.
 */
@Composable
fun VoiceCaption(text: String, fromUser: Boolean, final: Boolean, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val ink = palette.ink.copy(alpha = if (final) 1f else 0.55f)
    if (fromUser) {
        UserBubbleFrame(modifier) { Text(text, style = PersonaTheme.type.body, color = ink) }
    } else {
        val words = when (PersonaTheme.style) {
            AppStyle.Glass -> PersonaTheme.type.voice
            AppStyle.Brief -> PersonaTheme.type.voice.copy(fontSize = 19.sp, lineHeight = 29.sp, fontWeight = FontWeight.Medium)
            AppStyle.Board -> PersonaTheme.type.body.copy(fontWeight = FontWeight.Medium)
        }
        Text(text, modifier.fillMaxWidth().padding(end = 32.dp.dense, top = 6.dp.dense, bottom = 6.dp.dense), style = words, color = ink)
    }
}

/** What the voice conversation is doing ("Listening", "Speaking…"), centred under the orb, and what went wrong under it. */
@Composable
fun VoiceStatus(text: String, modifier: Modifier = Modifier, error: String? = null) {
    val palette = PersonaTheme.palette
    Column(
        modifier.fillMaxWidth().padding(horizontal = 24.dp.dense).semantics(mergeDescendants = true) { liveRegion = LiveRegionMode.Polite },
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text, style = PersonaTheme.type.callout.copy(fontWeight = FontWeight.Medium), textAlign = TextAlign.Center, maxLines = 2,
            color = if (PersonaTheme.style == AppStyle.Brief) palette.ink else palette.ink2, overflow = TextOverflow.Ellipsis,
        )
        error?.let {
            Spacer(Modifier.height(6.dp.dense))
            Text(it, style = PersonaTheme.type.caption, color = palette.danger.text, textAlign = TextAlign.Center)
        }
    }
}
