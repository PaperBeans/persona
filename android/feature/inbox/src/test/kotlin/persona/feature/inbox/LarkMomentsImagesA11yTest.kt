package persona.feature.inbox

import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.LarkMomentsImage
import persona.core.designsystem.PersonaTheme

/** TalkBack names each picture of a Lark Moments post and says a tap opens it full screen, like a chat picture (#4172). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class LarkMomentsImagesA11yTest {
    @get:Rule val rule = createComposeRule()

    private val opensFullScreen = SemanticsMatcher("an image that says a tap views it full screen") {
        it.config.getOrNull(SemanticsActions.OnClick)?.label == "View full screen" &&
            it.config.getOrNull(SemanticsProperties.Role) == Role.Image
    }

    @Test fun aSinglePictureSaysATapOpensIt() {
        rule.setContent { PersonaTheme(reduceMotion = true) { MomentsImagesLayout(listOf(LarkMomentsImage("a")), onOpenImage = { _, _ -> }) } }
        rule.onNodeWithContentDescription("Image").assert(opensFullScreen)
    }

    @Test fun everyPictureOfAGridSaysATapOpensIt() {
        val images = List(4) { LarkMomentsImage("p$it") }
        rule.setContent { PersonaTheme(reduceMotion = true) { MomentsImagesLayout(images, onOpenImage = { _, _ -> }) } }
        val nodes = rule.onAllNodesWithContentDescription("Image")
        nodes.assertCountEquals(4)
        repeat(4) { nodes[it].assert(opensFullScreen) }
    }
}
