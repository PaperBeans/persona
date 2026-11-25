package persona.core.hub.errors

import java.io.File
import java.nio.file.Files
import java.time.Instant
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.generated.ExtensionErrorReport

class DevErrorCopyTest {
    private val directory: File = Files.createTempDirectory("dev-error-copy").toFile()
    @After fun cleanUp() { directory.deleteRecursively() }
    private val report = ExtensionErrorReport(id = "one", time = "2026-11-17T12:00:00Z", message = "failed")
    private val now = Instant.parse(report.time).toEpochMilli() + 3_600_000
    private fun queue(localCopy: (ExtensionErrorReport) -> Unit) = ErrorReportQueue(directory, clock = { now }, localCopy = localCopy)

    @Test fun aSuccessfulQueueWriteOffersTheSameReportToTheLocalCopy() {
        val copies = mutableListOf<ExtensionErrorReport>()
        val queue = queue { copies += it }
        assertTrue(queue.write(report))

        assertEquals(listOf(report), copies)
        assertEquals(listOf("one"), queue.pending().map { it.report.id })
    }

    @Test fun aLocalCopyFailureDoesNotUndoTheExistingHubQueue() {
        var called = false
        val queue = queue { called = true; error("local disk full") }
        assertTrue(queue.write(report))

        assertTrue("the failing copy was actually attempted", called)
        assertEquals(listOf("one"), queue.pending().map { it.report.id })
    }

    @Test fun theQueueStillPrunesAReportAfterSevenDays() {
        val queue = ErrorReportQueue(directory, clock = { now + ErrorReportQueue.MaxAgeMillis + 1 })
        assertTrue(queue.write(report))

        assertTrue(queue.pending().isEmpty())
    }
}
