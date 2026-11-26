import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { access, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { test } from "node:test";

const runner = fileURLToPath(new URL("./process-group.mjs", import.meta.url));
const linux = process.platform === "linux";
const workerSource = `import { setInterval } from "node:timers";
import { writeFileSync } from "node:fs";
process.on("SIGTERM", () => {
  if (process.env.PERSONA_TEST_TERM_FILE) writeFileSync(process.env.PERSONA_TEST_TERM_FILE, "cleanup started");
});
if (process.env.PERSONA_TEST_WORKER_READY_FILE) writeFileSync(process.env.PERSONA_TEST_WORKER_READY_FILE, "ready");
setInterval(() => {}, 1_000);`;
const controlSource = `${workerSource}
process.on("disconnect", () => process.exit(0));`;
const fixtureSource = `import { spawn } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
const [mode, ready, workerSource, release] = process.argv.slice(2);
const sessionStat = readFileSync("/proc/" + process.ppid + "/stat", "utf8");
const watchdog = Number(sessionStat.slice(sessionStat.lastIndexOf(")") + 2).split(" ")[1]);
const workers = Array.from({ length: 3 }, (_, index) => spawn(process.execPath, [workerSource], {
  stdio: "ignore",
  env: { ...process.env, PERSONA_TEST_WORKER_READY_FILE: ready + ".worker-" + index },
}));
while (!workers.every((_, index) => existsSync(ready + ".worker-" + index))) await new Promise((resolve) => setTimeout(resolve, 5));
writeFileSync(ready, JSON.stringify({ pgid: process.ppid, session: process.ppid, watchdog, fixture: process.pid, workers: workers.map((child) => child.pid) }));
if (mode === "normal") {
  const hold = setInterval(() => { if (existsSync(release)) { clearInterval(hold); process.exit(0); } }, 10);
} else setInterval(() => {}, 1_000);`;

function alive(pid) {
  if (!pid) return false;
  try {
    const stat = awaitStat(pid);
    if (stat === "Z") return false;
    process.kill(pid, 0);
    return true;
  } catch { return false; }
}

// Synchronous /proc reads make zombie handling explicit: kill(pid, 0) alone
// counts a zombie as alive even though no worker is running any longer.
import { readFileSync } from "node:fs";
function awaitStat(pid) {
  const text = readFileSync(`/proc/${pid}/stat`, "utf8");
  return text.slice(text.lastIndexOf(")") + 2).split(" ")[0];
}

function groupPid(pid) {
  return Number(execFileSync("ps", ["-o", "pgid=", "-p", String(pid)], { encoding: "utf8" }).trim());
}

async function until(check, description, timeout = 5_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await check()) return;
    await delay(20);
  }
  throw new Error(`timed out waiting for ${description}`);
}

function stopped(child, timeout = 5_000) {
  return new Promise((resolve, reject) => {
    const finish = (code, signal) => { clearTimeout(timer); resolve({ code, signal }); };
    const timer = setTimeout(() => reject(new Error("timed out waiting for the supervised parent to close")), timeout);
    if (child.exitCode !== null || child.signalCode) finish(child.exitCode, child.signalCode);
    else child.once("close", finish);
  });
}

async function controlProcess() {
  const child = spawn(process.execPath, ["-e", controlSource], { detached: true, stdio: ["ignore", "ignore", "ignore", "ipc"] });
  await until(() => alive(child.pid), "the independent control process");
  return child;
}

