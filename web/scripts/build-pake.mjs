// Compile the same pinned RFC 9807 implementation used by the hub for the browser.
import { execFileSync } from "node:child_process";
import { mkdirSync, copyFileSync, chmodSync, rmSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";
import { goLicensePath } from "./go-license.mjs";

const server = fileURLToPath(new URL("../../server/", import.meta.url));
const output = fileURLToPath(new URL("../public/pake/", import.meta.url));
mkdirSync(output, { recursive: true });
const goroot = execFileSync("go", ["env", "GOROOT"], { cwd: server, encoding: "utf8" }).trim();
execFileSync("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", resolve(output, "client.wasm"), "./cmd/password-wasm"], {
  cwd: server, env: { ...process.env, GOOS: "js", GOARCH: "wasm" }, stdio: "inherit",
});
const runtime = resolve(output, "wasm_exec.js");
rmSync(runtime, { force: true }); // Go module-cache files may be read-only, including a previous copy.
copyFileSync(resolve(goroot, "lib/wasm/wasm_exec.js"), runtime);
chmodSync(runtime, 0o644);

// Ship the exact licenses of the Go runtime and all modules compiled into the WASM client.
const dependencies = execFileSync("go", ["list", "-deps", "-f",
  "{{if and .Module (not .Module.Main)}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}", "./cmd/password-wasm"], {
  cwd: server, env: { ...process.env, GOOS: "js", GOARCH: "wasm" }, encoding: "utf8",
}).trim().split("\n").filter(Boolean);
const licenses = new Map();
for (const dependency of dependencies) {
  const [name, version, directory] = dependency.split("\t");
  licenses.set(`${name} ${version}`, readFileSync(resolve(directory, "LICENSE"), "utf8"));
}
const toolchain = execFileSync("go", ["env", "GOVERSION"], { cwd: server, encoding: "utf8" }).trim();
licenses.set(`Go runtime and standard library (${toolchain})`, readFileSync(goLicensePath(goroot), "utf8"));
writeFileSync(resolve(output, "THIRD_PARTY_NOTICES.txt"), "Persona password login WebAssembly client\n\n" +
  [...licenses].sort(([a], [b]) => a.localeCompare(b)).map(([name, license]) => `${name}\n${"=".repeat(name.length)}\n\n${license.trim()}\n`).join("\n"));
