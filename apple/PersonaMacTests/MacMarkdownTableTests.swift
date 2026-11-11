import AppKit
import Foundation
import MarkdownUI
import SwiftUI
import Testing
@testable import Persona

@MainActor
struct MacMarkdownTableTests {
    @Test func inlineFormattingAndEscapedPipesSurviveCellSplitting() throws {
        let content = MarkdownContent("| Name | Value |\n| --- | ---: |\n| **Bold** | *Italic* and `a\\|b` |\n| [Link](https://example.com) | ~~Old~~ |")
        let rows = MarkdownTablePreparation().inlineRows(content)
        #expect(rows.map { $0.map { String($0.characters) } } == [
            ["Name", "Value"], ["Bold", "Italic and a|b"], ["Link", "Old"],
        ])
        #expect(rows[1][0].runs.contains { $0.inlinePresentationIntent?.contains(.stronglyEmphasized) == true })
        #expect(rows[1][1].runs.contains { $0.inlinePresentationIntent?.contains(.emphasized) == true })
        #expect(rows[1][1].runs.contains { $0.inlinePresentationIntent?.contains(.code) == true })
        #expect(rows[2][0].runs.contains { $0.link == URL(string: "https://example.com") })
        #expect(rows[2][1].runs.contains { $0.inlinePresentationIntent?.contains(.strikethrough) == true })
    }

    @Test func onlyTheSecondRowIsADelimiter() {
        let source = "| - | : |\n| --- | ---: |\n| --- | - |\n| | Unicode 🙂 |"
        #expect(MarkdownTableText.rows(source) == [["-", ":"], ["---", "-"], ["", "Unicode 🙂"]])
    }

    @Test func currentAlignmentAndTabularMeasurementAreRetained() {
        let source = "| Left | Center | Right |\n| :--- | :---: | ---: |\n| 1 | 2 | 3 |"
        let measured = MarkdownTableText.measure(source, fontSize: 15)
        #expect(measured.alignments == [.leading, .center, .trailing])
        #expect(measured.natural.count == 3)
        #expect(MarkdownTableText.measure("| N |\n| --- |\n| 111 |", fontSize: 15).natural ==
                MarkdownTableText.measure("| N |\n| --- |\n| 888 |", fontSize: 15).natural)
    }

    @Test func inlinePreparationReusesSourceAndInvalidatesOnContentChange() {
        var renders = 0, measurements = 0
        let preparation = MarkdownTablePreparation(render: { content in
            renders += 1
            return content.renderMarkdown()
        }, measure: { markdown, size in
            measurements += 1
            return MarkdownTableText.measure(markdown, fontSize: size)
        })
        let first = MarkdownContent("| Name | Value |\n| --- | --- |\n| A | **One** |")
        let second = MarkdownContent("| Name | Value |\n| --- | --- |\n| B | **Two** |")
        for _ in 0..<100 {
            #expect(String(preparation.inlineRows(first)[1][1].characters) == "One")
            _ = preparation.measure(first, fontSize: 15)
        }
        _ = preparation.measure(first, fontSize: 30)
        #expect(renders == 1 && measurements == 2)
        #expect(String(preparation.inlineRows(second)[1][1].characters) == "Two")
        _ = preparation.measure(second, fontSize: 30)
        #expect(renders == 2 && measurements == 3)
    }

    @Test func tablesCanRelayoutAndScrollInAFlippedLazyStack() async throws {
        let source = "| **Name** | Center | Right |\n| :--- | :---: | ---: |\n| *Italic* | `a\\|b` | 123.45 |\n| [Link](https://example.com) | ~~Old~~ | 8.90 |"
        for scheme in [ColorScheme.light, .dark] {
            let page = ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(0..<24, id: \.self) { _ in
                        Markdown(source)
                            .markdownTheme(MarkdownUI.Theme().table { FittedTable(config: $0) })
                            .scaleEffect(x: 1, y: -1)
                    }
                }
                .scaleEffect(x: 1, y: -1)
            }
            .environment(\.colorScheme, scheme)
            .background(scheme == .light ? Color.white : Color.black)
            let host = NSHostingView(rootView: page)
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 640, height: 480),
                                  styleMask: [.titled], backing: .buffered, defer: false)
            window.isReleasedWhenClosed = false
            window.contentView = host
            window.orderFront(nil)
            defer { window.close() }
            try await Task.sleep(for: .milliseconds(200))
            func scrollView(in view: NSView) -> NSScrollView? {
                if let scroll = view as? NSScrollView { return scroll }
                return view.subviews.lazy.compactMap { scrollView(in: $0) }.first
            }
            let scroll = try #require(scrollView(in: host))
            var furthest: CGFloat = 0
            for offset in [0, 300, 900, 600, 1200, 0] {
                scroll.contentView.scroll(to: NSPoint(x: 0, y: offset))
                scroll.reflectScrolledClipView(scroll.contentView)
                host.layoutSubtreeIfNeeded()
                try await Task.sleep(for: .milliseconds(50))
                furthest = max(furthest, scroll.contentView.bounds.minY)
            }
            #expect(furthest > 0)
            let bitmap = try #require(host.bitmapImageRepForCachingDisplay(in: host.bounds))
            host.cacheDisplay(in: host.bounds, to: bitmap)
            #expect(bitmap.pixelsWide > 0 && bitmap.pixelsHigh > 0)
            let directory = URL(fileURLWithPath: NSTemporaryDirectory()).appending(path: "persona-table-validation")
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let image = directory.appending(path: scheme == .light ? "table-light.png" : "table-dark.png")
            try #require(bitmap.representation(using: .png, properties: [:])).write(to: image)
            print("Table validation screenshot: \(image.path)")
        }
    }
}
