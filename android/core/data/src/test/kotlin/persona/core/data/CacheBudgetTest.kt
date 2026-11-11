package persona.core.data

import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import persona.core.contracts.SessionKey
import java.io.File

class CacheBudgetTest {
    @get:Rule val temporary = TemporaryFolder()

    @Test fun fileCacheEvictsLeastRecentlyReadAndPreservesOpenFilesAcrossOwners() {
        val directory = temporary.newFolder()
        val cache = BudgetFileCache(directory, 8)
        val first = cache.put("first", ByteArray(4))
        val second = cache.put("second", ByteArray(4))
        first.setLastModified(1)
        second.setLastModified(2)
        cache.read("first")
        val third = cache.put("third", ByteArray(4))
        assertTrue(first.exists())
        assertFalse(second.exists())
        CacheFiles.retain(first).use {
            first.setLastModified(1)
            BudgetFileCache(directory, 8).put("fourth", ByteArray(4))
            assertTrue(first.exists())
            assertFalse(third.exists())
            assertTrue(runCatching { cache.put("large", ByteArray(9)) }.isFailure)
            assertTrue(runCatching { cache.put("blocked", ByteArray(8)) }.isFailure)
        }
        cache.put("last", ByteArray(8))
        assertFalse(first.exists())
        assertEquals(8L, directory.listFiles()!!.sumOf { it.length() })
    }

    /** A hit moves the file to the front without walking the directory; the next write or trim catches up (#4047). */
    @Test fun aReadHitTouchesTheFileAndLeavesTrimmingToWrites() {
        val directory = temporary.newFolder()
        val cache = BudgetFileCache(directory, 8)
        val old = cache.put("old", ByteArray(4)).apply { setLastModified(1_000) }
        val read = cache.put("read", ByteArray(4)).apply { setLastModified(2_000) }
        // Left over budget by a file that was in use when the last write trimmed.
        File(directory, "extra").apply { writeBytes(ByteArray(4)); setLastModified(3_000) }
        assertEquals(read, cache.read("read"))
        assertTrue(read.lastModified() > 3_000)
        assertNull(cache.read("missing"))
        assertEquals(setOf("old", "read", "extra"), directory.list()!!.toSet())
        cache.trim()
        assertFalse(old.exists())
        assertEquals(setOf("read", "extra"), directory.list()!!.toSet())
    }

    /** A visible session's images stay; its digest is worked out once per set of visible sessions (#4047). */
    @Test fun visibleSessionsKeepTheirImagesAndTheirPrefixesFollowTheVisibleSet() {
        val directory = temporary.newFolder()
        val (a, b, c) = listOf("a", "b", "c").map { SessionKey("m", it) }
        var visible = setOf(a)
        // A new set on every call, as SessionItemsCache.activeSessions() returns.
        val cache = DisplayImageCache(directory, 8) { visible.toSet() }
        val first = cache.store(a, "image", 10, 10, ByteArray(4)).apply { setLastModified(1_000) }
        val second = cache.store(b, "image", 10, 10, ByteArray(4)).apply { setLastModified(2_000) }
        val prefixes = cache.activePrefixes()
        assertEquals(listOf(cacheDigest(a.path) + "-"), prefixes)
        assertSame(prefixes, cache.activePrefixes())
        // Over budget: a is the oldest but visible, so b goes.
        cache.store(c, "image", 10, 10, ByteArray(4))
        assertTrue(first.exists())
        assertFalse(second.exists())
        // No longer visible: the next write over budget lets a's image go.
        visible = emptySet()
        assertEquals(emptyList<String>(), cache.activePrefixes())
        cache.store(b, "later", 10, 10, ByteArray(4))
        assertFalse(first.exists())
        assertEquals(4L, cache.read(c, "image", 10, 10)?.length())
    }

    @Test fun activeItemsSurviveOverBudgetUntilReleaseAndImagesFitWithoutUpscaling() {
        val entries = listOf(BudgetEntry("active", 12, 1), BudgetEntry("old", 4, 2), BudgetEntry("recent", 4, 3))
        assertEquals(listOf("old", "recent"), evictionCandidates(entries, 8) { it == "active" })
        assertEquals(listOf("active"), evictionCandidates(entries, 8) { false })
        assertEquals(300 to 150, displayImageSize(2400, 1200, 300, 200))
        assertEquals(20 to 40, displayImageSize(20, 40, 300, 200))
        assertEquals(1 to 200, displayImageSize(1, 10000, 300, 200))
    }

    @Test fun clearingKeepsPinnedReadsAndDownloadsInProgress() {
        val directory = temporary.newFolder()
        val cache = BudgetFileCache(directory, 64)
        val idle = cache.put("idle", ByteArray(4))
        cache.put("open", ByteArray(4))
        val pinned = requireNotNull(cache.readPinned("open"))
        val download = cache.begin("download")
        download.write(ByteArray(4))
        val part = requireNotNull(directory.listFiles { f -> f.name.endsWith(".part") }?.single())
        val kept = CacheClearing.delete(directory.walkTopDown().filter { it.isFile }.toList())
        assertEquals(2, kept)
        assertFalse(idle.exists())
        assertTrue(pinned.file.exists())
        assertTrue(part.exists())
        // The download still commits after a clear.
        download.commit().use { assertTrue(it.file.exists()) }
        download.close()
        pinned.close()
        assertEquals(0, CacheClearing.delete(listOf(pinned.file)))
        assertFalse(pinned.file.exists())
    }
}
