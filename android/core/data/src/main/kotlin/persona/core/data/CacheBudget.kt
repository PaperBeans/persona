package persona.core.data

import java.io.File
import java.io.IOException
import java.io.OutputStream
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.security.MessageDigest

/** Policy defaults only; unmeasured. Override when assembling the pairing's data store. */
data class CacheBudgets(
    val items: Long = 32L * 1024 * 1024,
    val images: Long = 128L * 1024 * 1024,
    val inboxMedia: Long = 128L * 1024 * 1024,
) {
    init { require(items >= 0 && images >= 0 && inboxMedia >= 0) }
}

internal data class BudgetEntry<T>(val key: T, val bytes: Long, val accessed: Long)

internal fun <T> evictionCandidates(entries: List<BudgetEntry<T>>, limit: Long, protected: (T) -> Boolean): List<T> {
    var total = entries.sumOf { it.bytes }
    return buildList {
        for (entry in entries.sortedBy { it.accessed }) {
            if (total <= limit) break
            if (!protected(entry.key)) { add(entry.key); total -= entry.bytes }
        }
    }
}

internal fun cacheDigest(value: String): String = MessageDigest.getInstance("SHA-256")
    .digest(value.toByteArray(Charsets.UTF_8)).joinToString("") { "%02x".format(it) }

/** Serializes readers, writers and descriptor leases across owners of the same directory. */
internal object CacheFiles {
    val lock = Any()
    private val readers = mutableMapOf<String, Int>()
    fun pinned(file: File) = (readers[file.absolutePath] ?: 0) > 0
    fun retain(file: File): AutoCloseable = synchronized(lock) {
        val path = file.absolutePath
        readers[path] = (readers[path] ?: 0) + 1
        var closed = false
        AutoCloseable { synchronized(lock) {
            if (!closed) {
                closed = true
                val count = readers.getValue(path) - 1
                if (count == 0) readers.remove(path) else readers[path] = count
            }
        } }
    }
}

internal class BudgetFileLease(val file: File, private val lease: AutoCloseable) : AutoCloseable {
    override fun close() = lease.close()
}

internal class BudgetFileCache(
    private val directory: File,
    private val limit: Long,
    private val protected: (File) -> Boolean = { false },
) {
    init { require(limit >= 0) }
    /**
     * A hit only moves the file to the front of the LRU. It adds no bytes, so it does not trim: walking the whole
     * directory on every hit, under the app-wide cache lock, made each image of a chat wait for the others (#4047).
     * Writes trim; a cache left over budget by files that were in use catches up at the next write or [trim].
     */
    fun read(name: String): File? = synchronized(CacheFiles.lock) {
        val file = File(directory, name)
        if (!file.isFile) null else file.also { it.setLastModified(System.currentTimeMillis()) }
    }

    fun readPinned(name: String): BudgetFileLease? = synchronized(CacheFiles.lock) {
        read(name)?.let { BudgetFileLease(it, CacheFiles.retain(it)) }
    }

    /** Create and commit under the pairing lock; streaming only writes an already-open descriptor. */
    fun begin(name: String): PendingWrite = synchronized(CacheFiles.lock) {
        val destination = File(directory, name)
        val parent = requireNotNull(destination.parentFile)
        check(parent.isDirectory || parent.mkdirs())
        val temporary = File.createTempFile("cache-", ".part", parent)
        val lease = CacheFiles.retain(temporary)
        try { PendingWrite(destination, temporary, temporary.outputStream(), lease) }
        catch (failure: Exception) { temporary.delete(); lease.close(); throw failure }
    }

    inner class PendingWrite internal constructor(
        private val destination: File,
        private val temporary: File,
        private val output: OutputStream,
        private val lease: AutoCloseable,
    ) : OutputStream() {
        private var closed = false

        override fun write(value: Int) = write(byteArrayOf(value.toByte()), 0, 1)

        override fun write(bytes: ByteArray, offset: Int, length: Int) = synchronized(CacheFiles.lock) {
            check(!closed)
            require(offset >= 0 && length >= 0 && offset <= bytes.size - length)
            checkBudget(temporary, temporary.length() + length)
            output.write(bytes, offset, length)
            trim(temporary)
        }

        fun commit(): BudgetFileLease = synchronized(CacheFiles.lock) {
            check(!closed)
            // Another completed download or an external viewer may own the destination.
            if (isProtected(destination)) {
                return@synchronized requireNotNull(readPinned(directory.toPath().relativize(destination.toPath()).toString()))
            }
            output.close()
            closed = true
            Files.move(temporary.toPath(), destination.toPath(), StandardCopyOption.ATOMIC_MOVE,
                StandardCopyOption.REPLACE_EXISTING)
            destination.setLastModified(System.currentTimeMillis())
            val result = BudgetFileLease(destination, CacheFiles.retain(destination))
            try { trim(destination); result }
            catch (failure: Exception) { result.close(); throw failure }
        }

        override fun close() = synchronized(CacheFiles.lock) {
            try {
                if (!closed) { closed = true; output.close() }
            } finally { temporary.delete(); lease.close() }
        }
    }

    fun put(name: String, bytes: ByteArray): File = synchronized(CacheFiles.lock) {
        check(directory.isDirectory || directory.mkdirs())
        val destination = File(directory, name)
        checkBudget(destination, bytes.size.toLong())
        val temporary = File.createTempFile("cache-", ".tmp", directory)
        try {
            temporary.writeBytes(bytes)
            Files.move(temporary.toPath(), destination.toPath(), StandardCopyOption.ATOMIC_MOVE,
                StandardCopyOption.REPLACE_EXISTING)
        } finally { temporary.delete() }
        trim(destination)
        destination
    }

    fun trim(keep: File? = null) = synchronized(CacheFiles.lock) {
        evictionCandidates(entries(), limit) { it == keep || isProtected(it) }.forEach {
            if (!it.delete() && it.exists()) throw IOException("could not clear the local cache")
        }
    }

    private fun isProtected(file: File) = CacheFiles.pinned(file) || protected(file)
    private fun checkBudget(file: File, bytes: Long) {
        if (bytes > limit) throw IOException("file exceeds the local cache budget")
        val protectedBytes = entries().filter { it.key != file && isProtected(it.key) }.sumOf { it.bytes }
        if (protectedBytes > limit - bytes) throw IOException("files in use fill the local cache budget")
    }
    private fun entries() = directory.walkTopDown().filter { it.isFile }.map {
        BudgetEntry(it, it.length(), it.lastModified())
    }.toList()
}

/**
 * Settings › Storage's clearing of the caches: deletes under the cache lock, so it never races a [BudgetFileCache]
 * write, and keeps files still in use (a pinned read, a download's `.part` file being written).
 */
object CacheClearing {
    /** Deletes [files] that are not in use; returns how many stayed because they were. */
    fun delete(files: List<File>): Int = synchronized(CacheFiles.lock) {
        files.count { file -> CacheFiles.pinned(file) || (!file.delete() && file.exists()) }
    }
}
