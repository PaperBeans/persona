import { spawn } from "node:child_process";
import { constants } from "node:os";
import { fileURLToPath } from "node:url";
import { vitestShard } from "./test-shard.mjs";

const signals = constants.signals;

const web = fileURLToPath(new URL("..", import.meta.url));
const vitest = fileURLToPath(new URL("../node_modules/vitest/vitest.mjs", import.meta.url));

function run(args) {
  return new Promise((resolve) => {
    const child = spawn(process.execPath, args, { cwd: web, stdio: "inherit", windowsHide: true });
    child.once("error", (error) => resolve({ code: 127, error }));
    child.once("close", (code, signal) => resolve({ code: code ?? 128 + (signals[signal] ?? 1) }));
  });
}

// Keep the existing Node regression first, then run Vitest. CI's native file shards run that regression only on lane 1.
// Extra npm test arguments belong to Vitest, as they did before this supervisor.
const shard = vitestShard();
const node = shard.nodeTests
  ? await run(["--test", "e2e/extension-mirrors-cleanup.node-test.mjs", "scripts/process-group.node-test.mjs", "scripts/test-shard.node-test.mjs"])
  : { code: 0 };
const result = node.code === 0 ? await run([vitest, "run", ...process.argv.slice(2), ...shard.args]) : node;
if (result.error) console.error(result.error.message);
process.exitCode = result.code;