async function exercise(mode, control) {
  const dir = await mkdtemp(join(tmpdir(), "persona-web-process-group-"));
  const fixture = join(dir, "fixture.mjs");
  const source = join(dir, "worker.mjs");
  const ready = join(dir, "ready.json");
  const release = join(dir, "release");
  const term = join(dir, "term");
  await writeFile(fixture, fixtureSource);
  await writeFile(source, workerSource);
  let child;
  let state;
  let watchdog;
  try {
    child = spawn(process.execPath, [runner, "--timeout-ms", mode === "timeout" ? "0" : "5000", "--", process.execPath, fixture, mode, ready, source, release], {
      detached: true, stdio: ["ignore", "pipe", "pipe", "ipc"],
      env: { ...process.env, PERSONA_TEST_TERM_FILE: term },
    });
    let output = "";
    child.stdout.on("data", (chunk) => { output += chunk; });
    child.stderr.on("data", (chunk) => { output += chunk; });
    await until(async () => {
      try { state = JSON.parse(await readFile(ready, "utf8")); return true; } catch { return false; }
    }, `${mode} fixture readiness`);
    watchdog = state.watchdog;
    assert.equal(state.pgid, state.session, "the owned command shares its session leader's process group");
    assert.notEqual(state.pgid, groupPid(process.pid), "the fixture never shares the test runner's process group");
    assert.notEqual(state.pgid, control.pid, "the fixture never shares the control's process group");
    assert.equal(state.workers.length, 3);
    assert.ok(state.workers.every(alive));

    let result;
    if (mode === "normal") {
      await writeFile(release, "release the normally completing fixture");
      await until(() => access(term).then(() => true, () => false), "normal cleanup to start");
      // This maximum-size deadline arrives after finish() has started its async
      // cleanup. An explicit acknowledgement proves the guard handled it there;
      // a late message after finished cannot make this regression pass by accident.
      let ignored;
      child.on("message", (message) => { if (message?.type === "deadline-ignored") ignored = message; });
      child.send({ type: "deadline", timeout: 2_147_483_647 });
      await until(() => ignored, "deadline cleanup acknowledgement");
      assert.equal(ignored.phase, "cleanup");
    }
    if (mode === "timeout") child.send({ type: "deadline", timeout: 150 });
    if (mode === "abrupt") {
      process.kill(child.pid, "SIGKILL");
      result = await stopped(child);
      assert.equal(result.signal, "SIGKILL");
    } else {
      result = await stopped(child);
      assert.equal(result.code, mode === "timeout" ? 124 : 0, output);
      if (mode === "timeout") assert.match(output, /exceeded 150 ms/);
    }
    await until(() => state.workers.every((pid) => !alive(pid)), `${mode} worker cleanup`);
    assert.equal(alive(state.fixture), false, `${mode}: fixture stopped`);
    assert.equal(alive(control.pid), true, `${mode}: unrelated control was not signalled`);
    await until(() => !alive(watchdog), `${mode} watchdog exit`);
  } finally {
    if (!state) {
      try { state = JSON.parse(await readFile(ready, "utf8")); } catch {}
      if (state && !watchdog) watchdog = state.watchdog;
    }
    // Red paths must not leak the synthetic processes either. Every pid and pgid
    // here came from this fixture, never from a name or session pattern.
    if (state) {
      try { process.kill(-state.pgid, "SIGKILL"); } catch {}
      for (const pid of state.workers) { try { process.kill(pid, "SIGKILL"); } catch {} }
    }
    if (child?.pid) { try { process.kill(-child.pid, "SIGKILL"); } catch {} }
    if (watchdog && alive(watchdog)) {
      const command = readFileSync(`/proc/${watchdog}/cmdline`, "utf8");
      if (command.includes(runner) && command.includes("--watchdog")) process.kill(watchdog, "SIGKILL");
    }
    await rm(dir, { recursive: true, force: true });
  }
}

test("normal completion, timeout, and abrupt parent death reap only their own workers", { skip: !linux, timeout: 30_000 }, async (t) => {
  const control = await controlProcess();
  try {
    for (const mode of ["normal", "timeout", "abrupt"]) {
      await t.test(mode, async () => {
        await exercise(mode, control);
        assert.equal(alive(control.pid), true, `${mode}: control survives the complete case`);
      });
    }
  } finally {
    try { process.kill(-control.pid, "SIGKILL"); } catch {}
    if (control.connected) control.disconnect();
  }
});

test("empty and overflowing deadlines fail before starting the command", async () => {
  const dir = await mkdtemp(join(tmpdir(), "persona-web-timeout-validation-"));
  const marker = join(dir, "started");
  try {
    for (const value of ["", " ", "2147483648"]) {
      const child = spawn(process.execPath, [runner, "--", process.execPath, "-e", "require('node:fs').writeFileSync(process.argv[1], 'started')", marker], {
        stdio: "ignore", env: { ...process.env, PERSONA_WEB_TEST_TIMEOUT_MS: value },
      });
      const result = await stopped(child);
      assert.notEqual(result.code, 0, `deadline ${JSON.stringify(value)} was accepted`);
      await assert.rejects(access(marker), "an invalid deadline must not start the command");
    }
  } finally { await rm(dir, { recursive: true, force: true }); }
});

