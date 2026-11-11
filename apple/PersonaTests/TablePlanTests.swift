import Foundation
import MarkdownUI
import SwiftUI
import Testing
@testable import Persona

/// Column widths of a reply's table (`TablePlan`): natural when it fits, long columns wrap before the table scrolls.
struct TablePlanTests {
    @Test func fitsAsIs() {
        let p = TablePlan.fit(natural: [20, 80, 100], minimum: [20, 30, 30], available: 300)
        #expect(p == TablePlan(widths: [20, 80, 100], scrolls: false))
    }

    /// 「# / 事项 / 裁定」 with a long 「裁定」: # and 「事项」 keep their width, 「裁定」 takes the rest and wraps.
    @Test func longColumnWraps() {
        let p = TablePlan.fit(natural: [16, 120, 900], minimum: [16, 30, 30], available: 300)
        #expect(!p.scrolls)
        #expect(p.widths[0] == 16 && p.widths[1] == 120)
        #expect(p.widths[2] == 164)
        #expect(p.widths.reduce(0, +) <= 300)
    }

    /// Two long columns share in proportion; none goes under what cannot wrap.
    @Test func longColumnsShare() {
        let p = TablePlan.fit(natural: [16, 400, 800], minimum: [16, 150, 30], available: 300)
        #expect(!p.scrolls && p.widths[0] == 16 && p.widths[1] >= 150 && p.widths.reduce(0, +) <= 300)
    }

    /// Too many columns for the screen: it scrolls, each column capped so long text still wraps.
    @Test func scrollsWhenNothingFits() {
        let p = TablePlan.fit(natural: Array(repeating: 60, count: 6) + [500], minimum: Array(repeating: 50, count: 7), available: 300)
        #expect(p.scrolls && p.widths.last == TablePlan.scrollCap)
    }

    @Test func rowsOfMarkdown() {
        let rows = MarkdownTableText.rows("| # | 事项 | 裁定 |\n| --- | :-: | ---: |\n| 1 | **小组件** | 见 [文档](https://x) 和 `a\\|b` |")
        #expect(rows == [["#", "事项", "裁定"], ["1", "小组件", "见 文档 和 a|b"]])
    }

    /// Each column's alignment from the delimiter row, as GFM reads it (#3863): `--:` trailing, `:-:` centered, `:--`
    /// and `---` leading.
    @Test func alignmentsOfTheDelimiterRow() {
        #expect(MarkdownTableText.alignments("| # | 事项 | 金额 | 备注 |\n| --- | :-: | ---: | :-- |\n| 1 | 甲 | 12.50 | 乙 |")
            == [.leading, .center, .trailing, .leading])
        // Without the outer pipes, with longer runs and spaces.
        #expect(MarkdownTableText.alignments("a | b | c\n:----: |----:| ---\n1 | 2 | 3") == [.center, .trailing, .leading])
        // As MarkdownUI writes a table back out, which is what `FittedTable` measures.
        let rendered = MarkdownContent("| a | b | c | d |\n|:-|:-:|-:|-|\n| 1 | 2 | 3 | 4 |").renderMarkdown()
        #expect(MarkdownTableText.alignments(rendered) == [.leading, .center, .trailing, .leading])
        #expect(MarkdownTableText.measure(rendered, fontSize: 15).alignments == [.leading, .center, .trailing, .leading])
        // No delimiter row: none, and every cell is leading.
        #expect(MarkdownTableText.alignments("| a | b |\n| 1 | 2 |").isEmpty)
    }

    /// Digits are measured as the cells draw them, tabular, so a right-aligned column of numbers lines up and a number
    /// never wraps for a digit wider than measured: six 1s are as wide as six 8s.
    @Test func digitsAreMeasuredTabular() {
        let ones = MarkdownTableText.measure("| n |\n| --: |\n| 111111 |", fontSize: 15).natural
        let eights = MarkdownTableText.measure("| n |\n| --: |\n| 888888 |", fontSize: 15).natural
        #expect(ones == eights)
    }
}
