import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Completed Agent turn Changed Files card (#12016): shared `spec/fixtures/fold-changes` parity,
/// exchange completeness gating, fold/unfold/reload parity, >3 file expansion, and Review/FileViewer routing.
@MainActor
struct TurnChangesTests {
    // MARK: - Helpers

    private func item(
        _ seq: Int,
        _ kind: ItemKind,
        role: String = "assistant",
        text: String? = nil,
        tool: String? = nil,
        toolUseID: String? = nil,
        input: [String: JSONValue]? = nil,
        error: Bool? = nil,
        fold: Fold? = nil,
        compact: Compaction? = nil,
        image: ImagePayload? = nil,
        modelRefusal: ModelRefusal? = nil
    ) -> Item {
        var it = Item(
            id: "\(seq)#0",
            seq: seq,
            ts: Date(timeIntervalSince1970: TimeInterval(1_700_000_000 + seq)),
            role: role,
            kind: kind,
            text: text,
            toolUseID: toolUseID,
            toolName: tool,
            toolInput: input.map { .object($0) },
            isError: error,
            image: image,
            fold: fold,
            compact: compact
        )
        it.modelRefusal = modelRefusal
        return it
    }

    private func blocks(_ items: [Item]) -> [ChatBlock] {
        ChatStore.rebuild(items, settled: nil).blocks
    }

    private func changeLines(_ lines: [ChatLine]) -> [(id: String, changes: TurnChangeSet)] {
        lines.compactMap { line in
            if case .turnChanges(let id, let changes) = line.content { return (id, changes) }
            return nil
        }
    }

    // MARK: - Shared spec/fixtures/fold-changes/cases.json replay

    private struct FixtureFile: Decodable {
        let version: Int
        let cases: [FixtureCase]
    }

    private struct FixtureCase: Decodable {
        let name: String
        let description: String
        let input: FixtureInput
        let expected: FixtureExpected
    }

    private struct FixtureInput: Decodable {
        let items: [Item]
    }

    private struct FixtureExpected: Decodable {
        let changes: [FoldChange]
    }

    @Test func replaysAllSharedFoldChangesFixturesUnfoldedAndFolded() throws {
        let repoRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let fixtureURL = repoRoot.appending(path: "spec/fixtures/fold-changes/cases.json")
        let data = try Data(contentsOf: fixtureURL)
        let fixture = try JSON.decoder.decode(FixtureFile.self, from: data)
        #expect(fixture.cases.count >= 10)

        for tc in fixture.cases {
            let maxSeq = tc.input.items.map(\.seq).max() ?? 1
            let user = item(0, .text, role: "user", text: "Apply edits for \(tc.name)")
            let answer = item(maxSeq + 10, .text, role: "assistant", text: "Done.")
            let unfoldedItems = [user] + tc.input.items + [answer]

            let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: nil)
            let unfoldedLines = builder.build(blocks(unfoldedItems), live: nil)
            let unfoldedCards = changeLines(unfoldedLines)

            let fold = Fold(
                items: tc.input.items.count,
                tools: max(1, tc.input.items.count / 2),
                thoughts: 0,
                until: nil,
                throughSeq: maxSeq,
                throughID: tc.input.items.last?.id ?? "end#0",
                changes: tc.expected.changes
            )
            let foldItem = item(1, .fold, role: "assistant", fold: fold)
            let foldedItems = [user, foldItem, answer]
            let foldedLines = builder.build(blocks(foldedItems), live: nil)
            let foldedCards = changeLines(foldedLines)

            if tc.expected.changes.isEmpty {
                #expect(unfoldedCards.isEmpty, "fixture \(tc.name) unfolded should emit no card")
                #expect(foldedCards.isEmpty, "fixture \(tc.name) folded should emit no card")
            } else {
                #expect(unfoldedCards.count == 1, "fixture \(tc.name) unfolded should emit 1 card")
                #expect(foldedCards.count == 1, "fixture \(tc.name) folded should emit 1 card")
                let expectedFiles = tc.expected.changes.map {
                    TurnChangedFile(path: $0.path, shown: $0.path, add: $0.add, del: $0.del, exact: $0.exact)
                }
                let expectedSet = TurnChangeSet(
                    files: expectedFiles,
                    add: expectedFiles.reduce(0) { $0 + $1.add },
                    del: expectedFiles.reduce(0) { $0 + $1.del },
                    exact: expectedFiles.allSatisfy(\.exact)
                )
                #expect(unfoldedCards.first?.changes == expectedSet, "fixture \(tc.name) unfolded mismatch")
                #expect(foldedCards.first?.changes == expectedSet, "fixture \(tc.name) folded mismatch")
                #expect(unfoldedCards.first?.id == foldedCards.first?.id, "fixture \(tc.name) card id should stay stable across fold/unfold")
            }

            // Opening the work summary must preserve the exact same Changed Files card.
            let workIDs = Set(unfoldedLines.compactMap { line -> String? in
                if case .work(let id, _, _, _, _) = line.content { return id }
                return nil
            })
            let openBuilder = ChatLines(openWork: workIDs, includeTurnChanges: true, turnChangesCwd: nil)
            let openCards = changeLines(openBuilder.build(blocks(unfoldedItems), live: nil))
            #expect(openCards.map { $0.changes } == unfoldedCards.map { $0.changes }, "fixture \(tc.name) open work should preserve card")
        }
    }

    // MARK: - Exchange completeness & placement gating

    private func sampleEditExchange(cwd: String = "/workspace/app") -> [Item] {
        [
            item(1, .text, role: "user", text: "Update main.swift"),
            item(2, .toolUse, tool: "Edit", toolUseID: "c1", input: [
                "file_path": .string("\(cwd)/Sources/main.swift"),
                "old_string": .string("let x = 1\n"),
                "new_string": .string("let x = 2\nlet y = 3\n"),
            ]),
            item(3, .toolResult, role: "user", text: "ok", toolUseID: "c1"),
            item(4, .text, role: "assistant", text: "Updated `Sources/main.swift`."),
        ]
    }

    @Test func completedAgentExchangePlacesCardRightAfterFinalAnswerAndAfterSplitAnswerParts() {
        let items = sampleEditExchange()
        let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        let lines = builder.build(blocks(items), live: nil)

        guard let answerIndex = lines.firstIndex(where: { if case .row(.assistant) = $0.content { true } else { false } }),
              let cardIndex = lines.firstIndex(where: { if case .turnChanges = $0.content { true } else { false } }) else {
            Issue.record("expected answer and turnChanges lines")
            return
        }
        #expect(cardIndex == answerIndex + 1)

        // Long answer split into multiple AnswerParts keeps the Changed Files card right after the final part.
        let longBody = Array(repeating: "Paragraph about the file updates and verification details.", count: 120).joined(separator: "\n\n")
        var longItems = items
        longItems[3].text = longBody
        let split = ChatLines.splitAnswers(builder.build(blocks(longItems), live: nil))
        let lastPartIndex = split.lastIndex { if case .answerPart = $0.content { true } else { false } }
        let splitCardIndex = split.firstIndex { if case .turnChanges = $0.content { true } else { false } }
        #expect(lastPartIndex != nil && splitCardIndex == lastPartIndex! + 1)
    }

    @Test func completedExchangeWithIdleCompactionPlacesCardBeforeCompactionNotice() {
        var items = sampleEditExchange()
        items.append(item(5, .notice, role: "system", text: "Compacted", compact: Compaction(trigger: "auto", preTokens: 12_000)))
        let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        let lines = builder.build(blocks(items), live: nil)

        let answerIndex = lines.firstIndex { if case .row(.assistant) = $0.content { true } else { false } }
        let cardIndex = lines.firstIndex { if case .turnChanges = $0.content { true } else { false } }
        let compactIndex = lines.firstIndex { if case .row(.notice(let n)) = $0.content { n.compact != nil } else { false } }
        #expect(answerIndex != nil && cardIndex == answerIndex! + 1 && compactIndex == cardIndex! + 1)
    }

    @Test func excludesAssistantRunningClippedMidTurnInterruptedAndAPIErrorExchanges() {
        let items = sampleEditExchange()
        let builtBlocks = blocks(items)

        // Assistant conversation (includeTurnChanges == false)
        let assistantBuilder = ChatLines(openWork: [], includeTurnChanges: false, turnChangesCwd: "/workspace/app", foldSupervision: true)
        #expect(changeLines(assistantBuilder.build(builtBlocks, live: nil)).isEmpty)

        // Running / active exchange
        let turnID: String? = builtBlocks.compactMap { if case .turn(let t) = $0 { return t.id } else { return nil } }.first
        let agentBuilder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        #expect(turnID != nil)
        #expect(changeLines(agentBuilder.build(builtBlocks, live: turnID)).isEmpty)

        // Clipped mid-exchange page (no user message start)
        let clippedItems = Array(items.dropFirst())
        #expect(changeLines(agentBuilder.build(blocks(clippedItems), live: nil)).isEmpty)

        // Clipped mid-exchange page followed by a queued user photo later in the segment still has no user start.
        var queuedPhoto = item(3, .image, role: "user", image: ImagePayload(mime: "image/png", data: "aGVsbG8="))
        queuedPhoto.id = "3#1"
        queuedPhoto.queued = true
        let clippedWithQueuedPhoto = [items[1], items[2], queuedPhoto, items[3]]
        #expect(changeLines(agentBuilder.build(blocks(clippedWithQueuedPhoto), live: nil)).isEmpty)

        // Tool work after narration with no final answer
        var unfinished = items
        unfinished.append(item(5, .toolUse, tool: "Bash", toolUseID: "c2", input: ["command": .string("swift test")]))
        unfinished.append(item(6, .toolResult, role: "user", text: "ok", toolUseID: "c2"))
        #expect(changeLines(agentBuilder.build(blocks(unfinished), live: nil)).isEmpty)

        // Interrupted turn (trailing notice)
        var interrupted = items
        interrupted.append(item(5, .notice, role: "system", text: "Interrupted", modelRefusal: ModelRefusal(subtype: "model_refusal", category: "safety", text: "refused")))
        #expect(changeLines(agentBuilder.build(blocks(interrupted), live: nil)).isEmpty)

        // Final answer is a model API error
        var apiError = items
        apiError[3].text = "API Error: 502 Bad Gateway"
        #expect(changeLines(agentBuilder.build(blocks(apiError), live: nil)).isEmpty)
    }

    @Test func photoOnlyUserExchangeStillCountsAsComplete() {
        let photo = item(1, .image, role: "user", image: ImagePayload(mime: "image/png", data: "aGVsbG8="))
        let items: [Item] = [
            photo,
            item(2, .toolUse, tool: "Write", toolUseID: "c1", input: [
                "file_path": .string("/workspace/app/Notes.md"),
                "content": .string("Hello\nWorld\n"),
            ]),
            item(3, .toolResult, role: "user", text: "ok", toolUseID: "c1"),
            item(4, .text, role: "assistant", text: "Created Notes.md."),
        ]
        let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        let cards = changeLines(builder.build(blocks(items), live: nil))
        #expect(cards.count == 1)
        #expect(cards[0].changes.files.map(\.shown) == ["Notes.md"])
        #expect(cards[0].changes.add == 2 && cards[0].changes.del == 0 && cards[0].changes.exact)
    }

    // MARK: - Older folds, zero-edit folds, and JSON / ChatCache v24 round-trip

    @Test func olderFoldWithoutChangesSuppressesCardEvenWithOtherEditsUntilUnfolded() throws {
        let olderFoldJSON = #"{"id":"fold:t1#0","seq":2,"role":"assistant","kind":"fold","fold":{"items":4,"tools":2,"thoughts":0,"through_seq":5,"through_id":"5#0"}}"#
        let olderFold = try JSON.decoder.decode(Item.self, from: Data(olderFoldJSON.utf8))
        #expect(olderFold.fold?.changes == nil)

        let itemsWithOlderFold: [Item] = [
            item(1, .text, role: "user", text: "Refactor"),
            olderFold,
            item(6, .toolUse, tool: "Edit", toolUseID: "c6", input: [
                "file_path": .string("/workspace/app/b.swift"),
                "old_string": .string("old\n"),
                "new_string": .string("new\n"),
            ]),
            item(7, .toolResult, role: "user", text: "ok", toolUseID: "c6"),
            item(8, .text, role: "assistant", text: "Done."),
        ]
        let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        #expect(changeLines(builder.build(blocks(itemsWithOlderFold), live: nil)).isEmpty)

        // Zero-edit fold (`"changes": []`) allows later edits in the same exchange to be counted.
        let emptyFoldJSON = #"{"id":"fold:t1#0","seq":2,"role":"assistant","kind":"fold","fold":{"items":4,"tools":2,"thoughts":0,"through_seq":5,"through_id":"5#0","changes":[]}}"#
        let emptyFold = try JSON.decoder.decode(Item.self, from: Data(emptyFoldJSON.utf8))
        #expect(emptyFold.fold?.changes == [])

        var itemsWithEmptyFold = itemsWithOlderFold
        itemsWithEmptyFold[1] = emptyFold
        let cards = changeLines(builder.build(blocks(itemsWithEmptyFold), live: nil))
        #expect(cards.count == 1)
        #expect(cards[0].changes.files.map(\.shown) == ["b.swift"])
        #expect(cards[0].changes.add == 1 && cards[0].changes.del == 1)

        // Round-trip preserves nil vs [] vs populated changes, and ChatCache is bumped to v24.
        let populatedJSON = #"{"id":"fold:t2#0","seq":2,"role":"assistant","kind":"fold","fold":{"items":2,"tools":1,"thoughts":0,"through_seq":3,"through_id":"3#0","changes":[{"path":"/workspace/app/a.swift","add":4,"del":2,"exact":false},{"path":"/workspace/app/c.swift","add":1,"del":0}]}}"#
        let populated = try JSON.decoder.decode(Item.self, from: Data(populatedJSON.utf8))
        #expect(populated.fold?.changes == [
            FoldChange(path: "/workspace/app/a.swift", add: 4, del: 2, exact: false),
            FoldChange(path: "/workspace/app/c.swift", add: 1, del: 0, exact: true),
        ])
        let reencodedOlder = try JSON.decoder.decode(Item.self, from: JSON.encoder.encode(olderFold))
        let reencodedEmpty = try JSON.decoder.decode(Item.self, from: JSON.encoder.encode(emptyFold))
        let reencodedPopulated = try JSON.decoder.decode(Item.self, from: JSON.encoder.encode(populated))
        #expect(reencodedOlder.fold?.changes == nil)
        #expect(reencodedEmpty.fold?.changes == [])
        #expect(reencodedPopulated.fold?.changes == populated.fold?.changes)
        #expect(ChatCache.folder == "chats-v25")
    }

    // MARK: - Clipped counts, CRLF, >3 file expansion, and late-arriving cwd

    @Test func clippedEditsOmitTotalsAndCRLFCountsAccurately() {
        let exactClip = "line1\n…（已截断，共 4096 字节）" // i18n-ignore: fixture for hub clip note
        let approxClip = "line1\n…（已截断，共 ≈8192 字节）" // i18n-ignore: fixture for hub clip note
        let crlfClip = "line1\r\n…（已截断，共 4096 字节）" // i18n-ignore: fixture for hub clip note
        #expect(TurnChanges.hasClippedSuffix(exactClip))
        #expect(TurnChanges.hasClippedSuffix(approxClip))
        #expect(TurnChanges.hasClippedSuffix(crlfClip))
        #expect(!TurnChanges.hasClippedSuffix("line1\n…（已截断，共 4096 字节）more")) // i18n-ignore: fixture for hub clip note
        #expect(!TurnChanges.hasClippedSuffix("line1\n…（已截断，共  字节）")) // i18n-ignore: fixture for hub clip note

        // Structural zz_persona_clipped nested key and CRLF-cut input both mark their files uncountable
        // while leaving other files in the same exchange exact.
        let items: [Item] = [
            item(1, .text, role: "user", text: "Big edit"),
            item(2, .toolUse, tool: "Write", toolUseID: "c1", input: [
                "file_path": .string("/workspace/app/big.txt"),
                "content": .string("hello\n"),
                "zz_persona_clipped": .string(approxClip),
            ]),
            item(3, .toolResult, role: "user", text: "ok", toolUseID: "c1"),
            item(4, .toolUse, tool: "Write", toolUseID: "c2", input: [
                "file_path": .string("/workspace/app/crlf-clipped.txt"),
                "content": .string(crlfClip),
            ]),
            item(5, .toolResult, role: "user", text: "ok", toolUseID: "c2"),
            item(6, .toolUse, tool: "Edit", toolUseID: "c3", input: [
                "file_path": .string("/workspace/app/crlf.txt"),
                "old_string": .string("a\r\nb\r\n"),
                "new_string": .string("a\r\nb\r\nc\r\n"),
            ]),
            item(7, .toolResult, role: "user", text: "ok", toolUseID: "c3"),
            item(8, .text, role: "assistant", text: "Done."),
        ]
        let builder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        let cards = changeLines(builder.build(blocks(items), live: nil))
        #expect(cards.count == 1)
        let set = cards[0].changes
        #expect(!set.exact)
        #expect(set.files.count == 3)
        #expect(set.files[0].shown == "big.txt" && !set.files[0].exact)
        #expect(set.files[1].shown == "crlf-clipped.txt" && !set.files[1].exact)
        #expect(set.files[2].shown == "crlf.txt" && set.files[2].exact && set.files[2].add == 1 && set.files[2].del == 0)

        // Past Myers' diffLineLimit (60,000 lines), lineCounts falls back to counting all old removed and all new added.
        let hugeOld = Array(repeating: "old", count: 30_001).joined(separator: "\n") + "\n"
        let hugeNew = Array(repeating: "new", count: 30_000).joined(separator: "\n") + "\n"
        let overflow = TurnChanges.lineCounts(hugeOld, hugeNew)
        #expect(overflow.add == 30_000 && overflow.del == 30_001)
    }

    @Test func collapseExpandFiveFilesAndLateArrivingCwd() {
        // Matches the 5-file + failed-edit + relative/outside path scenario.
        let items: [Item] = [
            item(1, .text, role: "user", text: "Update five files"),
            item(2, .toolUse, tool: "Edit", toolUseID: "e1", input: [
                "file_path": .string("./src/a.swift"),
                "old_string": .string("a\n"),
                "new_string": .string("a1\na2\n"),
            ]),
            item(3, .toolResult, role: "user", text: "ok", toolUseID: "e1"),
            item(4, .toolUse, tool: "Edit", toolUseID: "e2", input: [
                "file_path": .string("/workspace/app/src/../src/a.swift"),
                "old_string": .string("b\n"),
                "new_string": .string("b1\n"),
            ]),
            item(5, .toolResult, role: "user", text: "ok", toolUseID: "e2"),
            item(6, .toolUse, tool: "Edit", toolUseID: "e3", input: [
                "file_path": .string("/workspace/app/src/failed.swift"),
                "old_string": .string("x\n"),
                "new_string": .string("y\n"),
            ]),
            item(7, .toolResult, role: "user", text: "boom", toolUseID: "e3", error: true),
            item(8, .toolUse, tool: "Write", toolUseID: "e4", input: ["file_path": .string("/workspace/app/src/b.swift"), "content": .string("1\n2\n")]),
            item(9, .toolResult, role: "user", text: "ok", toolUseID: "e4"),
            item(10, .toolUse, tool: "Write", toolUseID: "e5", input: ["file_path": .string("/workspace/app/src/c.swift"), "content": .string("1\n")]),
            item(11, .toolResult, role: "user", text: "ok", toolUseID: "e5"),
            item(12, .toolUse, tool: "Write", toolUseID: "e6", input: ["file_path": .string("/workspace/app-sibling/d.swift"), "content": .string("1\n")]),
            item(13, .toolResult, role: "user", text: "ok", toolUseID: "e6"),
            item(14, .toolUse, tool: "Write", toolUseID: "e7", input: ["file_path": .string("/tmp/outside.md"), "content": .string("note\n")]),
            item(15, .toolResult, role: "user", text: "ok", toolUseID: "e7"),
            item(16, .text, role: "assistant", text: "All five updated."),
        ]

        // Before session cwd arrives: full / normalized paths shown.
        let noCwdBuilder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: nil)
        let beforeCards = changeLines(noCwdBuilder.build(blocks(items), live: nil))
        #expect(beforeCards.count == 1)
        #expect(beforeCards[0].changes.files.map(\.shown).contains("src/a.swift"))

        // Once session cwd arrives: relative ./src/a.swift and /workspace/app/src/../src/a.swift merge into one file,
        // workspace files show relative paths, and sibling-prefix / outside paths stay full.
        let withCwdBuilder = ChatLines(openWork: [], includeTurnChanges: true, turnChangesCwd: "/workspace/app")
        let afterCards = changeLines(withCwdBuilder.build(blocks(items), live: nil))
        #expect(afterCards.count == 1)
        let set = afterCards[0].changes
        #expect(set.files.count == 5)
        #expect(set.files.map(\.shown) == [
            "src/a.swift",
            "src/b.swift",
            "src/c.swift",
            "/workspace/app-sibling/d.swift",
            "/tmp/outside.md",
        ])
        #expect(set.files[0].add == 3 && set.files[0].del == 2) // merged e1 (+2/-1) + e2 (+1/-1)
        #expect(TurnChanges.visibleFiles(in: set, expanded: false).count == 3)
        #expect(TurnChanges.visibleFiles(in: set, expanded: true).count == 5)
    }

    // MARK: - Branch review routing & ReviewPage target-file fallback

    @Test func routingOpensBranchReviewOrFallsBackToNativeFileViewer() {
        // Header Review button
        #expect(TurnReviewRouting.route(targetFile: nil, canReview: true, cwd: "/workspace/app") == .review(targetFile: nil))
        #expect(TurnReviewRouting.route(targetFile: nil, canReview: false, cwd: "/workspace/app") == .none)
        #expect(TurnReviewRouting.route(targetFile: nil, canReview: true, cwd: "") == .none)
        #expect(TurnReviewRouting.route(targetFile: nil, canReview: true, cwd: nil) == .none)

        // File inside workspace
        #expect(
            TurnReviewRouting.route(targetFile: "/workspace/app/Sources/a.swift", canReview: true, cwd: "/workspace/app")
                == .review(targetFile: "/workspace/app/Sources/a.swift")
        )
        // Trailing slash on cwd still matches
        #expect(
            TurnReviewRouting.route(targetFile: "/workspace/app/Sources/a.swift", canReview: true, cwd: "/workspace/app/")
                == .review(targetFile: "/workspace/app/Sources/a.swift")
        )
        // Hub lacks git_review -> native file viewer
        #expect(
            TurnReviewRouting.route(targetFile: "/workspace/app/Sources/a.swift", canReview: false, cwd: "/workspace/app")
                == .file("/workspace/app/Sources/a.swift")
        )
        // Empty cwd -> native file viewer
        #expect(
            TurnReviewRouting.route(targetFile: "/workspace/app/Sources/a.swift", canReview: true, cwd: "")
                == .file("/workspace/app/Sources/a.swift")
        )
        // Outside workspace & sibling prefix -> native file viewer
        #expect(
            TurnReviewRouting.route(targetFile: "/workspace/app-extra/Sources/a.swift", canReview: true, cwd: "/workspace/app")
                == .file("/workspace/app-extra/Sources/a.swift")
        )
        #expect(
            TurnReviewRouting.route(targetFile: "/tmp/scratch.md", canReview: true, cwd: "/workspace/app")
                == .file("/tmp/scratch.md")
        )
    }

    @Test func reviewTargetResolutionSelectsRepoRelativeFileOrFallsBackOnMissingOrFailure() {
        let target = "/repo/packages/ios/Sources/App.swift"
        let subdirCwd = "/repo/packages/ios"

        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .loading)
                == .pending
        )

        // Manual switch to another scope while loading cancels automatic target resolution / fallback.
        let matchingReview = GitReview(
            root: "/repo",
            scope: "branch",
            base: nil,
            head: nil,
            files: [GitReview.File(path: "packages/ios/Sources/App.swift", status: "modified", added: 3, deleted: 1)],
            truncated: nil
        )
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .uncommitted, phase: .loaded(matchingReview))
                == .ignore
        )
        let wrongScopePayload = GitReview(
            root: "/repo",
            scope: "uncommitted",
            base: nil,
            head: nil,
            files: [],
            truncated: nil
        )
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .loaded(wrongScopePayload))
                == .ignore
        )

        // Loaded review where repo root is parent of cwd: resolves against review.root and matches file.
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .loaded(matchingReview))
                == .select("packages/ios/Sources/App.swift")
        )

        // Loaded review without root falls back to cwd.
        let cwdRootReview = GitReview(
            root: nil,
            scope: "branch",
            base: nil,
            head: nil,
            files: [GitReview.File(path: "Sources/App.swift", status: "modified", added: 1, deleted: 0)],
            truncated: nil
        )
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .loaded(cwdRootReview))
                == .select("Sources/App.swift")
        )

        // File absent from review (e.g. clean branch or reverted file) -> fallback to native file viewer.
        let otherFileReview = GitReview(
            root: "/repo",
            scope: "branch",
            base: nil,
            head: nil,
            files: [GitReview.File(path: "README.md", status: "modified", added: 1, deleted: 0)],
            truncated: nil
        )
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .loaded(otherFileReview))
                == .fallback(target)
        )

        // Review load failed (not a git repo, no default branch, etc.) -> fallback to native file viewer.
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .failed(code: "not_git", message: "not a git repo"))
                == .fallback(target)
        )
        #expect(
            ReviewLogic.resolveTargetFile(target, cwd: subdirCwd, requestedScope: .branch, currentScope: .branch, phase: .failed(code: "no_default_branch", message: "no default branch"))
                == .fallback(target)
        )
    }
}
