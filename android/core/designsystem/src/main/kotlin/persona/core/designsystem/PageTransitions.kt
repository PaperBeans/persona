package persona.core.designsystem

import androidx.compose.animation.ContentTransform
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.tween
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.unit.LayoutDirection

/** Existing Android page timings and geometry, retained until UIKit's navigation transition is measured. */
internal object PageTransitionTokens {
    const val pushDuration = Tokens.Motion.Layout.duration
    const val popDuration = 260
    const val behindDivisor = 4
}

/** Opaque pages: push from the trailing edge; pop the top page back over the page underneath. */
class PersonaPageTransitions internal constructor(reduced: Boolean, direction: LayoutDirection) {
    private val trailing = if (direction == LayoutDirection.Ltr) 1 else -1
    val push = transform(reduced, pop = false)
    val pop = transform(reduced, pop = true)
    // Navigation 3 seeks this same eased transition with the back gesture's progress, including cancellation.
    val predictivePop get() = pop

    private fun transform(reduced: Boolean, pop: Boolean): ContentTransform {
        if (reduced) return ContentTransform(EnterTransition.None, ExitTransition.None, sizeTransform = null)
        val spec = tween<androidx.compose.ui.unit.IntOffset>(
            if (pop) PageTransitionTokens.popDuration else PageTransitionTokens.pushDuration,
            easing = FastOutSlowInEasing,
        )
        return ContentTransform(
            slideInHorizontally(spec) { width -> if (pop) -trailing * width / PageTransitionTokens.behindDivisor else trailing * width },
            slideOutHorizontally(spec) { width -> if (pop) trailing * width else -trailing * width / PageTransitionTokens.behindDivisor },
            targetContentZIndex = if (pop) -1f else 0f,
            sizeTransform = null,
        )
    }
}

/** Shared by every page stack, including stacks implemented with AnimatedContent. */
@Composable
fun rememberPageTransitions(): PersonaPageTransitions {
    val reduced = PersonaTheme.reduceMotion
    val direction = LocalLayoutDirection.current
    return remember(reduced, direction) { PersonaPageTransitions(reduced, direction) }
}

/**
 * iOS SplitNavigation.swift:171-208: selecting a value replaces the detail immediately; only hideOverlay() moves the
 * list, with Theme.Motion.layout (Theme.swift:724). The phone's drawer has its separate drawer spring.
 */
internal fun <T> sidebarColumnSpec(style: AppStyle, reduced: Boolean): FiniteAnimationSpec<T> = when {
    reduced -> tween(Tokens.Motion.reduced)
    else -> Motions.of(style).let { it.layout?.spec() ?: tween(it.standard) }
}
