package persona.core.designsystem

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.LookCurrent
import persona.core.contracts.api.LookListing
import persona.core.contracts.api.LookUploaded
import persona.core.contracts.api.LookView
import persona.core.contracts.api.PersonaInfo
import persona.core.contracts.api.PersonaManifest
import persona.core.contracts.api.PersonaPacks
import persona.core.hub.HubError
import persona.core.hub.HubFailure
import java.io.File
import java.io.IOException

/**
 * New from Photo and deleting an avatar made from an image (#2407), as on iOS (#2405): the upload chooses it for every
 * device, so this phone takes it on with no PUT of its own; a package that didn't come is fetched again without sending
 * the image twice; deleting the chosen one brings the default back here.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class PersonaStoreUploadTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    private class UploadHub : PersonaPacks {
        val looks = mutableListOf<LookView>()
        var current = LookCurrent("persona", "persona")
        var files = true
        var refuse: Exception? = null
        /** Another device's choice, heard between the upload's answer and the list read after it. */
        var chosenElsewhere: String? = null
        val uploadedImages = mutableListOf<Triple<String, String?, Int>>()
        val puts = mutableListOf<String>()
        val deletes = mutableListOf<String>()
        override val uploads = true
        override suspend fun list(): List<PersonaInfo> = looks.mapNotNull { it.pack }
        override suspend fun file(id: String, file: String): ByteArray = if (files) byteArrayOf(1, 2, 3) else throw IOException("offline")
        override suspend fun looks(): LookListing = LookListing(looks.toList(), current)
        override suspend fun choose(id: String, ifUnset: Boolean): LookCurrent {
            puts += id
            if (!(ifUnset && current.id != null)) current = LookCurrent(id, id)
            return current
        }
        override suspend fun upload(name: String, subtitle: String?, image: ByteArray, choose: Boolean): LookUploaded {
            refuse?.let { throw it }
            uploadedImages += Triple(name, subtitle, image.size)
            val id = "u-0000000${looks.size}"
            val look = LookView(id, "hub", name, rev = "0123456789abcdef", uploaded = true, manifest = PersonaManifest(
                format = 1, id = id, name = name, subtitle = subtitle,
                avatar = PersonaManifest.Avatar(mapOf("128" to "avatar-128.png")),
                dot = PersonaManifest.Dot(144, mapOf("idle" to PersonaManifest.DotState("dot/idle.png"))),
                source = PersonaManifest.Source("upload"),
            ))
            looks += look
            if (choose) current = LookCurrent(id, id)
            chosenElsewhere?.let { current = LookCurrent(it, it) }
            return LookUploaded(look, current)
        }
        override suspend fun deleteUpload(id: String): LookCurrent {
            refuse?.let { throw it }
            deletes += id
            if (looks.removeAll { it.id == id }.not()) throw HubFailure(HubError.Server, "not found", status = 404, code = "not_found")
            if (current.id == id) current = LookCurrent("persona", "persona")
            return current
        }
    }

    private fun store(hub: UploadHub): Pair<AppStyleStore, PersonaStore> {
        context.getSharedPreferences("persona-appearance", 0).edit().clear().commit()
        File(context.filesDir, "personas").deleteRecursively()
        val style = AppStyleStore(context)
        val store = PersonaStore(context, style) { hub }
        runBlocking { store.refresh() }
        return style to store
    }

    @Test fun anUploadIsChosenHereWithoutAPutOfItsOwn() = runBlocking {
        val hub = UploadHub()
        val (style, store) = store(hub)
        assertTrue(store.canUpload.value)
        assertEquals(LookUploadOutcome.Done, store.upload("我的猫", "On the sofa", ByteArray(10)))
        assertEquals(listOf(Triple("我的猫", "On the sofa", 10)), hub.uploadedImages)
        assertEquals("u-00000000", style.personaId.value)
        assertEquals("u-00000000", store.current.value?.id)
        assertEquals(setOf("u-00000000"), store.uploaded.value)
        assertEquals(emptyList<String>(), hub.puts)
    }

    @Test fun madeButNotDownloadedIsFetchedAgainWithoutSendingTheImage() = runBlocking {
        val hub = UploadHub().apply { files = false }
        val (style, store) = store(hub)
        val outcome = store.upload("Cat", null, ByteArray(10))
        assertEquals(LookUploadOutcome.NotDownloaded("u-00000000"), outcome)
        assertNull(store.current.value)
        hub.files = true
        assertEquals(LookUploadOutcome.Done, store.fetch("u-00000000"))
        assertEquals(1, hub.uploadedImages.size)
        assertEquals("u-00000000", store.current.value?.id)
        assertEquals("u-00000000", style.personaId.value)
        assertEquals(emptyList<String>(), hub.puts)
    }

    @Test fun aNewerChoiceFromElsewhereStaysOnScreen() = runBlocking {
        val hub = UploadHub().apply { chosenElsewhere = "persona" }
        val (style, store) = store(hub)
        assertEquals(LookUploadOutcome.Done, store.upload("Cat", null, ByteArray(10)))
        assertEquals("persona", style.personaId.value)
        assertEquals(listOf("u-00000000"), store.offered.value.map { it.id })
    }

    @Test fun refusalsKeepTheHubsWordsAndNoAnswerIsUnreachable() = runBlocking {
        val hub = UploadHub()
        val (_, store) = store(hub)
        hub.refuse = HubFailure(HubError.Server, "已经有 50 个了", status = 409, code = "too_many")
        assertEquals(LookUploadOutcome.Refused("已经有 50 个了"), store.upload("Cat", null, ByteArray(10)))
        hub.refuse = IOException("connection reset")
        assertEquals(LookUploadOutcome.Unreachable, store.upload("Cat", null, ByteArray(10)))
        assertEquals(LookUploadOutcome.Unreachable, store.delete("u-00000000"))
        assertTrue(hub.uploadedImages.isEmpty())
    }

    @Test fun deletingTheChosenOneBringsTheDefaultBackAndAGoneOneCountsAsDeleted() = runBlocking {
        val hub = UploadHub()
        val (style, store) = store(hub)
        store.upload("Cat", null, ByteArray(10))
        assertEquals("u-00000000", style.personaId.value)
        assertEquals(LookUploadOutcome.Done, store.delete("u-00000000"))
        assertEquals(PersonaDesign.Default.id, style.personaId.value)
        assertNull(store.current.value)
        assertFalse("u-00000000" in store.uploaded.value)
        assertTrue(store.offered.value.isEmpty())
        // Already gone from the hub (another device deleted it): deleted here too.
        assertEquals(LookUploadOutcome.Done, store.delete("u-00000000"))
        assertEquals(listOf("u-00000000", "u-00000000"), hub.deletes)
        hub.refuse = HubFailure(HubError.Server, "只能删除用图片做的形象", status = 409, code = "not_uploaded")
        assertEquals(LookUploadOutcome.Refused("只能删除用图片做的形象"), store.delete("aurora"))
        assertEquals(emptyList<String>(), hub.puts)
    }
}
