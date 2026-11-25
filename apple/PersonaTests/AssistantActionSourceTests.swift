import Foundation
import Testing
@testable import Persona

struct AssistantActionSourceTests {
    private let tag = #"<persona-action view="item/0" action="choose">{"values":{"task":"读书"}}</persona-action>"#
    @Test func canonicalSourceBecomesMetadataAndPreservesReplyBytes() throws {
        let text = "原文🧪 e\u{301}\n" + tag
        let source = try #require(AssistantActionSource.parse(text))
        #expect(Array(source.text.utf8) == Array("原文🧪 e\u{301}".utf8))
        #expect(source.view == "item/0" && source.action == "choose" && source.values == ["task": "读书"] && !source.html)
    }
    @Test(arguments: ["\n", "\r\n", "\r"])
    func ownLineAndOriginalNewlineAreRecognized(_ ending: String) throws {
        let source = try #require(AssistantActionSource.parse("原文" + ending + tag + ending))
        #expect(source.text == "原文")
    }
    @Test func XMLAttributesAreDecodedExactlyOnce() throws {
        let escape = String(decoding: [UInt8(92)], as: UTF8.self)
        let tag = #"<persona-action view="item&amp;&#34;/0" action="choose&#39;&amp;quot;">{"values":{"task":"<原文>"}}</persona-action>"#
            .replacingOccurrences(of: "<原文>", with: escape + "u003c原文" + escape + "u003e")
        let source = try #require(AssistantActionSource.parse("原文\n" + tag))
        #expect(source.view == "item&\"/0" && source.action == "choose'&quot;" && source.values["task"] == "<原文>")
    }
    @Test func HTMLSourceIsStrictlyBooleanAndHasNoSubmittedFields() throws {
        let valid = #"<persona-action view="view_example" action="drawing">{"html":true,"values":{}}</persona-action>"#
        #expect(try #require(AssistantActionSource.parse("原文\n" + valid)).html)
        for invalid in [valid.replacingOccurrences(of: "true", with: "1"), valid.replacingOccurrences(of: "true", with: "false"),
                        valid.replacingOccurrences(of: "{}", with: #"{"secret":"no"}"#)] {
            #expect(AssistantActionSource.parse("原文\n" + invalid) == nil)
        }
    }
    @Test func malformedOrUnrecognizedTagsRemainVisible() {
        let variants = [tag + "\n后文", "原文 " + tag,
            tag.replacingOccurrences(of: "persona-action", with: "other-action"),
            tag.replacingOccurrences(of: "\"choose\"", with: "'choose'"),
            tag.replacingOccurrences(of: "item/0", with: "item&unknown;/0"),
            tag.replacingOccurrences(of: "item/0", with: ""),
            tag.replacingOccurrences(of: #""读书""#, with: "12"),
            tag.replacingOccurrences(of: #"{"values""#, with: #"{"extra":true,"values""#),
            tag.replacingOccurrences(of: "读书", with: "<原文>")]
        for invalid in variants {
            let text = "原文\n" + invalid
            #expect(AssistantActionSource.parse(text) == nil)
            #expect(AssistantActionSource.stripping(text) == text)
        }
    }
    @Test(arguments: ["```xml", "````markdown", "~~~", "~~~~xml"])
    func anUnfinishedCodeExampleIsNeverHidden(_ opening: String) {
        let text = opening + "\n原文\n" + tag
        #expect(AssistantActionSource.parse(text) == nil)
        #expect(AssistantActionSource.stripping(text) == text)
    }
    @Test func aClosedExampleBeforeTheRealMarkerRemainsInTheReply() throws {
        let prefix = "````markdown\n```xml\n" + tag + "\n```\n````\n原文"
        let source = try #require(AssistantActionSource.parse(prefix + "\n" + tag))
        #expect(source.text == prefix)
    }
    /// A tap on a kept Dashboards page goes back to its conversation as the owner speaking (#15017): its source names
    /// the page (`-pages/<page id>`) and is hidden in the bubble like a live card's.
    @Test func aDashboardsPageTapIsStripped() throws {
        let tag = #"<persona-action view="-pages/pg_3f9a1c0d5e7b2a41" action="done_today">{"values":{"note":"轻松跑"}}</persona-action>"#
        let text = "今天完成\n" + tag
        let source = try #require(AssistantActionSource.parse(text))
        #expect(source.text == "今天完成")
        #expect(source.view == "-pages/pg_3f9a1c0d5e7b2a41" && source.action == "done_today" && source.values == ["note": "轻松跑"])
        #expect(AssistantActionSource.stripping(text) == "今天完成")
        let empty = "今天完成\n" + #"<persona-action view="-pages/pg_3f9a1c0d5e7b2a41" action="done_today">{"values":{}}</persona-action>"#
        #expect(AssistantActionSource.stripping(empty) == "今天完成")
    }
    @Test func onlyTheLastCanonicalMarkerIsRemoved() throws {
        let prefix = "原文\n" + tag
        let source = try #require(AssistantActionSource.parse(prefix + "\n" + tag))
        #expect(source.text == prefix)
    }
}
