package persona.core.domain.sort

import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.Machine
import persona.core.contracts.Session

/**
 * A working tree's line in By Repository (#4338 A7), written from iOS's `RepoWorktreeLine` and `ChatsView.treeDetail`:
 * Primary on the main checkout, the branch only when the name is not it, Detached HEAD in git without a branch, Idle or
 * "N agents", dimmed while the computer is offline.
 */
class RepoTreeLinesTest {
    private val repo = "/home/me/code/app"

    private fun at(id: String, cwd: String, worktree: String? = cwd, branch: String? = null, slotName: String? = null,
                   state: String = "working", machine: String = "laptop") =
        AgentSession(Session(machineId = machine, sessionId = id, title = id, cwd = cwd, state = state, repoRoot = repo,
            worktree = worktree, branch = branch, slotName = slotName, updatedAt = "2026-01-01T00:00:00Z"))

    private fun tree(vararg rows: AgentSession): AgentWorktree = agentRepos(rows.toList()).single().worktrees.single()

    @Test fun theMainCheckoutNamedByItsBranchSaysPrimaryAndNoBranch() {
        val line = worktreeLine(tree(at("a", repo, branch = "main")), quiet = false, agents = 1, online = true)
        assertEquals(WorktreeLine(main = true, tally = WorktreeTally.Agents(1)), line)
    }

    @Test fun aTreeItsHostNamedShowsItsBranchAfterTheName() {
        // Paseo names its workspace in slot_name (#4497): the name is not the branch, so iOS writes the branch beside it.
        val named = tree(at("a", "/home/me/.paseo/worktrees/x/brave-fox", branch = "perf/importer", slotName = "Speed up the importer"))
        assertEquals("Speed up the importer", named.name)
        val line = worktreeLine(named, quiet = false, agents = 2, online = true)
        assertEquals("perf/importer", line.branch)
        assertFalse(line.main)
        assertFalse(line.detached)
    }

    @Test fun aTreeNamedByItsBranchSaysNothingMore() {
        val line = worktreeLine(tree(at("a", "$repo/.worktrees/fix", branch = "fix/sync")), quiet = false, agents = 1, online = true)
        assertNull(line.branch)
        assertFalse(line.detached)
    }

    @Test fun inGitWithoutABranchHeadIsDetached() {
        val bisect = tree(at("a", "$repo/.worktrees/bisect"))
        assertEquals("bisect", bisect.name)
        assertTrue(bisect.detached)
        val line = worktreeLine(bisect, quiet = false, agents = 1, online = true)
        assertTrue(line.detached)
        assertNull(line.branch)
    }

    @Test fun aPlaceTheNodeDidNotReadIsNotDetached() {
        // An older node sends neither worktree nor branch: the tree comes from the path, and nothing says its HEAD.
        val inferred = agentRepos(listOf(AgentSession(Session(machineId = "laptop", sessionId = "a", cwd = "$repo/.worktrees/old",
            state = "working")))).single().worktrees.single()
        assertFalse(inferred.detached)
        assertFalse(worktreeLine(inferred, quiet = false, agents = 1, online = true).detached)
    }

    @Test fun theTallyIsIdleWhileNothingGoesOnElseHowManyAgents() {
        val t = tree(at("a", "$repo/.worktrees/fix", branch = "fix"))
        assertEquals(WorktreeTally.Idle, worktreeLine(t, quiet = true, agents = 3, online = true).tally)
        assertEquals(WorktreeTally.Agents(3), worktreeLine(t, quiet = false, agents = 3, online = true).tally)
        // None of its own while something goes on under it: iOS leaves the count out.
        assertNull(worktreeLine(t, quiet = false, agents = 0, online = true).tally)
    }

    @Test fun anOfflineComputerDimsTheLine() {
        val t = tree(at("a", "$repo/.worktrees/fix", branch = "fix"))
        assertTrue(worktreeLine(t, quiet = false, agents = 1, online = false).offline)
        assertFalse(worktreeLine(t, quiet = false, agents = 1, online = true).offline)
    }

    @Test fun theListGivesEachWorkingTreeItsLineAndTheRepositoryNone() {
        val rows = listOf(
            at("main", repo, branch = "main"),
            at("fix", "$repo/.worktrees/fix", branch = "fix/sync", state = "needs_you"),
            at("bisect", "$repo/.worktrees/bisect", state = "idle"),
            at("hotfix", "/srv/api/.worktrees/hotfix", branch = "hotfix", machine = "box").let {
                AgentSession(it.session.copy(repoRoot = "/srv/api"))
            },
            at("ghost", "/srv/ghost/.worktrees/x", branch = "x", machine = "unknown").let {
                AgentSession(it.session.copy(repoRoot = "/srv/ghost"))
            },
        )
        val list = buildAgentList(rows, listOf(Machine("laptop", "Laptop", online = true), Machine("box", "Box", online = false)),
            settings = AgentListSettings(grouping = AgentGrouping.Repo))
        val byId = list.groups.associateBy { it.id }
        assertNull(byId["repo/laptop/$repo"]!!.worktree)
        assertEquals(WorktreeLine(main = true, tally = WorktreeTally.Agents(1)), byId["tree/laptop/$repo"]!!.worktree)
        assertEquals(WorktreeTally.Agents(1), byId["tree/laptop/$repo/.worktrees/fix"]!!.worktree!!.tally)
        val bisect = byId["tree/laptop/$repo/.worktrees/bisect"]!!.worktree!!
        assertTrue(bisect.detached)
        assertEquals(WorktreeTally.Idle, bisect.tally)
        assertTrue("its computer is offline", byId["tree/box//srv/api/.worktrees/hotfix"]!!.worktree!!.offline)
        assertFalse("a computer the hub has not listed yet is not called offline", byId["tree/unknown//srv/ghost/.worktrees/x"]!!.worktree!!.offline)
    }
}
