#if DEBUG || PERF_SIGNPOST
import Darwin
import Foundation
import QuartzCore
import os

nonisolated struct MainThreadBlock: Sendable {
    var durationMS: Double
    var backtrace: [String]
}

nonisolated struct MainThreadWatchdogSnapshot: Sendable {
    let count: Int
    let maxMS: Double
    let longest: [MainThreadBlock]
}

private nonisolated struct MainThreadSample: Sendable {
    let addresses: [UInt64]
    let failure: String?
}

private nonisolated struct MainThreadFrame {
    var next: UInt64 = 0
    var returnAddress: UInt64 = 0
}

private nonisolated func perfStripPointer(_ value: UInt64) -> UInt64 {
    #if arch(arm64)
    // MACH_VM_ADDRESS_MASK: remove pointer-authentication bits while preserving every valid user address bit.
    return value & 0x00007FFFFFFFFFFF
    #else
    return value
    #endif
}

private nonisolated func perfReadFrame(_ address: UInt64) -> MainThreadFrame? {
    var frame = MainThreadFrame()
    var out = vm_size_t()
    let result = withUnsafeMutablePointer(to: &frame) {
        vm_read_overwrite(mach_task_self_, vm_address_t(address), vm_size_t(MemoryLayout<MainThreadFrame>.size),
                          vm_address_t(UInt(bitPattern: $0)), &out)
    }
    guard result == KERN_SUCCESS, Int(out) == MemoryLayout<MainThreadFrame>.size else { return nil }
    return frame
}

