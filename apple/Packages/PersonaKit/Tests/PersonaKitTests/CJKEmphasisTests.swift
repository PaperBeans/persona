import Foundation
import Testing
@testable import PersonaKit

/// Bold and italics next to CJK punctuation (#3863): CommonMark leaves a bold label with a full-width colon and no space
/// after it open, so the asterisks showed; the punctuation moves outside the pair before the reply is parsed, only where
/// CommonMark would leave it open.
struct CJKEmphasisTests {
    /// The web's fixture (`web/src/ui/remark-gfm-parse.test.tsx`, remark-cjk-friendly): bold around 「」, italics around
    /// （）, and `__…__` that CommonMark already reads.
    private static let web = "这是**「强调」**的文字，*（斜体）*也是，__下划线__（括号）"

    @Test func theWebFixtureReadsAsTheWebReadsIt() throws {
        let out = CJKEmphasis.apply(Self.web)
        #expect(out == "这是「**强调**」的文字，（*斜体*）也是，__下划线__（括号）")
        // Parsed by CommonMark, as the chat's renderer parses it: before, both stayed literal asterisks.
        #expect(try shown(Self.web).contains("**"))
        #expect(try shown(out) == "这是「强调」的文字，（斜体）也是，下划线（括号）")
        #expect(try runs(out, .stronglyEmphasized) == ["强调", "下划线"])
        #expect(try runs(out, .emphasized) == ["斜体"])
        // The web's streaming test.
        #expect(try runs(CJKEmphasis.apply("这是**「重要」**的事"), .stronglyEmphasized) == ["重要"])
    }

    @Test func aBoldLabelBeforeItsText() throws {
        #expect(CJKEmphasis.apply("**注意：**后文") == "**注意**：后文")
        #expect(CJKEmphasis.apply("**完成。**下一步") == "**完成**。下一步")
        #expect(CJKEmphasis.apply("**注意：**一，**提示：**二") == "**注意**：一，**提示**：二")
        #expect(CJKEmphasis.apply("***注意：***后") == "***注意***：后")
        #expect(CJKEmphasis.apply("- **注意：**后文") == "- **注意**：后文")
        #expect(CJKEmphasis.apply("#### **注意：**后文") == "#### **注意**：后文")
        // An ASCII colon in Chinese text: the letter after it is CJK.
        #expect(CJKEmphasis.apply("**注意:**后文") == "**注意**:后文")
        #expect(try runs(CJKEmphasis.apply("**注意：**后文"), .stronglyEmphasized) == ["注意"])
    }

    /// Brackets or quotes around the words go out together, even when only one side was open; other punctuation inside
    /// stays where it is.
    @Test func bracketsMoveOutWhole() {
        #expect(CJKEmphasis.apply("**「强调」**的") == "「**强调**」的")
        #expect(CJKEmphasis.apply("**“引号”**中文") == "“**引号**”中文")
        #expect(CJKEmphasis.apply("**（1）注意：**后") == "**（1）注意**：后")
        #expect(CJKEmphasis.apply("* 这是*（斜体）*也是") == "* 这是（*斜体*）也是") // a list's marker is no pair
        #expect(CJKEmphasis.apply("**「方案（推荐）」：**后") == "「**方案（推荐）**」：后")
    }

    /// A bracket whose partner stays inside the bold stays with it: only what is past it moves, and with nothing past it
    /// the pair is left as written rather than bolded with half a bracket.
    @Test func aBracketStaysWithItsPartner() throws {
        #expect(CJKEmphasis.apply("**方案 A（推荐）：**使用") == "**方案 A（推荐）**：使用")
        #expect(try runs(CJKEmphasis.apply("**方案 A（推荐）：**使用"), .stronglyEmphasized) == ["方案 A（推荐）"])
        #expect(CJKEmphasis.apply("**第一步（可选）：**安装依赖") == "**第一步（可选）**：安装依赖")
        #expect(CJKEmphasis.apply("**注意（重要）。**后") == "**注意（重要）**。后")
        #expect(CJKEmphasis.apply("**a（b「c」）：**后") == "**a（b「c」）**：后")
        for text in ["**注意（重要）**后", "这是**（重要）说明**的", "这是**“重要”说明**的"] {
            #expect(CJKEmphasis.apply(text) == text, "\(text)")
        }
    }

    /// An image's `!` stays before its `[`, and an escaped character stays where it is.
    @Test func imagesAndEscapesStay() {
        #expect(CJKEmphasis.apply("**![图](https://example.com/a.png)：**后") == "**![图](https://example.com/a.png)**：后")
        for text in ["中文**![图](https://example.com/a.png)：**后", "中文**（注意）!**[链接](https://example.com)", "**注意\\!**后"] {
            #expect(CJKEmphasis.apply(text) == text, "\(text)")
        }
    }

