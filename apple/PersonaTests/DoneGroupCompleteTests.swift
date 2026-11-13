import XCTest
@testable import Persona

/// Done's groups after #5549: the list carries only the newest 200 done items, so a group is not complete just
/// because it is within the list's three days. A group is complete when a page reached its start or the loaded
/// rows reach the hub's count.
final class DoneGroupCompleteTests: XCTestCase {
    func testAGroupWithinThreeDaysIsNotCompleteWhenTheListCarriesOnlyPartOfIt() {
        // More than 200 done items today, only 50 loaded: not complete (on main the listDoneSpan clause said complete).
        let complete = InboxPage.doneGroupComplete("today", 50, groupComplete: [], counts: ["today": 250], filtered: false)
        XCTAssertFalse(complete, "a group with more than 200 done items is not complete from the list's rows alone")
    }

    func testAGroupIsCompleteWhenTheLoadedRowsReachTheCount() {
        let complete = InboxPage.doneGroupComplete("today", 200, groupComplete: [], counts: ["today": 200], filtered: false)
        XCTAssertTrue(complete, "as many loaded as the hub counts: complete")
    }

    func testAGroupIsCompleteWhenAPageReachedItsStart() {
        let complete = InboxPage.doneGroupComplete("today", 0, groupComplete: ["today"], counts: ["today": 250], filtered: false)
        XCTAssertTrue(complete, "a page of its own reached its start: complete")
    }

    func testFilteredGroupsAreNotCompleteFromTheCount() {
        let complete = InboxPage.doneGroupComplete("today", 250, groupComplete: [], counts: ["today": 250], filtered: true)
        XCTAssertFalse(complete, "filtered: the counts are of what is loaded, so a group is not complete from the count")
    }
}
