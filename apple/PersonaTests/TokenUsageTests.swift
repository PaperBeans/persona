import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Token counts in K / M / B, the same rules as the web's formatTokens (#4973), and the usage API's shapes.
struct TokenUsageTests {
    @Test func formatsTokenCounts() {
        let cases: [(Int64, String)] = [
            (0, "0"), (7, "7"), (999, "999"),
            (1_000, "1K"), (1_250, "1.25K"), (1_999, "2K"), (12_340, "12.3K"), (31_000, "31K"), (999_499, "999K"),
            (999_500, "1M"), (1_000_000, "1M"), (2_960_000, "2.96M"), (178_000_000, "178M"), (999_999_999, "1B"),
            (1_170_000_000, "1.17B"), (25_000_000_000, "25B"), (-1_250, "-1.25K"),
        ]
        for (n, want) in cases {
            #expect(TokenFormat.count(n) == want, "\(n)")
        }
    }

    @Test func decodesAMachinesUsage() throws {
        let json = """
        {"usage": {"machine_id": "server", "online": true, "range": "24h", "step": 600, "launchers_known": false, "stored": true,
          "points": [{"t": 1800000000, "in": 10, "cache_write": 20, "cache_read": 270, "out": 5, "req": 3},
                     {"t": 1800000600, "gap": true, "in": 0, "cache_write": 0, "cache_read": 0, "out": 0, "req": 0}],
          "total": {"in": 10, "cache_write": 20, "cache_read": 270, "out": 5, "req": 3},
          "models": [{"name": "opus", "in": 10, "cache_write": 20, "cache_read": 270, "out": 5, "req": 3}],
          "launchers": [{"name": "", "in": 10, "cache_write": 20, "cache_read": 270, "out": 5, "req": 3}]}}
        """
        struct R: Decodable { var usage: MachineUsageSeries }
        let u = try JSONDecoder().decode(R.self, from: Data(json.utf8)).usage
        #expect(u.points.count == 2 && u.points[1].gap && !u.points[0].gap)
        #expect(u.total.total == 305 && u.total.hitRate == 0.9)
        #expect(u.minutesPerStep == 10 && UsageChartData.perMinute(u.points[0].tokens.total, u) == 31)
        #expect(!u.launchersKnown && MachineUsageSection.launcherName(u.launchers[0].name) != "")
        #expect(UsageChartData.offline(u).count == 1)
    }

    @Test func hitRateBreaksWhereThereAreTooFewReplies() {
        let p = { (t: Int64, req: Int) in UsagePoint(t: t, tokens: UsageTokens(input: 10, cacheRead: 90, requests: req)) }
        let u = MachineUsageSeries(machineID: "m", online: true, range: "1h", step: 60,
                                   points: [p(0, 5), p(60, 1), p(120, 4), UsagePoint(t: 180, tokens: UsageTokens(), gap: true), p(240, 3)],
                                   total: UsageTokens(), models: [], launchers: [], launchersKnown: true)
        let rates = UsageChartData.hitRate(u)
        #expect(rates.count == 3)
        #expect(Set(rates.map(\.segment)).count == 3) // three runs: each break starts a new line
    }

    @Test func aCardsUsageIsOptional() throws {
        let card = #"{"machine_id": "m", "online": true, "points": [], "usage": {"today": {"in": 1, "out": 2}, "per_minute": 40, "recent": [1, 2]}}"#
        let m = try JSONDecoder().decode(MachineMetrics.self, from: Data(card.utf8))
        #expect(m.usage?.today.total == 3 && m.usage?.perMinute == 40)
        let old = try JSONDecoder().decode(MachineMetrics.self, from: Data(#"{"machine_id": "m", "online": true, "points": []}"#.utf8))
        #expect(old.usage == nil)
    }
}
