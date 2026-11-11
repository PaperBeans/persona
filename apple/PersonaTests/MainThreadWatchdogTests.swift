#if DEBUG || PERF_SIGNPOST
import QuartzCore
import XCTest
@testable import Persona

final class MainThreadWatchdogTests: XCTestCase {
    @MainActor func testCapturesABlockedMainThreadWithItsBacktrace() {
        let watchdog = MainThreadWatchdog.shared
        watchdog.stop()
        watchdog.start(threshold: 0.1, interval: 0.01)
        defer { watchdog.stop() }
        watchdog.reset()

        // Keep the main thread in this test method: the signal handler must sample this stack, not the run loop after it.
        let until = CACurrentMediaTime() + 0.25
        var reportedWhileBlocked = false
        while CACurrentMediaTime() < until {
            if watchdog.snapshot.count == 1 { reportedWhileBlocked = true }
        }
        XCTAssertTrue(reportedWhileBlocked, "a persistent block must be visible before the main thread answers again")

        let deadline = Date.now.addingTimeInterval(2)
        var report = watchdog.snapshot
        while Date.now < deadline {
            report = watchdog.snapshot
            let stack = report.longest.first?.backtrace.joined(separator: "\n") ?? ""
            if report.maxMS >= 200, stack.contains("PersonaTests") { break }
            RunLoop.current.run(mode: .default, before: Date.now.addingTimeInterval(0.01))
        }
        XCTAssertEqual(report.count, 1)
        XCTAssertGreaterThanOrEqual(report.maxMS, 100)
        guard let block = report.longest.first else { return XCTFail("no main-thread block was recorded") }
        XCTAssertEqual(block.durationMS, report.maxMS)
        let stack = block.backtrace.joined(separator: "\n")
        XCTAssertTrue(stack.contains("PersonaTests") && stack.contains("testCapturesABlocked"),
                      "the sampled stack must contain the deliberately blocked test method: \(block.backtrace)")
    }

    @MainActor func testResponsiveMainThreadStaysQuiet() {
        let watchdog = MainThreadWatchdog.shared
        watchdog.stop()
        watchdog.start(threshold: 0.1, interval: 0.005)
        defer { watchdog.stop() }
        watchdog.reset()

        let deadline = Date.now.addingTimeInterval(0.25)
        while Date.now < deadline {
            RunLoop.current.run(mode: .default, before: Date.now.addingTimeInterval(0.01))
        }
        XCTAssertEqual(watchdog.snapshot.count, 0)
        XCTAssertEqual(watchdog.snapshot.maxMS, 0)
        XCTAssertTrue(watchdog.snapshot.longest.isEmpty)
    }
}
#endif