/// Reports main-thread blocks in DEBUG and PERF_SIGNPOST builds. A background thread pings the main run loop; after the
/// threshold it suspends the main Mach thread, copies its registers and frame-pointer chain, resumes it, and only then
/// symbolicates the addresses. No unwinder, allocator or symbol-lookup code runs inside a signal handler.
nonisolated final class MainThreadWatchdog: @unchecked Sendable {
    static let shared = MainThreadWatchdog()
    private static let retainedBlocks = 5

    private let lock = NSLock()
    private let samplingLock = NSLock()
    private let symbolicationLock = NSLock()
    private var worker: Thread?
    private var mainThread: thread_act_t?
    private var generation = 0
    private var count = 0
    private var maxMS = 0.0
    private var longest: [MainThreadBlock] = []
    private var current: MainThreadBlock?

    private init() {}

    func start(threshold: TimeInterval = 0.1, interval: TimeInterval = 0.025) {
        precondition(Thread.isMainThread)
        lock.lock()
        guard worker == nil else { lock.unlock(); return }
        generation += 1
        let generation = self.generation
        mainThread = pthread_mach_thread_np(pthread_self())
        // A typed value, not a literal: some batch builds import `Thread(block:)`'s block as `@MainActor`, and a literal
        // takes that isolation despite `@Sendable`, so the loop trapped on its own thread (#4703).
        let body: @Sendable () -> Void = { [self] in run(threshold: threshold, interval: interval, generation: generation) }
        let thread = Thread(block: body)
        worker = thread
        lock.unlock()
        thread.start()
    }

    func stop() {
        lock.lock()
        generation += 1
        worker?.cancel()
        worker = nil
        mainThread = nil
        current = nil
        lock.unlock()
        // Do not join the worker (its next ping needs this thread). Wait only for an in-flight Mach sample to resume it.
        samplingLock.lock()
        samplingLock.unlock()
    }

    func reset() {
        lock.lock()
        count = 0
        maxMS = 0
        longest = []
        current = nil
        lock.unlock()
    }

    var snapshot: MainThreadWatchdogSnapshot {
        lock.lock()
        defer { lock.unlock() }
        var blocks = longest
        if let current { blocks.append(current) }
        blocks.sort { $0.durationMS > $1.durationMS }
        return MainThreadWatchdogSnapshot(count: count, maxMS: maxMS, longest: Array(blocks.prefix(Self.retainedBlocks)))
    }

    /// JSON-ready counters and the longest stacks for `PerfCount` and the performance UI tests' `frames.json`.
    var report: [String: Any] {
        let snapshot = snapshot
        return [
            "count": snapshot.count,
            "max_ms": snapshot.maxMS,
            "longest": snapshot.longest.map { ["duration_ms": $0.durationMS, "backtrace": $0.backtrace] },
        ]
    }

    private func running(_ generation: Int) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return worker != nil && self.generation == generation
    }

    private func run(threshold: TimeInterval, interval: TimeInterval, generation: Int) {
        let answered = DispatchSemaphore(value: 0)
        let answeredAt = OSAllocatedUnfairLock(initialState: 0.0)
        let capacity = 66
        let sampleBuffer = UnsafeMutablePointer<UInt64>.allocate(capacity: capacity)
        defer { sampleBuffer.deallocate() }
        while running(generation), !Thread.current.isCancelled {
            answeredAt.withLock { $0 = 0 }
            let started = CACurrentMediaTime()
            DispatchQueue.main.async {
                answeredAt.withLock { $0 = CACurrentMediaTime() }
                answered.signal()
            }
            if answered.wait(timeout: .now() + threshold) == .success {
                Thread.sleep(forTimeInterval: interval)
                continue
            }

            // The answer may have timestamped just before its semaphore signal won the timeout race. A real answer below
            // the budget is not a block; an answer already over budget no longer has the blocked stack to capture.
            let answeredBeforeCapture = answeredAt.withLock { $0 }
            let earlyDurationMS = (answeredBeforeCapture - started) * 1000
            if answeredBeforeCapture > 0, earlyDurationMS < threshold * 1000 {
                _ = answered.wait(timeout: .now() + 0.01)
                Thread.sleep(forTimeInterval: interval)
                continue
            }
            let detectedMS = (CACurrentMediaTime() - started) * 1000
            let sample = answeredBeforeCapture > 0
                ? MainThreadSample(addresses: [], failure: "main thread answered before stack capture")
                : sampleMainThread(generation: generation, buffer: sampleBuffer, capacity: capacity)
            let rawStack = sample.addresses.map { String(format: "0x%016llx", $0) } + (sample.failure.map { [$0] } ?? [])
            let answeredBeforeSample = answeredBeforeCapture > 0 ? answeredBeforeCapture : answeredAt.withLock { $0 }
            let initialMS = answeredBeforeSample > 0 ? (answeredBeforeSample - started) * 1000 : detectedMS
            begin(durationMS: initialMS, backtrace: rawStack, generation: generation)

            // Raw addresses are already visible if symbolication itself has to wait for a runtime lock.
            guard running(generation) else { return }
            let symbols = symbolicate(sample.addresses)
            if !symbols.isEmpty { updateBacktrace(symbols, generation: generation) }
            while answered.wait(timeout: .now() + 0.01) == .timedOut {
                guard running(generation), !Thread.current.isCancelled else { return }
            }
            if symbols.isEmpty {
                let resolved = symbolicate(sample.addresses)
                if !resolved.isEmpty { updateBacktrace(resolved, generation: generation) }
            }
            let answeredAt = answeredAt.withLock { $0 }
            let durationMS = answeredAt > 0 ? (answeredAt - started) * 1000 : (CACurrentMediaTime() - started) * 1000
            finish(durationMS: durationMS, generation: generation)
            Thread.sleep(forTimeInterval: interval)
        }
    }

    private func sampleMainThread(generation: Int, buffer: UnsafeMutablePointer<UInt64>, capacity: Int) -> MainThreadSample {
        samplingLock.lock()
        defer { samplingLock.unlock() }
        guard running(generation) else { return MainThreadSample(addresses: [], failure: nil) }
        lock.lock()
        let main = mainThread
        lock.unlock()
        guard let main else { return MainThreadSample(addresses: [], failure: "main thread unavailable") }
        let suspended = thread_suspend(main)
        guard suspended == KERN_SUCCESS else {
            return MainThreadSample(addresses: [], failure: "thread_suspend failed (\(suspended))")
        }
        // Nothing in this suspended region allocates: the buffer belongs to the worker and all Mach reads write into it.
        let captured = captureMainThread(main, buffer: buffer, capacity: capacity)
        _ = thread_resume(main)
        let addresses = Array(UnsafeBufferPointer(start: buffer, count: captured.count))
        let failure: String?
        if captured.result != KERN_SUCCESS {
            failure = "thread_get_state failed (\(captured.result))"
        } else {
            failure = addresses.isEmpty ? "main-thread backtrace unavailable" : nil
        }
        return MainThreadSample(addresses: addresses, failure: failure)
    }

    private func captureMainThread(_ main: thread_act_t, buffer: UnsafeMutablePointer<UInt64>, capacity: Int)
        -> (count: Int, result: kern_return_t) {
        var pc: UInt64 = 0
        var lr: UInt64 = 0
        var fp: UInt64 = 0
        #if arch(arm64)
        var state = arm_thread_state64_t()
        var count = mach_msg_type_number_t(MemoryLayout<arm_thread_state64_t>.size / MemoryLayout<natural_t>.size)
        let result = withUnsafeMutablePointer(to: &state) {
            $0.withMemoryRebound(to: natural_t.self, capacity: Int(count)) {
                thread_get_state(main, ARM_THREAD_STATE64, $0, &count)
            }
        }
        if result == KERN_SUCCESS {
            pc = perfStripPointer(UInt64(state.__pc))
            lr = perfStripPointer(UInt64(state.__lr))
            fp = perfStripPointer(UInt64(state.__fp))
        }
        #elseif arch(x86_64)
        var state = x86_thread_state64_t()
        var count = mach_msg_type_number_t(MemoryLayout<x86_thread_state64_t>.size / MemoryLayout<natural_t>.size)
        let result = withUnsafeMutablePointer(to: &state) {
            $0.withMemoryRebound(to: natural_t.self, capacity: Int(count)) {
                thread_get_state(main, x86_THREAD_STATE64, $0, &count)
            }
        }
        if result == KERN_SUCCESS {
            pc = UInt64(state.__rip)
            fp = UInt64(state.__rbp)
        }
        #else
        let result = KERN_FAILURE
        #endif
        guard result == KERN_SUCCESS else { return (0, result) }

        var frames = 0
        if pc != 0, frames < capacity {
            buffer[frames] = pc
            frames += 1
        }
        if lr != 0, lr != pc, frames < capacity {
            buffer[frames] = lr
            frames += 1
        }
        for _ in 0..<64 {
            guard frames < capacity, fp != 0, fp & 0x7 == 0, let frame = perfReadFrame(fp) else { break }
            let returnAddress = perfStripPointer(frame.returnAddress)
            if returnAddress != 0 {
                buffer[frames] = returnAddress
                frames += 1
            }
            let next = perfStripPointer(frame.next)
            guard next > fp, next - fp < 16 * 1024 * 1024 else { break }
            fp = next
        }
        return (frames, result)
    }

    private func symbolicate(_ addresses: [UInt64]) -> [String] {
        // backtrace_symbols is also used by crash reporting; do not let an old and a new worker call it concurrently.
        symbolicationLock.lock()
        defer { symbolicationLock.unlock() }
        let pointers = addresses.map { UnsafeMutableRawPointer(bitPattern: UInt($0)) }
        return pointers.withUnsafeBufferPointer { frames -> [String] in
            guard let base = frames.baseAddress, let lines = backtrace_symbols(base, Int32(frames.count)) else { return [] }
            defer { free(lines) }
            return (0..<frames.count).compactMap { index in
                guard let line = lines[index] else { return nil as String? }
                return String(cString: line)
            }
        }
    }

    private func begin(durationMS: Double, backtrace: [String], generation: Int) {
        lock.lock()
        guard worker != nil, self.generation == generation else { lock.unlock(); return }
        count += 1
        maxMS = max(maxMS, durationMS)
        current = MainThreadBlock(durationMS: durationMS, backtrace: backtrace)
        lock.unlock()
        PerfTrace.event("Main thread block", String(format: "%.1f ms (detected)\n%@", durationMS, backtrace.joined(separator: "\n")))
    }

    private func updateBacktrace(_ backtrace: [String], generation: Int) {
        lock.lock()
        guard worker != nil, self.generation == generation, current != nil else { lock.unlock(); return }
        current?.backtrace = Array(backtrace.prefix(32))
        lock.unlock()
    }

    private func finish(durationMS: Double, generation: Int) {
        lock.lock()
        guard worker != nil, self.generation == generation, var block = current else { lock.unlock(); return }
        block.durationMS = durationMS
        maxMS = max(maxMS, durationMS)
        longest.append(block)
        longest.sort { $0.durationMS > $1.durationMS }
        longest = Array(longest.prefix(Self.retainedBlocks))
        current = nil
        lock.unlock()
    }
}
#endif