test("real Vitest forks respect the cap and supervised parent death reaps only their group", { skip: !linux, timeout: 120_000 }, async (t) => {
  const web = fileURLToPath(new URL("..", import.meta.url));
  const vitest = join(web, "node_modules", "vitest", "vitest.mjs");
  const dir = await mkdtemp(join(tmpdir(), "persona-web-real-vitest-"));
  const fixtures = join(dir, "fixtures");
  const ownGroup = groupPid(process.pid);
  const control = await controlProcess();
  const owned = new Set();
  const fileCount = 12;
  const cap = 8;

  function liveGroup(pgid) {
    if (!pgid) return [];
    const output = execFileSync("ps", ["-eo", "pgid=,stat=,args="], { encoding: "utf8", timeout: 1_000, maxBuffer: 8 * 1024 * 1024 });
    return output.split("\n").flatMap((line) => {
      const match = line.trim().match(/^(\d+)\s+(\S+)\s+(.*)$/);
      if (!match || Number(match[1]) !== pgid || match[2].startsWith("Z")) return [];
      return [{ args: match[3] }];
    });
  }

  async function markers(active) {
    const values = [];
    for (const name of await readdir(active)) {
      if (!name.endsWith(".json")) continue;
      try {
        const value = JSON.parse(await readFile(join(active, name), "utf8"));
        if (Number.isSafeInteger(value.pgid) && value.pgid > 1) values.push(value);
      } catch {}
    }
    return values;
  }

  async function runCase(name, { config, expected, supervised = false, abrupt = false }) {
    const active = join(dir, `active-${name}`);
    const release = join(dir, `release-${name}`);
    await mkdir(active);
    const command = [vitest, "run", "--configLoader", "runner", "--config", config];
    const args = supervised
      ? [runner, "--timeout-ms", "0", "--", process.execPath, ...command]
      : command;
    const child = spawn(process.execPath, args, {
      cwd: dir,
      detached: true,
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, PERSONA_TEST_ACTIVE_DIR: active, PERSONA_TEST_RELEASE_FILE: release },
    });
    const groups = new Set([child.pid]);
    owned.add(child.pid);
    let output = "";
    let closed;
    let pgid;
    let peakActive = 0;
    let peakForks = 0;
    let triggered = false;
    let previousActive = 0;
    let stableSince = Date.now();
    const deadline = Date.now() + 30_000;
    child.stdout.on("data", (chunk) => { output = (output + chunk).slice(-20_000); });
    child.stderr.on("data", (chunk) => { output = (output + chunk).slice(-20_000); });
    child.once("error", (error) => { output += `\n${error.message}`; });
    child.once("close", (code, signal) => { closed = { code, signal }; });
    try {
      while (!closed) {
        const current = await markers(active);
        peakActive = Math.max(peakActive, current.length);
        if (current.length) {
          pgid = current[0].pgid;
          assert.notEqual(pgid, ownGroup, "the real Vitest fixture must not share the test runner's group");
          assert.notEqual(pgid, control.pid, "the real Vitest fixture must not share the control's group");
          groups.add(pgid);
          owned.add(pgid);
          const forks = liveGroup(pgid).filter((row) => row.args.includes("forks.js")).length;
          peakForks = Math.max(peakForks, forks);
        }
        if (current.length !== previousActive) {
          previousActive = current.length;
          stableSince = Date.now();
        }
        // Do not release or abort at the first glimpse of the expected count:
        // a broken cap may still be starting additional workers. Keep sampling
        // through a stable window so that excess workers make the test fail.
        if (!triggered && current.length >= expected && Date.now() - stableSince >= 250) {
          if (abrupt) process.kill(child.pid, "SIGKILL");
          else await writeFile(release, "release the real Vitest fixtures");
          triggered = true;
        }
        if (Date.now() > deadline) throw new Error(`${name}: timed out waiting for ${expected} real workers; ${output}`);
        await delay(10);
      }
      assert.equal(triggered, true, `${name}: exited before reaching ${expected} workers; ${output}`);
      if (abrupt) assert.equal(closed.signal, "SIGKILL", `${name}: the parent abort was not exercised`);
      else assert.equal(closed.code, 0, `${name}: real Vitest failed; ${output}`);

      let leftoverForks = 0;
      if (abrupt && !supervised) {
        // The unsupervised control must demonstrate the defect before its owned
        // group is cleaned in finally. Otherwise the supervised zero proves nothing.
        await delay(100);
        leftoverForks = liveGroup(pgid).filter((row) => row.args.includes("forks.js")).length;
        assert.ok(leftoverForks > 0, `${name}: direct parent death left no control orphans; ${output}`);
      } else {
        await until(() => liveGroup(pgid).length === 0, `${name} real worker cleanup`);
      }
      assert.equal(alive(control.pid), true, `${name}: independent control was signalled`);
      return { peakActive, peakForks, leftoverForks };
    } finally {
      if (!closed) { try { process.kill(child.pid, "SIGKILL"); } catch {} }
      for (const id of groups) {
        if (id <= 1 || id === ownGroup || id === control.pid) continue;
        // Ownership was established before recording the PGID. A process-table
        // failure must not prevent the cleanup signal for a known owned group.
        try { process.kill(-id, "SIGKILL"); } catch {}
      }
      await delay(100);
      for (const id of groups) if (!liveGroup(id).length) owned.delete(id);
    }
  }

  try {
    await mkdir(fixtures);
    await symlink(join(web, "node_modules"), join(dir, "node_modules"), "dir");
    const common = `include: ["fixtures/*.test.mjs"], pool: "forks", environment: "node", isolate: true, watch: false, testTimeout: 20000, execArgv: ["--no-experimental-webstorage"]`;
    const highConfig = join(dir, "high.config.mjs");
    const cappedConfig = join(dir, "capped.config.mjs");
    await writeFile(highConfig, `export default { test: { maxWorkers: ${fileCount}, ${common} } };\n`);
    const scheduling = pathToFileURL(join(web, "test-scheduling.ts")).href;
    await writeFile(cappedConfig, `import { testWorkerLimit } from ${JSON.stringify(scheduling)};\nexport default { plugins: [testWorkerLimit(64)], test: { ${common} } };\n`);
    for (let index = 0; index < fileCount; index += 1) {
      const source = `import { test } from "vitest";
import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { setTimeout as delay } from "node:timers/promises";
const stat = readFileSync("/proc/self/stat", "utf8");
const fields = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\\s+/);
const marker = process.env.PERSONA_TEST_ACTIVE_DIR + "/${index}-" + process.pid + ".json";
writeFileSync(marker, JSON.stringify({ pgid: Number(fields[2]) }));
test("hold a real fork until the parent observer releases it", async () => {
  const deadline = Date.now() + 15000;
  try {
    while (!existsSync(process.env.PERSONA_TEST_RELEASE_FILE)) {
      if (Date.now() > deadline) throw new Error("observer did not release the fixture");
      await delay(10);
    }
  } finally { try { rmSync(marker); } catch {} }
});
`;
      await writeFile(join(fixtures, `worker-${index}.test.mjs`), source);
    }

    const high = await runCase("high", { config: highConfig, expected: fileCount });
    const capped = await runCase("capped", { config: cappedConfig, expected: cap, supervised: true });
    const direct = await runCase("direct-abort", { config: cappedConfig, expected: cap, abrupt: true });
    const supervised = await runCase("supervised-abort", { config: cappedConfig, expected: cap, supervised: true, abrupt: true });
    assert.deepEqual([high.peakActive, high.peakForks], [fileCount, fileCount]);
    assert.deepEqual([capped.peakActive, capped.peakForks, capped.leftoverForks], [cap, cap, 0]);
    assert.ok(direct.leftoverForks > 0);
    assert.deepEqual([supervised.peakActive, supervised.peakForks, supervised.leftoverForks], [cap, cap, 0]);
    t.diagnostic(JSON.stringify({ high, capped, direct, supervised }));
  } finally {
    for (const id of owned) {
      if (id <= 1 || id === ownGroup || id === control.pid) continue;
      try { process.kill(-id, "SIGKILL"); } catch {}
    }
    try { process.kill(-control.pid, "SIGKILL"); } catch {}
    if (control.connected) control.disconnect();
    await rm(dir, { recursive: true, force: true });
  }
});
