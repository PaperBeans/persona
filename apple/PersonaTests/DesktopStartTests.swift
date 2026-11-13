import Foundation
import Testing
@testable import Persona

/// New Agent in Claude's desktop app (#5480): the launcher it is, where browsing starts, a new folder's path.
struct DesktopStartTests {
    private static func session(_ id: String, agent: String = "claude", surface: String? = "desktop", cwd: String) -> Session {
        let surfaceField = surface.map { #","agent_surface":"\#($0)""# } ?? ""
        let json = #"{"machine_id":"m","session_id":"\#(id)","title":"\#(id)","cwd":"\#(cwd)","status":"idle","state":"idle","agent":"\#(agent)","updated_at":"2026-11-12T00:00:00Z"\#(surfaceField)}"#
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func theLauncherIsTheOneWhoseSourceIsTheDesktopApp() throws {
        let json = #"[{"name":"claude-desktop","base_url":"","model":"","source":"claude_desktop","agent":"claude"},{"name":"glm","base_url":"https://g","model":"glm-5","source":"zshrc"},{"name":"old","base_url":""}]"#
        let ls = try JSON.decoder.decode([Launcher].self, from: Data(json.utf8))
        #expect(ls.map(\.startsInDesktopApp) == [true, false, false])
        #expect(ls.map(\.source) == ["claude_desktop", "zshrc", nil])
        // Run Agent Here types the launcher into a shell: not the one that starts in the app.
        #expect(ls.map(NodeMenu.launchableHere) == [false, true, true])
    }

    @Test func browsingStartsWhereMostOfTheAppsProjectsAre() {
        let sessions = [
            Self.session("a", cwd: "/Users/u/Code/a"), Self.session("b", cwd: "/Users/u/Code/b"),
            Self.session("t", cwd: "/Users/u/Code/临时会话"), // the scratch folder's count for the folder it is in
            Self.session("c", cwd: "/Users/u/Elsewhere/c"), Self.session("d", cwd: "/Users/u/Elsewhere/d"),
            Self.session("x", agent: "codex", cwd: "/Users/u/Codex/x"), Self.session("y", agent: "codex", cwd: "/Users/u/Codex/y"),
            Self.session("z", agent: "codex", cwd: "/Users/u/Codex/z"), Self.session("w", agent: "codex", cwd: "/Users/u/Codex/w"),
            // the Codex App's and the terminal's are not the Claude app's, however many
            Self.session("cli1", surface: "cli", cwd: "/Users/u/Term/1"), Self.session("cli2", surface: "cli", cwd: "/Users/u/Term/2"),
            Self.session("cli3", surface: "cli", cwd: "/Users/u/Term/3"), Self.session("cli4", surface: "cli", cwd: "/Users/u/Term/4"),
            Self.session("root", cwd: "/top"), Self.session("rel", cwd: "relative/p"),
        ]
        #expect(DesktopStart.projectsFolder(sessions) == "/Users/u/Code")
        // A tie goes to the first by name, as on the computer.
        #expect(DesktopStart.projectsFolder([Self.session("e", cwd: "/w/B/e"), Self.session("f", cwd: "/w/A/f")]) == "/w/A")
        #expect(DesktopStart.projectsFolder([Self.session("g", surface: "cli", cwd: "/w/A/g")]) == nil)
    }

    @Test func aNewFolderIsOneNameInTheFolderBrowsed() {
        #expect(DesktopStart.newFolder("  报告 2026 ", in: "/Users/u/Code") == "/Users/u/Code/报告 2026")
        for bad in ["", "  ", ".", "..", "a/b", "/abs"] {
            #expect(DesktopStart.newFolder(bad, in: "/Users/u/Code") == nil, "\(bad)")
        }
        #expect(DesktopStart.newFolder("x", in: "") == nil)
        #expect(DesktopStart.newFolder("x", in: "relative") == nil)
    }
}
