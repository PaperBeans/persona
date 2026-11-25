package persona.core.contracts

import org.junit.Assert.assertEquals
import org.junit.Test

class ToolNamesTest {
    @Test fun stripToolResultTimestampHeadersStripsRfc3339PairsOnly() {
        val receipt = """{"level":"triage","summary":"All checks quiet.","visibility":"silent"}"""
        assertEquals(
            receipt,
            stripToolResultTimestampHeaders(
                "Created At: 2026-11-23T16:10:40+08:00\nCompleted At: 2026-11-23T16:10:44+08:00\n" +
                    "Created At: 2026-11-23T16:10:40+08:00\nCompleted At: 2026-11-23T16:10:44+08:00\n$receipt",
            ),
        )
        assertEquals(
            receipt,
            stripToolResultTimestampHeaders(
                "Created At: 2026-11-23T09:10:40.125Z\r\nCompleted At: 2026-11-23T09:10:44.500Z\r\n$receipt",
            ),
        )
        assertEquals(
            receipt,
            stripToolResultTimestampHeaders(
                "Created At: 2026-11-23T09:10:40Z\nCompleted At: 2026-11-23T09:10:44Z\nOutput:\n$receipt",
            ),
        )
        assertEquals(
            "Showing lines 1-3\nOutput:\nkeep me",
            stripToolResultTimestampHeaders(
                "Created At: 2026-11-23T09:10:40Z\nCompleted At: 2026-11-23T09:10:44Z\nShowing lines 1-3\nOutput:\nkeep me",
            ),
        )
        assertEquals(
            "Created At: yesterday\nNotes",
            stripToolResultTimestampHeaders(
                "Created At: 2026-11-23T16:10:40+08:00\nCompleted At: 2026-11-23T16:10:44+08:00\nCreated At: yesterday\nNotes",
            ),
        )
        val plainDateOnly = "Created At: 2026-11-23\nCompleted At: 2026-11-23\nHello"
        assertEquals(plainDateOnly, stripToolResultTimestampHeaders(plainDateOnly))
        val singleCreatedAt = "Created At: 2026-11-23T16:10:40+08:00\nTitle: Notes"
        assertEquals(singleCreatedAt, stripToolResultTimestampHeaders(singleCreatedAt))
        val nonLeading = "Summary:\nCreated At: 2026-11-23T16:10:40+08:00\nCompleted At: 2026-11-23T16:10:44+08:00\nBody"
        assertEquals(nonLeading, stripToolResultTimestampHeaders(nonLeading))
        val malformedCompleted = "Created At: 2026-11-23T16:10:40+08:00\nCompleted At: soon\n$receipt"
        assertEquals(malformedCompleted, stripToolResultTimestampHeaders(malformedCompleted))
    }
}
