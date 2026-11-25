import Foundation
import XCTest
@testable import PersonaLink

final class HelperProcessTests: XCTestCase {
    func testOnceFirstClaimWinsAndLaterClaimsLose() {
        let once = Once()
        XCTAssertTrue(once.claim())
        XCTAssertFalse(once.claim())
        XCTAssertFalse(once.claim())
    }

    func testOnceHasExactlyOneWinnerAcrossQueues() {
        let once = Once()
        let counter = NSLock()
        var wins = 0
        DispatchQueue.concurrentPerform(iterations: 500) { _ in
            if once.claim() {
                counter.lock()
                wins += 1
                counter.unlock()
            }
        }
        XCTAssertEqual(wins, 1)
        XCTAssertFalse(once.claim())
    }

    func testHelperSplitsStdoutAndStderr() throws {
        let script = try makeScript("#!/bin/sh\necho out\necho err >&2\nexit 0\n")
        let r = waitHelper(executable: script, stdin: nil)
        XCTAssertTrue(r.ok)
        XCTAssertEqual(r.out, "out")
        XCTAssertEqual(r.err, "err")
        XCTAssertEqual(r.message, "out")
    }

    func testHelperFailureReportsStderr() throws {
        let script = try makeScript("#!/bin/sh\necho boom >&2\nexit 3\n")
        let r = waitHelper(executable: script, stdin: nil)
        XCTAssertFalse(r.ok)
        XCTAssertEqual(r.out, "")
        XCTAssertEqual(r.err, "boom")
        XCTAssertEqual(r.message, "boom")
    }

    func testHelperThatExitsBeforeReadingLargeStdinResumesOnce() throws {
        let script = try makeScript("#!/bin/sh\nexit 0\n")
        // Over 1 MB, far larger than the pipe buffer: a reader that exits immediately makes the write fail with a
        // broken pipe, which used to resume the continuation a second time and trap ("more than once").
        let stdin = String(repeating: "x", count: 2 * 1024 * 1024)
        for _ in 0..<5 {
            let r = waitHelper(executable: script, stdin: stdin, timeout: 20)
            // Termination and the broken-pipe write race: either side may win, but exactly one of them does.
            if r.ok {
                XCTAssertEqual(r.message, r.out)
            } else {
                XCTAssertFalse(r.message.isEmpty)
            }
        }
    }

    // MARK: - helpers

    private typealias HelperResult = (ok: Bool, message: String, out: String, err: String)

    private func waitHelper(executable: URL, stdin: String?, timeout: TimeInterval = 15) -> HelperResult {
        let finished = expectation(description: "helper finished")
        let box = ResultBox<HelperResult>()
        Task {
            let r = await LinkModel.runHelper(executable: executable, arguments: [], stdin: stdin,
                                              environment: ["PATH": "/usr/bin:/bin"], split: true)
            box.set(r)
            finished.fulfill()
        }
        wait(for: [finished], timeout: timeout)
        return box.value
    }

    @discardableResult
    private func makeScript(_ body: String) throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        addTeardownBlock { try? FileManager.default.removeItem(at: dir) }
        let url = dir.appendingPathComponent("script.sh")
        try body.write(to: url, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: NSNumber(value: 0o755)], ofItemAtPath: url.path)
        return url
    }
}

/// Holds a value handed across the Task boundary in tests.
private final class ResultBox<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: T?

    func set(_ value: T) {
        lock.lock()
        stored = value
        lock.unlock()
    }

    var value: T {
        lock.lock()
        defer { lock.unlock() }
        return stored!
    }
}
