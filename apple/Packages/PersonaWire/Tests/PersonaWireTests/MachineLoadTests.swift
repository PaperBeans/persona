import Foundation
import Testing

@testable import PersonaWire

/// Load means one thing on every screen (#12519, docs/server/machines.md "What load counts"): the 1-minute load average
/// over the core count, written with one decimal, the figure the alert goes by too.
@Suite struct MachineLoadTests {
    @Test func loadPerCoreNeedsALoadAndACoreCount() {
        #expect(MetricFormat.loadPerCore(25.4, cores: 18).map(MetricFormat.perCore) == "1.4")
        #expect(MetricFormat.loadPerCore(25.4, cores: nil) == nil)
        #expect(MetricFormat.loadPerCore(25.4, cores: 0) == nil)
        #expect(MetricFormat.loadPerCore(nil, cores: 18) == nil)
    }

    @Test func perCoreHasOneDecimal() {
        #expect(MetricFormat.perCore(0) == "0.0")
        #expect(MetricFormat.perCore(0.04) == "0.0")
        #expect(MetricFormat.perCore(1.43) == "1.4")
        #expect(MetricFormat.perCore(12.345) == "12.3")
        // The raw value keeps its own format: two decimals under 10, one from 10.
        #expect(MetricFormat.load(3.1) == "3.10")
        #expect(MetricFormat.load(25.4) == "25.4")
    }

    /// The alert reads the same per-core figure the card shows: warning from 1, critical from 2, whatever the raw value.
    @Test func alertGoesByLoadPerCore() {
        let s = MetricSample(t: 0, cpu: 41, load: 25.4, memUsed: 50, memTotal: 100, swapUsed: 0, swapTotal: 0, diskUsed: 10, diskTotal: 100)
        #expect(MetricAlert.of(s, cores: 18).map(\.level) == [.warning]) // 1.41 per core
        #expect(MetricAlert.of(s, cores: 12).map(\.level) == [.critical]) // 2.12 per core
        #expect(MetricAlert.of(s, cores: 26).isEmpty) // 0.98 per core
        #expect(MetricAlert.of(s, cores: nil).isEmpty) // no core count: no load alert
    }
}