    /// What CommonMark already reads, and what is not about CJK, stays exactly as it is.
    @Test func whatAlreadyReadsIsUntouched() {
        for text in ["**注意**：后文", "**注意：** 后文", "前文**注意：**", "**结论**完成", "plain *a* text", "**Note:**after",
                     "2*3*（4）中", "**注意：*后*文**", "| a | b |\n| - | - |\n| **注意**：后 | 1 |", "没有星号：只是文字"] {
            #expect(CJKEmphasis.apply(text) == text, "\(text)")
        }
    }

    /// Code, formulas, escapes and URLs are not prose: nothing in them moves.
    @Test func codeFormulasAndLinksStay() {
        #expect(CJKEmphasis.apply("`**注意：**后` 和 **注意：**后") == "`**注意：**后` 和 **注意**：后")
        #expect(CJKEmphasis.apply("```\n**注意：**后\n```\n**注意：**后") == "```\n**注意：**后\n```\n**注意**：后")
        #expect(CJKEmphasis.apply("~~~md\n**注意：**后\n~~~") == "~~~md\n**注意：**后\n~~~")
        #expect(CJKEmphasis.apply("$$\na*（b）*c\n$$") == "$$\na*（b）*c\n$$")
        // A line that is one formula becomes a math fence (`MathMarkdown`).
        #expect(CJKEmphasis.apply("$$总价 = 单价*（1+税率）*数量$$") == "$$总价 = 单价*（1+税率）*数量$$")
        #expect(CJKEmphasis.apply("\\[a*（b）*c\\]") == "\\[a*（b）*c\\]")
        #expect(CJKEmphasis.apply("\\(a*（b）*c\\)中") == "\\(a*（b）*c\\)中")
        #expect(CJKEmphasis.apply("\\*\\*注意：\\*\\*后") == "\\*\\*注意：\\*\\*后")
        #expect(CJKEmphasis.apply("看 https://example.com/a**：**b 吧") == "看 https://example.com/a**：**b 吧")
        #expect(CJKEmphasis.apply("**[链接](https://example.com/a*b)：**后") == "**[链接](https://example.com/a*b)**：后")
        // Four spaces in is code after a blank line or a heading; inside a list, or under a paragraph's line, it is text.
        #expect(CJKEmphasis.apply("前文\n\n    **注意：**后") == "前文\n\n    **注意：**后")
        #expect(CJKEmphasis.apply("## 标题\n    **注意：**后") == "## 标题\n    **注意：**后")
        #expect(CJKEmphasis.apply("- 项\n\n    **注意：**后") == "- 项\n\n    **注意**：后")
        #expect(CJKEmphasis.apply("前文\n    **注意：**后") == "前文\n    **注意**：后")
        // A fence under a list item, however far in, after a list marker, or in a quote; what comes after it is text.
        let listed = "1. 安装：\n\n    ```md\n    **注意：**后文\n    ```\n\n**注意：**后"
        #expect(CJKEmphasis.apply(listed) == "1. 安装：\n\n    ```md\n    **注意：**后文\n    ```\n\n**注意**：后")
        let nested = "- 一\n  - 二\n\n    ```md\n    **注意：**后文\n    ```"
        #expect(CJKEmphasis.apply(nested) == nested)
        #expect(CJKEmphasis.apply("- ```md\n  **注意：**后\n  ```\n- **注意：**后") == "- ```md\n  **注意：**后\n  ```\n- **注意**：后")
        #expect(CJKEmphasis.apply("> ```md\n> **注意：**后\n> ```\n**注意：**后") == "> ```md\n> **注意：**后\n> ```\n**注意**：后")
        // The quote ends without closing its fence: so does the fence.
        #expect(CJKEmphasis.apply("> ```md\n> **注意：**后\n\n**注意：**后") == "> ```md\n> **注意：**后\n\n**注意**：后")
        // CRLF lines are not told apart: left as written.
        #expect(CJKEmphasis.apply("```\r\n**注意：**后\r\n```") == "```\r\n**注意：**后\r\n```")
    }

    /// A table's cells are mended one by one; a pair never reaches across a `|`.
    @Test func tableCells() {
        #expect(CJKEmphasis.apply("| **注意：**后 | x |") == "| **注意**：后 | x |")
        #expect(CJKEmphasis.apply("| a* | *（b） |") == "| a* | *（b） |")
    }

    @Test func mendingTwiceChangesNothing() {
        for text in [Self.web, "**注意：**后文", "**「强调」**的", "* 这是*（斜体）*也是", "**方案 A（推荐）：**使用"] {
            let once = CJKEmphasis.apply(text)
            #expect(CJKEmphasis.apply(once) == once)
        }
    }

    /// The text CommonMark shows (the marks it read gone, the ones it did not left in).
    private func shown(_ markdown: String) throws -> String {
        String(try AttributedString(markdown: markdown, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)).characters)
    }

    /// The runs CommonMark set with `intent`, in order.
    private func runs(_ markdown: String, _ intent: InlinePresentationIntent) throws -> [String] {
        let parsed = try AttributedString(markdown: markdown, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))
        return parsed.runs.compactMap { run in
            (run.inlinePresentationIntent ?? []).contains(intent) ? String(parsed[run.range].characters) : nil
        }
    }
}
