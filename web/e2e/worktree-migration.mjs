// Full-app checks against mock_worktree_migration.py; all runtime writes are synthetic.
// node web/e2e/worktree-migration.mjs <loopback web origin> <loopback mock origin> <artifacts> [Chrome executable]
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [web, hub, output, executable] = process.argv.slice(2);
const local = (value) => /^http:\/\/(?:127\.0\.0\.1|localhost):\d+\/?$/.test(value ?? "");
assert(local(web) && local(hub) && output, "Provide dedicated loopback web/mock origins and an artifact directory");
const control = async (mode) => {
  const res = await fetch(`${hub}/mock/worktree-migration${mode ? `?mode=${mode}` : ""}`);
  assert(res.ok, "The dedicated migration fixture must be running");
  const body = await res.json();
  assert.equal(body.fixture, "worktree-migration", "Never run mutations against a real hub");
  return body;
};
await control("normal");
mkdirSync(output, { recursive: true });
const results = [];
const watchdog = setTimeout(() => { console.error("TIMEOUT worktree migration acceptance (240s)"); process.exit(124); }, 240_000);
const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });

try {
  for (const width of [1440, 390]) {
    for (const theme of ["light", "dark"]) {
      const tag = `${width}-${theme}`;
      const reset = await fetch(`${hub}/mock/worktree-migration?reset=true`);
      assert.equal((await reset.json()).fixture, "worktree-migration");
      const context = await browser.newContext({ viewport: { width, height: 900 }, hasTouch: width === 390,
                                                colorScheme: theme, reducedMotion: "reduce", locale: "en-US" });
      const failures = [];
      await context.addInitScript(({ theme }) => {
        localStorage.setItem("persona.token", "synthetic-worktree-token");
        localStorage.setItem("persona.language", "en");
        localStorage.setItem("persona.theme", theme);
        localStorage.setItem("persona.agentGrouping", "repo");
      }, { theme });
      await context.route("**/*", async (route) => {
        const url = new URL(route.request().url());
        if (url.origin === new URL(web).origin || url.origin === new URL(hub).origin || url.protocol === "data:") return route.continue();
        return route.abort();
      });
      const page = await context.newPage();
      page.setDefaultTimeout(15000);
      // Exercise the phone menu-to-sheet handoff even when its first composer chunk is still on the wire.
      if (width === 390) await page.route("**/NewWorktree-*.js", async (route) => {
        await new Promise((done) => setTimeout(done, 100));
        await route.continue();
      });
      page.on("pageerror", (error) => failures.push(String(error)));
      let launches = 0;
      page.on("request", (request) => { if (request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/sessions") launches++; });
      const screenshot = async (name) => {
        // Dismiss completed-operation toasts before inspecting a subsequent dialog's footer.
        const notification = page.locator('[aria-live="polite"] > div');
        while (await notification.count()) {
          await notification.first().getByRole("button", { name: "Close", exact: true }).click();
          await notification.first().waitFor({ state: "detached" });
        }
        await page.waitForFunction(() => {
          const dialog = document.querySelector('[role="dialog"]');
          return !dialog || dialog.getAnimations({ subtree: true }).every((animation) =>
            animation.effect?.getTiming().iterations === Infinity || animation.playState !== "running");
        });
        assert.deepEqual(failures, [], "No client exceptions");
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "No page overflow");
        await page.screenshot({ path: join(output, `${tag}-${name}.png`) });
      };
      await page.goto(`${web}/agents`);
      const tree = page.getByRole("main");
      const empty = tree.getByRole("button", { name: "Empty parent", exact: true });
      await empty.waitFor();
      await tree.getByRole("button", { name: "Catalog leaf", exact: true }).waitFor();
      await empty.click();
      assert.equal(await empty.getAttribute("aria-pressed"), "true", "An empty workspace is independently selectable");
      await screenshot("catalog");
      const expand = tree.getByRole("button", { name: "Expand or collapse Empty parent", exact: true });
      assert.equal(await expand.getAttribute("aria-expanded"), "false");
      await expand.click();
      const leaf = tree.getByRole("button", { name: "Catalog leaf", exact: true });
      await leaf.click();
      assert.equal(await leaf.getAttribute("aria-expanded"), "false", "A populated child title folds without navigating");
      assert.equal(new URL(page.url()).pathname, "/agents");
      await leaf.press("Enter");
      assert.equal(await leaf.getAttribute("aria-expanded"), "true", "Enter opens the same disclosure");
      await leaf.press("Space");
      assert.equal(await leaf.getAttribute("aria-expanded"), "false", "Space folds the same disclosure");
      await leaf.click();
      assert.equal(await leaf.getAttribute("aria-expanded"), "true");
      await tree.getByRole("link", { name: /Catalog-only ancestry check/ }).click();
      await page.getByRole("heading", { name: "Catalog-only ancestry check", exact: true }).waitFor();
      results.push({ tag, check: "catalog empty ancestor / direct Agent navigation", pass: true });

      if (process.env.WORKTREE_ACCEPTANCE_PHASE === "catalog") { await context.close(); continue; }
      await page.goto(`${web}/agents`);
      await tree.getByRole("button", { name: "Actions for Empty parent", exact: true }).click();
      await page.getByRole("menuitem", { name: /new agent/i }).click();
      const dialog = page.getByRole("dialog");
      await dialog.getByTestId("new-agent-workspace").waitFor();
      assert(await dialog.getByText("Empty parent", { exact: true }).isVisible());
      assert.equal(await dialog.getByTestId("new-agent-worktree").count(), 0, "Existing workspaces never create a second checkout");
      const start = dialog.getByRole("button", { name: "Start", exact: true });
      await start.waitFor();
      await start.scrollIntoViewIfNeeded();
      const bounds = await start.boundingBox();
      assert(bounds && bounds.y >= 0 && bounds.y + bounds.height <= 900, "Start remains in the viewport");
      await screenshot("exact-destination");
      const creation = page.waitForRequest((request) => request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/sessions");
      await start.click();
      const request = await creation;
      const body = request.postDataJSON();
      assert.equal(body.machine_id, "macbook");
      assert.equal(body.host, "orca");
      assert.equal(body.workspace_id, "app::/Users/me/code/app/.worktrees/empty-parent");
      assert.equal(body.cwd, "/Users/me/code/app/.worktrees/empty-parent");
      assert(!("codex_permissions" in body), "Default creation does not introduce an explicit permission override");
      results.push({ tag, check: "existing workspace exact host / identity / path launch", pass: true });
      await page.goto(`${web}/agents`);
      const openMenu = async (name) => {
        await tree.getByRole("button", { name: `Actions for ${name}`, exact: true }).click();
      };
      await openMenu("Empty parent");
      await page.getByRole("menuitem", { name: /new child worktree/i }).click();
      const composer = page.getByRole("dialog");
      await composer.getByLabel("Project", { exact: true }).selectOption({ label: "Example library" });
      await composer.getByLabel("Name (optional)", { exact: true }).fill(`accept-${tag}`);
      await composer.locator("summary").filter({ hasText: /^Advanced$/ }).click();
      assert.equal(await composer.getByLabel("Parent worktree", { exact: true }).inputValue(),
        "parent:app::/Users/me/code/app/.worktrees/empty-parent", "Changing Git source retains visual parent");
      await composer.getByLabel("Base branch", { exact: true }).selectOption("review/existing");
      await composer.getByLabel("Branch name (optional)", { exact: true }).fill(`acceptance/${tag}`);
      await composer.getByLabel("Note", { exact: true }).fill("Synthetic acceptance note");
      await composer.getByLabel("Sparse checkout", { exact: true }).selectOption("web-only");
      const create = composer.getByRole("button", { name: "Create worktree", exact: true });
      await create.scrollIntoViewIfNeeded();
      const createBounds = await create.boundingBox();
      assert(createBounds && createBounds.y >= 0 && createBounds.y + createBounds.height <= 900, "Create remains in the viewport");
      await screenshot("composer");
      const createRequest = page.waitForRequest((request) => request.method() === "POST" && new URL(request.url()).pathname.endsWith("/worktree/create"));
      await create.click();
      const bodyCreated = (await createRequest).postDataJSON();
      assert.equal(bodyCreated.workspace_id, "library::/Users/me/code/library");
      assert.equal(bodyCreated.parent_workspace_id, "app::/Users/me/code/app/.worktrees/empty-parent");
      assert.equal(bodyCreated.base_ref, "review/existing");
      assert.equal(bodyCreated.branch_override, `acceptance/${tag}`);
      assert.equal(bodyCreated.sparse_preset_id, "web-only");
      assert.equal(bodyCreated.note, "Synthetic acceptance note");
      const newName = `accept-${tag}`;
      const newPath = `/Users/me/code/library/.worktrees/${newName}`;
      await composer.getByText(newPath, { exact: true }).waitFor();
      await composer.getByRole("button", { name: "Start agent in this worktree", exact: true }).waitFor();
      await screenshot("created");
      await composer.getByRole("button", { name: "Done", exact: true }).click();
      await composer.waitFor({ state: "detached" });
      const creationEvidence = await control();
      assert.equal(creationEvidence.created, 1);
      assert.equal(launches, 1, "Worktree creation starts no Agent by default");
      results.push({ tag, check: "independent project/source/parent/base + branch/note/sparse creation", pass: true });

      // Rename only the workspace; the separately launched Agent's identity and title stay intact.
      await openMenu(newName);
      await page.getByRole("menuitem", { name: /rename worktree/i }).click();
      const rename = page.getByRole("dialog");
      const renamed = `Renamed ${tag}`;
      await rename.getByRole("textbox").fill(renamed);
      await screenshot("rename");
      await rename.getByRole("button", { name: "Save", exact: true }).click();
      await rename.waitFor({ state: "detached" });
      await tree.getByRole("button", { name: new RegExp(`^${renamed}`) }).waitFor();
      results.push({ tag, check: "acknowledged workspace rename and catalog refresh", pass: true });

      await control("dirty");
      await openMenu(renamed);
      await page.getByRole("menuitem", { name: /delete worktree/i }).click();
      const deletion = page.getByRole("dialog");
      const remove = deletion.getByRole("button", { name: /^(Delete|Remove) worktree$/, exact: false });
      await deletion.getByRole("checkbox", { name: /changes|discard|lost/i }).waitFor();
      assert(await remove.isDisabled(), "Dirty deletion requires explicit acknowledgement");
      await deletion.getByRole("checkbox", { name: /changes|discard|lost/i }).check();
      await screenshot("delete-dirty");
      const removeRequest = page.waitForRequest((request) => {
        if (request.method() !== "POST" || !new URL(request.url()).pathname.endsWith("/worktree/delete")) return false;
        const body = request.postDataJSON(); return body.preview_token && !body.reconcile;
      });
      await remove.click();
      const removedBody = (await removeRequest).postDataJSON();
      assert.equal(removedBody.force, true);
      assert(!removedBody.delete_branch, "Keep branch is the default");
      assert(!removedBody.allowFailedArchiveHook && !removedBody.allowUnverifiedPtyStop);
      await deletion.waitFor({ state: "detached" });
      const removalEvidence = await control("normal");
      assert(removalEvidence.removed.includes(`library::${newPath}`));
      results.push({ tag, check: "dirty preview/explicit confirmation + default branch retention", pass: true });
      if (tag === "1440-light") {
        await openMenu("Empty parent");
        await page.getByRole("menuitem", { name: /new child worktree/i }).click();
        const recovered = page.getByRole("dialog");
        await recovered.getByRole("button", { name: "Create another worktree", exact: true }).click();
        await recovered.getByLabel("Name (optional)", { exact: true }).fill("recover-response");
        await control("lost-create");
        await recovered.getByRole("button", { name: "Create worktree", exact: true }).click();
        await recovered.getByText(/The result is not confirmed/).waitFor();
        const pendingEvidence = await control();
        const lostIntent = pendingEvidence.requests.filter((row) => row.action === "create").at(-1);
        await screenshot("create-uncertain");
        await recovered.getByRole("button", { name: "Cancel", exact: true }).click();
        await recovered.waitFor({ state: "detached" });
        await openMenu("Empty parent");
        await page.getByRole("menuitem", { name: /new child worktree/i }).click();
        await page.getByRole("dialog").getByRole("button", { name: "Check result", exact: true }).click();
        await page.getByRole("dialog").getByText("/Users/me/code/library/.worktrees/recover-response", { exact: true }).waitFor();
        const replayed = await control("normal");
        assert.equal(replayed.created, 2, "Lost acknowledgement creates exactly one additional checkout");
        const sameIntent = replayed.requests.filter((row) => row.action === "create" && row.body.name === "recover-response");
        assert(sameIntent.length >= 1 && sameIntent.every((row) => row.op_id === lostIntent.op_id));
        assert.equal(launches, 1);
        await page.getByRole("dialog").getByRole("button", { name: "Done", exact: true }).click();
        await page.getByRole("dialog").waitFor({ state: "detached" });
        results.push({ tag, check: "lost create acknowledgement / close/reopen / same receipt recovery without duplicate", pass: true });

        await control("unknown");
        await openMenu("recover-response");
        await page.getByRole("menuitem", { name: /delete worktree/i }).click();
        const unsure = page.getByRole("dialog");
        const deleteAgain = unsure.getByRole("button", { name: /^(Delete|Remove) worktree$/, exact: false });
        await deleteAgain.waitFor();
        await deleteAgain.click();
        await unsure.getByRole("button", { name: /check.*status/i }).waitFor();
        await screenshot("delete-uncertain");
        const reconciled = page.waitForResponse((response) => {
          const request = response.request();
          return request.method() === "POST" && new URL(response.url()).pathname.endsWith("/worktree/delete") && request.postDataJSON().reconcile === true;
        });
        await unsure.getByRole("button", { name: /check.*status/i }).click();
        await reconciled;
        const checked = await control();
        const deletionCalls = checked.requests.filter((row) => row.action === "delete" && row.body.workspace_id.endsWith("/recover-response"));
        assert.equal(deletionCalls.filter((row) => !row.body.preview && !row.body.reconcile).length, 1, "Unknown deletion never resubmits a mutation");
        assert(deletionCalls.some((row) => row.body.reconcile));
        await unsure.getByRole("button", { name: "Close", exact: true }).last().click();
        await unsure.waitFor({ state: "detached" });
        await control("normal");
        results.push({ tag, check: "unknown deletion retains target and only performs read-only reconciliation", pass: true });
      }
      assert.deepEqual(failures, []);
      await context.close();
    }
  }
  writeFileSync(join(output, "results.json"), JSON.stringify({ fixture: "synthetic worktree migration", results }, null, 2));
  const reset = await fetch(`${hub}/mock/worktree-migration?reset=true`);
  assert.equal((await reset.json()).fixture, "worktree-migration");
  console.log(`PASS worktree migration: ${results.length} full-app checks`);
} finally {
  clearTimeout(watchdog);
  await browser.close();
}
