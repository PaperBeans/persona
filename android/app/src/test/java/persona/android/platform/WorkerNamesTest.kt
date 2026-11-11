package persona.android.platform

import java.io.File
import javax.xml.parsers.DocumentBuilderFactory
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element

class WorkerNamesTest {
    @Test fun manifestsDeclareEachCanonicalComponentOnceAndLaunchTheRealActivity() {
        val root = generateSequence(File(System.getProperty("user.dir")).absoluteFile) { it.parentFile }
            .first { File(it, "android/app/src/main/AndroidManifest.xml").isFile }
        val namespace = "http://schemas.android.com/apk/res/android"
        fun manifest(relative: String) = DocumentBuilderFactory.newInstance().apply { isNamespaceAware = true }
            .newDocumentBuilder().parse(File(root, "android/$relative/src/main/AndroidManifest.xml"))
        for (module in listOf("app", "platform/notify")) {
            val document = manifest(module)
            for (tag in listOf("activity", "activity-alias", "receiver", "service")) {
                val nodes = document.getElementsByTagName(tag)
                val names = (0 until nodes.length).map { (nodes.item(it) as Element).getAttributeNS(namespace, "name") }
                assertEquals("$module $tag", names.size, names.distinct().size)
            }
        }
        val app = manifest("app")
        assertEquals(0, app.getElementsByTagName("activity-alias").length)
        val launchers = app.getElementsByTagName("category").let { nodes ->
            (0 until nodes.length).map { nodes.item(it) as Element }
                .filter { it.getAttributeNS(namespace, "name") == "android.intent.category.LAUNCHER" }
        }
        assertEquals(1, launchers.size)
        val activity = launchers.single().parentNode.parentNode as Element
        assertEquals("activity", activity.tagName)
        assertEquals(".MainActivity", activity.getAttributeNS(namespace, "name"))
        val strings = DocumentBuilderFactory.newInstance().newDocumentBuilder()
            .parse(File(root, "android/app/src/main/res/values/compatibility.xml")).getElementsByTagName("string")
        val resources = (0 until strings.length).associate { index ->
            val element = strings.item(index) as Element
            element.getAttribute("name") to element.textContent
        }
        val routes = app.getElementsByTagName("data").let { nodes ->
            (0 until nodes.length).map { nodes.item(it) as Element }
                .map {
                    val scheme = it.getAttributeNS(namespace, "scheme")
                    (if (scheme.startsWith("@string/")) resources.getValue(scheme.removePrefix("@string/")) else scheme) to
                        it.getAttributeNS(namespace, "host")
                }
                .filter { it.first.isNotEmpty() }
        }
        assertEquals(persona.core.contracts.ProtocolCompatibility.appSchemes.flatMap { scheme ->
            listOf(scheme to "pair") +
                (if (scheme == "persona") listOf(scheme to "password-login") else emptyList()) +
                listOf(scheme to "open")
        }, routes)
        assertTrue(activity.getAttributeNS(namespace, "exported").toBoolean())
    }

    @Test fun queuedWorkersUseCanonicalNames() {
        assertEquals("persona.android.platform.ShareRetryWorker", ShareRetryWorker::class.java.name)
        assertEquals(ShareRetryWorker::class.java, Class.forName("persona.android.platform.ShareRetryWorker"))
        assertEquals("persona.platform.phonetools.PhoneNotificationWorker", persona.platform.phonetools.PhoneNotificationWorker::class.java.name)
        assertEquals(persona.platform.phonetools.PhoneNotificationWorker::class.java,
            Class.forName("persona.platform.phonetools.PhoneNotificationWorker"))
    }
}
