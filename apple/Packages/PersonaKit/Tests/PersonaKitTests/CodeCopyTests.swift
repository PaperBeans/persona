import Foundation
import Testing
@testable import PersonaKit

/// Inline code a tap copies (#2984): each code span outside code blocks, formulas and links becomes a link that
/// copies what is in it; everything else is the same Markdown.
struct CodeCopyTests {
    private func url(_ code: String) -> String { CodeCopyURL.make(code)!.absoluteString }

    @Test func codeSpansCopyWhatIsInThem() {
        let md = "跑 `swift test`，再看 ``a `b` c`` 和 ` 空格 `。"
        #expect(CodeCopyMarkdown.linked(md)
            == "跑 [`swift test`](\(url("swift test")))，再看 [``a `b` c``](\(url("a `b` c"))) 和 [` 空格 `](\(url("空格")))。")
    }

    @Test func codeBlocksFormulasAndLinksStayAsTheyAre() {
        let link = PathLinkURL.make("src/a.ts", line: nil)!.absoluteString
        let md = "见 [`src/a.ts`](\(link))\n\n```sh\necho `x`\n```\n\n    `indented`\n\n![x](persona-math:x) [`在链接里`](https://example.com)"
        #expect(CodeCopyMarkdown.linked(md) == md)
        #expect(CodeCopyMarkdown.linked("没有代码。") == "没有代码。")
    }

    @Test func theURLGivesTheCodeBack() throws {
        for code in ["swift test", "a/b?c=d&e#f", "中文 (括号) [方括号]", "`x`"] {
            let u = try #require(CodeCopyURL.make(code))
            #expect(CodeCopyURL.parse(u) == code)
        }
        #expect(CodeCopyURL.parse(URL(string: "https://example.com")!) == nil)
    }
}
