package persona.feature.chat

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import androidx.core.content.FileProvider
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.encodeToString
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.HubScope
import persona.core.contracts.ImageLimits
import persona.core.contracts.ImagePayload
import persona.core.contracts.JsonCodec
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantSnapshot
import persona.core.contracts.api.DraftKey
import persona.core.contracts.api.DraftSnapshot
import persona.core.designsystem.jpegBytes
import persona.feature.chat.media.ChatMedia
import persona.feature.chat.media.MediaShare
import persona.feature.chat.media.chatViewerActions
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.Base64
import java.util.Random

/**
 * Verifies #13043 across `:feature:chat`: hub `image_limits` for count & base64 body budget, overflow to file
 * attachments with localized notices, independent video limit, JPEG scaling to `max_side`, drop import, forwarding,
 * viewer limit, and en/zh/ja strings.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35])
class ComposerImageLimitsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val created = mutableListOf<File>()

    @Before fun setUpProvider() {
        useChatFileProvider(context)
    }

    @After fun cleanUp() {
        created.forEach(File::delete)
        File(context.cacheDir, "chat-drops").deleteRecursively()
        File(context.cacheDir, "chat-captures").deleteRecursively()
    }

    private fun photo(id: String, size: Long = 1024L) =
        ComposerAttachment(id, "$id.jpg", "content://example/$id", image = true, size = size)

    private fun file(id: String) =
        ComposerAttachment(id, "$id.txt", "content://example/$id", image = false, size = 128L)

    private fun video(id: String) =
        ComposerAttachment(id, "$id.mp4", "content://example/$id", image = false, video = true, size = 4096L)

    @Test fun forwardAndAttachAllowMoreThanFourPhotosUnderDefaultHubLimits() = runTest {
        val paths = List(6) { "/tmp/photo-$it.jpg" }
        val raw = "Look\n\n<attached_images>\n${paths.joinToString("\n")}\n</attached_images>"
        val session = Session("box", "s1", title = "Work", cwd = "/work", agent = "claude")
        val machine = Machine("box", "Box", online = true)
        val forwarding = ChatForwarding(
            agents = MutableStateFlow(listOf(session)),
            machines = MutableStateFlow(listOf(machine)),
            assistant = MutableStateFlow(AssistantSnapshot(null, emptyList())),
            personaGroupOf = { true },
            conversationTitle = { it.title },
            memory = { null },
            openChat = { _, _ -> },
            openPersonaHome = {},
            openNewAgent = {},
        )
        val draft = forwardDraftOf(MessageTarget("m1", "Look", user = true, raw = raw), session.key, session, machine, ChatRenderStyle.Agent, forwarding)
        assertEquals(6, draft.photos.size)
        val capped = forwardDraftOf(MessageTarget("m1", "Look", user = true, raw = raw), session.key, session, machine, ChatRenderStyle.Agent, forwarding, maxPhotos = 3)
        assertEquals(3, capped.photos.size)

        val composer = ChatComposerState()
        val handoff = RestoredPhotos(List(6) { index ->
            ComposerAttachment("p$index", "photo-$index.jpg", "", image = true, size = 1024)
        })
        val rejected = attachForwardedPhotos(context, composer, listOf(handoff))
        assertTrue(rejected.isEmpty())
        assertEquals(6, composer.attachments.count { it.image })
    }

    @Test fun dropImportAcceptsFifthPhotoAndSpillsPastLimitToFile() {
        val imageFile = File(File(context.cacheDir, "chat-captures").apply { mkdirs() }, "fifth.png")
        val bitmap = Bitmap.createBitmap(4, 4, Bitmap.Config.ARGB_8888)
        imageFile.outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        bitmap.recycle()
        created += imageFile
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.chat.files", imageFile)

        val fourPhotos = List(4) { photo("p$it") }
        val fifth = importDroppedAttachment(context, uri, existing = fourPhotos)
        assertNotNull("5th photo must not be rejected at legacy cap 4", fifth.attachment)
        assertTrue(fifth.attachment!!.image)
        assertNull(fifth.notice)

        val tightLimits = ImageLimits(perMessage = 2)
        val spilled = importDroppedAttachment(context, uri, existing = List(2) { photo("e$it") }, limits = tightLimits, canSpill = true)
        assertNotNull(spilled.attachment)
        assertFalse("overflow picture becomes an ordinary file attachment", spilled.attachment!!.image)
        assertFalse(spilled.attachment!!.video)
        assertNotNull(spilled.notice)

        val refused = importDroppedAttachment(context, uri, existing = List(2) { photo("e$it") }, limits = tightLimits, canSpill = false)
        assertNull(refused.attachment)
        assertNotNull(refused.notice)
    }

    @Config(qualifiers = "en")
    @Test fun planComposerPhotosSpillsCountAndBytesOverflowAndReportsWhichLimitWasHit() {
        val countLimits = ImageLimits(perMessage = 2)
        val countPlan = planComposerPhotos(
            existing = listOf(photo("p0")),
            incoming = List(3) { PhotoCandidate(photo("n$it"), scaledBytes = 1024L) },
            limits = countLimits,
            canSpill = true,
        )
        assertEquals(1, countPlan.acceptedPhotos.size)
        assertEquals(2, countPlan.spilledFiles.size)
        assertTrue(countPlan.spilledFiles.none { it.image || it.video })
        assertTrue(countPlan.refused.isEmpty())
        assertEquals(
            listOf("A message holds up to 2 pictures. The other 2 will go as files."),
            countPlan.notices(context.resources, countLimits),
        )

        // Base64 budget: request_bytes - 1 MiB = 400 bytes -> 150 raw bytes = 200 base64 bytes each -> 2 fit, 3rd spills.
        val bytesLimits = ImageLimits(requestBytes = ImageLimits.BODY_ROOM + 400L, perMessage = 20)
        val bytesPlan = planComposerPhotos(
            existing = listOf(photo("e0", size = 150L)),
            incoming = listOf(
                PhotoCandidate(photo("n0", size = 5000L), scaledBytes = 150L),
                PhotoCandidate(photo("n1", size = 5000L), scaledBytes = 150L),
            ),
            limits = bytesLimits,
            canSpill = true,
        )
        assertEquals(1, bytesPlan.acceptedPhotos.size)
        assertEquals(150L, bytesPlan.acceptedPhotos.single().size)
        assertEquals(1, bytesPlan.spilledFiles.size)
        assertEquals("spilled file retains original size", 5000L, bytesPlan.spilledFiles.single().size)
        assertEquals(
            listOf("These pictures are too large together. One of them will go as a file."),
            bytesPlan.notices(context.resources, bytesLimits),
        )

        // Without a computer (canSpill = false), overflow photos are refused with the exact limit notice.
        val refusedBytes = planComposerPhotos(
            existing = listOf(photo("e0", size = 300L)),
            incoming = listOf(PhotoCandidate(photo("n0", size = 150L), scaledBytes = 150L)),
            limits = bytesLimits,
            canSpill = false,
        )
        assertTrue(refusedBytes.added.isEmpty())
        assertEquals(1, refusedBytes.refused.size)
        assertEquals(
            listOf("These pictures are too large to send together"),
            refusedBytes.notices(context.resources, bytesLimits),
        )
    }

    @Test fun videosKeepTheirOwnCapOfFourAndPickerSizesByRemainingRoom() {
        val limits = ImageLimits(perMessage = 5)
        val existing = List(4) { video("v$it") } + listOf(photo("p0"), photo("p1")) + List(3) { file("f$it") }
        assertEquals(0, composerVideoRoom(existing))
        assertEquals("videos must not eat into photo slots", 3, composerPhotoRoom(existing, limits))
        assertEquals(7, composerFileRoom(existing))
        assertEquals(10, composerMediaPickerRoom(existing, limits, canSpill = true))
        assertEquals(3, composerMediaPickerRoom(existing, limits, canSpill = false))
    }

    @Test fun encodePhotoJpegScalesToHubMaxSideAtQuality75() {
        val big = Bitmap.createBitmap(2000, 1000, Bitmap.Config.ARGB_8888)
        try {
            val encoded = encodePhotoJpeg(big, ImageLimits(maxSide = 800))
            assertTrue(encoded.isNotEmpty())
            val decoded = BitmapFactory.decodeByteArray(encoded, 0, encoded.size)
            assertNotNull(decoded)
            assertEquals(800, decoded.width)
            assertEquals(400, decoded.height)
            decoded.recycle()
        } finally {
            big.recycle()
        }
    }

    @Test fun planComposerPhotosIsOrderIndependentForLargeAndSmallMultiPick() {
        // Budget = 400 base64 bytes: large (300 raw = 400 b64) and small (150 raw = 200 b64) cannot both fit inline.
        val limits = ImageLimits(requestBytes = ImageLimits.BODY_ROOM + 400L, perMessage = 20)
        val large = PhotoCandidate(photo("large", size = 5000L), scaledBytes = 300L)
        val small = PhotoCandidate(photo("small", size = 2000L), scaledBytes = 150L)

        // Spillable: [large, small] and [small, large] both keep small inline and spill large as a file.
        val largeThenSmall = planComposerPhotos(emptyList(), listOf(large, small), limits, canSpill = true)
        val smallThenLarge = planComposerPhotos(emptyList(), listOf(small, large), limits, canSpill = true)
        assertEquals(listOf("small"), largeThenSmall.acceptedPhotos.map { it.id })
        assertEquals(listOf("small"), smallThenLarge.acceptedPhotos.map { it.id })
        assertEquals(150L, largeThenSmall.acceptedPhotos.single().scaledBytes)
        assertEquals(listOf("large"), largeThenSmall.spilledFiles.map { it.id })
        assertEquals(listOf("large"), smallThenLarge.spilledFiles.map { it.id })
        assertEquals(5000L, largeThenSmall.spilledFiles.single().size)
        assertNull(largeThenSmall.spilledFiles.single().scaledBytes)
        assertTrue(largeThenSmall.refused.isEmpty())
        assertTrue(smallThenLarge.refused.isEmpty())

        // Without file spill: [large, small] and [small, large] both keep small inline and refuse large.
        val noSpillLS = planComposerPhotos(emptyList(), listOf(large, small), limits, canSpill = false)
        val noSpillSL = planComposerPhotos(emptyList(), listOf(small, large), limits, canSpill = false)
        assertEquals(listOf("small"), noSpillLS.acceptedPhotos.map { it.id })
        assertEquals(listOf("small"), noSpillSL.acceptedPhotos.map { it.id })
        assertEquals(listOf("large"), noSpillLS.refused.map { it.id })
        assertEquals(listOf("large"), noSpillSL.refused.map { it.id })
        assertEquals(1, noSpillLS.refusedByBytes)
        assertEquals(1, noSpillSL.refusedByBytes)

        // Limited file slots (only 1 slot left): small-first fits both small photos inline so large takes the 1 file
        // slot and nothing is dropped, while restoring each call's original composer order.
        val nineFiles = List(9) { file("f$it") }
        val smallA = PhotoCandidate(photo("smallA", size = 1800L), scaledBytes = 100L) // 136 b64
        val smallB = PhotoCandidate(photo("smallB", size = 2200L), scaledBytes = 150L) // 200 b64
        val constrainedLS = planComposerPhotos(nineFiles, listOf(large, smallA, smallB), limits, canSpill = true)
        val constrainedSL = planComposerPhotos(nineFiles, listOf(smallB, smallA, large), limits, canSpill = true)
        assertEquals(listOf("smallA", "smallB"), constrainedLS.acceptedPhotos.map { it.id })
        assertEquals(listOf("smallB", "smallA"), constrainedSL.acceptedPhotos.map { it.id })
        assertEquals(listOf("large"), constrainedLS.spilledFiles.map { it.id })
        assertEquals(listOf("large"), constrainedSL.spilledFiles.map { it.id })
        assertTrue("neither small photo should be dropped", constrainedLS.refused.isEmpty())
        assertTrue(constrainedSL.refused.isEmpty())

        // Multiple overflow candidates with only 1 file slot: admission and spill sets remain order-independent.
        val huge = PhotoCandidate(photo("huge", size = 9000L), scaledBytes = 500L) // 668 b64
        val overflowA = planComposerPhotos(nineFiles, listOf(huge, large, smallA), limits, canSpill = true)
        val overflowB = planComposerPhotos(nineFiles, listOf(smallA, large, huge), limits, canSpill = true)
        assertEquals(listOf("smallA"), overflowA.acceptedPhotos.map { it.id })
        assertEquals(listOf("smallA"), overflowB.acceptedPhotos.map { it.id })
        assertEquals(listOf("large"), overflowA.spilledFiles.map { it.id })
        assertEquals(listOf("large"), overflowB.spilledFiles.map { it.id })
        assertEquals(listOf("huge"), overflowA.refused.map { it.id })
        assertEquals(listOf("huge"), overflowB.refused.map { it.id })
    }

    @Config(qualifiers = "en")
    @Test fun restoredMessageTagPhotosWithUnknownSizeDoNotContributeZeroAndScaledBytesPersistInDrafts() {
        // Restoring a sent message creates remote-only photo attachments with null size and null scaledBytes.
        val restored = ChatComposerState().apply {
            editMessage("Check this\n\n<attached_images>\n/tmp/remote.jpg\n</attached_images>")
        }
        val held = restored.attachments.single()
        assertTrue(held.image)
        assertNull(held.size)
        assertNull(held.scaledBytes)

        // Budget = 600 b64, per-image cap = 300 raw (400 b64). The unknown held photo conservatively reserves 400 b64
        // (leaving 200 b64), never 0.
        val limits = ImageLimits(requestBytes = ImageLimits.BODY_ROOM + 600L, imageBytes = 300L, perMessage = 10)
        val fits = planComposerPhotos(
            existing = restored.attachments,
            incoming = listOf(PhotoCandidate(photo("fits", size = 500L), scaledBytes = 150L)), // 200 b64
            limits = limits,
            canSpill = false,
        )
        assertEquals(listOf("fits"), fits.acceptedPhotos.map { it.id })
        assertEquals(150L, fits.acceptedPhotos.single().scaledBytes)

        val tooLarge = planComposerPhotos(
            existing = restored.attachments,
            incoming = listOf(PhotoCandidate(photo("too-large", size = 500L), scaledBytes = 200L)), // 268 b64
            limits = limits,
            canSpill = false,
        )
        assertTrue(tooLarge.acceptedPhotos.isEmpty())
        assertEquals(listOf("too-large"), tooLarge.refused.map { it.id })
        assertEquals(listOf("These pictures are too large to send together"), tooLarge.notices(context.resources, limits))

        // Persisting the draft retains scaledBytes across serialization/restoration.
        val draft = ChatComposerDraft("with scaled", fits.acceptedPhotos)
        val encoded = JsonCodec.encodeToString(draft.storedAttachments())
        assertTrue(encoded.contains("\"scaledBytes\":150"))
        val snapshot = DraftSnapshot(
            HubScope("hub", "pairing"),
            DraftKey.Chat(SessionKey("machine", "session")),
            draft.text,
            1,
            JsonCodec.decodeFromString(encoded),
        )
        val reloaded = ChatComposerState(snapshot.composerDraft())
        assertEquals(150L, reloaded.attachments.single().scaledBytes)
    }

    @Test fun queuedPhotosUseActualScaledJpegBytesForSubsequentBodyBudgetAccounting() = runTest {
        // Build a large lossless PNG whose raw decoded byte count is far larger than its scaled JPEG output.
        val bitmap = noisyBitmap(600, 600)
        val pngBytes = ByteArrayOutputStream().use { out ->
            assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, out))
            out.toByteArray()
        }
        val limits = ImageLimits(maxSide = 300, imageBytes = ImageLimits.DEFAULT_IMAGE_BYTES, perMessage = 10)
        val expectedJpegBytes = encodePhotoJpeg(bitmap, limits).size.toLong()
        bitmap.recycle()
        assertTrue(
            "expected PNG (${pngBytes.size}) to exceed scaled JPEG ($expectedJpegBytes)",
            pngBytes.size.toLong() > expectedJpegBytes * 2,
        )

        val payload = ImagePayload("image/png", Base64.getEncoder().encodeToString(pngBytes))
        val restored = restoredPhotos(context, listOf(payload), limits = limits)
        val queuedPhoto = restored.attachments.single()
        assertEquals(pngBytes.size.toLong(), queuedPhoto.size)
        assertEquals(expectedJpegBytes, queuedPhoto.scaledBytes)

        // Set a body budget tight enough to reject raw pngBytes + next photo, but big enough for scaled JPEG + next.
        val nextBytes = 2_000L
        val tightBudget = ImageLimits.base64Bytes(expectedJpegBytes) + ImageLimits.base64Bytes(nextBytes)
        assertTrue(ImageLimits.base64Bytes(pngBytes.size.toLong()) > tightBudget)
        val budgetLimits = limits.copy(requestBytes = ImageLimits.BODY_ROOM + tightBudget)

        val plan = planComposerPhotos(
            existing = listOf(queuedPhoto),
            incoming = listOf(PhotoCandidate(photo("next", size = 50_000L), scaledBytes = nextBytes)),
            limits = budgetLimits,
            canSpill = true,
        )
        assertEquals("queued photo should account for scaled JPEG bytes, not raw PNG bytes", listOf("next"), plan.acceptedPhotos.map { it.id })
        assertTrue(plan.spilledFiles.isEmpty())

        // A legacy draft holding the same local URI without scaledBytes refreshes its scaled JPEG size on demand.
        val legacyHeld = queuedPhoto.copy(scaledBytes = null)
        val refreshed = refreshHeldPhotoSizes(context, listOf(legacyHeld), budgetLimits)
        assertEquals(expectedJpegBytes, refreshed.single().scaledBytes)
    }

    @Config(qualifiers = "en")
    @Test fun writeEditedPhotoAppliesImageBytesStepDownAndViewerAdmissionEnforcesBodyBudget() = runTest {
        val source = noisyBitmap(1600, 1600)
        try {
            val q75 = source.jpegBytes(1600, PHOTO_JPEG_QUALITY).size.toLong()
            val q60 = source.jpegBytes(1600, 60).size.toLong()
            val fallback1280 = source.jpegBytes(1280, 60).size.toLong()
            assertTrue("expected q75 ($q75) > q60 ($q60) > 1280@q60 ($fallback1280)", q75 > q60 && q60 > fallback1280)

            // Tight imageBytes below q75 must step down to quality 60.
            val stepQualityLimits = ImageLimits(maxSide = 1600, imageBytes = q75 - 1L)
            val qualityStepped = MediaShare.writeEditedPhoto(context, source, stepQualityLimits)
            assertEquals(q60, qualityStepped.size)
            assertEquals(q60, qualityStepped.scaledBytes)

            // Tight imageBytes below q60 must step down to 1280px at quality 60.
            val stepSideLimits = ImageLimits(maxSide = 1600, imageBytes = q60 - 1L)
            val sideStepped = MediaShare.writeEditedPhoto(context, source, stepSideLimits)
            assertEquals(fallback1280, sideStepped.size)
            assertEquals(fallback1280, sideStepped.scaledBytes)

            // Viewer admission must refuse (and clean up) when edited photo + unknown-size restored photo exceed budget.
            val composer = ChatComposerState().apply {
                editMessage("Before\n\n<attached_images>\n/tmp/remote.jpg\n</attached_images>")
            }
            val notices = mutableListOf<String>()
            val tightLimits = ImageLimits(
                requestBytes = ImageLimits.BODY_ROOM + ImageLimits.base64Bytes(fallback1280) + 100L,
                imageBytes = fallback1280,
                maxSide = 1600,
                perMessage = 10,
            )
            val admitted = admitViewerEditedPhoto(
                context = context,
                composer = composer,
                attachment = sideStepped,
                limits = tightLimits,
                canSpill = false,
                onNotice = notices::add,
            )
            assertFalse("unknown held photo + edited photo exceed budget", admitted)
            assertEquals(1, composer.attachments.size)
            assertEquals(listOf("These pictures are too large to send together"), notices)
            assertFalse("refused viewer edit capture must be deleted", attachmentReadable(context, sideStepped))

            // An edited attachment with unknown size (size = null, scaledBytes = null) must never be admitted as 0 bytes.
            val unmeasured = ComposerAttachment("unmeasured", "edited.jpg", "", image = true, size = null, scaledBytes = null)
            val unmeasuredAdmitted = admitViewerEditedPhoto(
                context = context,
                composer = composer,
                attachment = unmeasured,
                limits = tightLimits,
                canSpill = false,
            )
            assertFalse(unmeasuredAdmitted)

            // When budget allows, viewer-edited photo joins with its measured scaledBytes intact.
            val roomyComposer = ChatComposerState()
            val added = admitViewerEditedPhoto(
                context = context,
                composer = roomyComposer,
                attachment = qualityStepped,
                limits = stepQualityLimits,
                canSpill = true,
            )
            assertTrue(added)
            assertEquals(q60, roomyComposer.attachments.single().scaledBytes)
        } finally {
            source.recycle()
        }
    }

    private fun noisyBitmap(width: Int, height: Int): Bitmap {
        val random = Random(13043L)
        val pixels = IntArray(width * height) {
            Color.rgb(random.nextInt(256), random.nextInt(256), random.nextInt(256))
        }
        return Bitmap.createBitmap(pixels, width, height, Bitmap.Config.ARGB_8888)
    }

    @Config(qualifiers = "en")
    @Test fun viewerLimitStringAndEnglishResourcesMatchHubLimits() = runTest {
        val media = object : ChatMedia {
            override suspend fun save(bytes: ByteArray) = true
            override suspend fun share(bytes: ByteArray) = true
            override suspend fun copy(bytes: ByteArray) = true
        }
        val edit = chatViewerActions(context.resources, media, { true }, { null }).first { it.label == "Edit" }
        assertEquals("Up to 20 photos at a time", edit.onClick(0, byteArrayOf(1)))
        val customEdit = chatViewerActions(context.resources, media, { true }, { null }, maxPhotos = 7).first { it.label == "Edit" }
        assertEquals("Up to 7 photos at a time", customEdit.onClick(0, byteArrayOf(1)))

        assertEquals("Up to 20 photos at a time", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("Up to 4 videos at a time", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("These pictures are too large to send together", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("A message holds up to 20 pictures. The other one will go as a file.", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("A message holds up to 20 pictures. The other 3 will go as files.", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("These pictures are too large together. One of them will go as a file.", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("These pictures are too large together. 2 of them will go as files.", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }

    @Config(qualifiers = "zh")
    @Test fun chineseImageLimitStringsFormatCleanly() {
        assertEquals("一次最多 20 张", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("一次最多 4 个视频", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("这些图加起来太大了，没法一起发送", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("一条消息最多 20 张图，另外 1 张作为文件发送", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("一条消息最多 20 张图，另外 3 张作为文件发送", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("这些图加起来太大了，其中 1 张作为文件发送", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("这些图加起来太大了，其中 2 张作为文件发送", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }

    @Config(qualifiers = "ja")
    @Test fun japaneseImageLimitStringsFormatCleanly() {
        assertEquals("一度に20枚まで", context.getString(R.string.chat_composer_photo_limit, 20))
        assertEquals("動画は一度に4本まで", context.getString(R.string.chat_composer_video_limit, 4))
        assertEquals("画像の合計が大きすぎて、まとめて送れません", context.getString(R.string.chat_composer_photos_too_large))
        assertEquals("1件のメッセージに入れられる画像は20枚までです。残りの1枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_as_files_one, 20))
        assertEquals("1件のメッセージに入れられる画像は20枚までです。残りの3枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_as_files_other, 20, 3))
        assertEquals("画像の合計が大きすぎます。1枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_bytes_as_files_one))
        assertEquals("画像の合計が大きすぎます。2枚はファイルとして送ります", context.getString(R.string.chat_composer_photos_bytes_as_files_other, 2))
    }
}
