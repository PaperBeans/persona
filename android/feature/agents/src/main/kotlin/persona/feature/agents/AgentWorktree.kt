package persona.feature.agents

import persona.core.contracts.api.WorktreeStatus

/**
 * A new agent's own worktree (#4532, iOS `AgentWorktree`, web `lib/worktree.ts`): New Agent can start the agent in a Git
 * worktree of its own (`worktree/add`, a branch persona/<its name>). The same rules as the node names it.
 */
internal object AgentWorktree {
    /** The node's `agentBranchPrefix`. */
    const val PREFIX = "persona/"
    /** The node's `maxSlug`. */
    const val MAX_SLUG = 40

    /** Lower case, each run of anything but ASCII letters and digits one "-", none at either end (the node's `herdr.WorktreeSlug`). */
    fun slug(text: String): String {
        val out = StringBuilder()
        var dash = false
        for (ch in text.lowercase()) {
            if (ch in 'a'..'z' || ch in '0'..'9') {
                if (dash && out.isNotEmpty()) out.append('-')
                dash = false
                out.append(ch)
            } else dash = true
        }
        return out.toString()
    }

    /** The branch the node gives a new agent's worktree (`agentBranch`): persona/<slug, 40 at most>, "agent" with no slug. */
    fun branch(nameHint: String): String {
        var s = slug(nameHint)
        if (s.length > MAX_SLUG) s = s.take(MAX_SLUG).trimEnd('-')
        return PREFIX + s.ifEmpty { "agent" }
    }

    /** What names the branch: the first message, or the folder's name when the message has nothing to name a branch with. */
    fun nameHint(prompt: String, cwd: String): String {
        val p = prompt.trim()
        if (slug(p).isNotEmpty()) return p
        return cwd.trim().trimEnd('/').substringAfterLast('/')
    }

    /** Where the checkout goes: `<checkouts>/<the branch as a slug>`; "" when the computer did not say. */
    fun checkout(repo: WorktreeStatus, branch: String): String {
        val s = slug(branch)
        if (repo.checkouts.isEmpty() || s.isEmpty()) return ""
        val windows = '\\' in repo.checkouts && '/' !in repo.checkouts
        return repo.checkouts.trimEnd('/', '\\') + (if (windows) "\\" else "/") + s
    }
}
