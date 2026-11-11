// JSON-lines driver for Go <-> browser-WASM interoperability tests. This never talks to a network.
import { readFileSync } from "node:fs";
import { createInterface } from "node:readline";
import { runInThisContext } from "node:vm";
import { webcrypto } from "node:crypto";
import { resolve } from "node:path";

globalThis.crypto ??= webcrypto;
const directory = process.argv[2];
runInThisContext(readFileSync(resolve(directory, "wasm_exec.js"), "utf8"));
const go = new Go();
const { instance } = await WebAssembly.instantiate(readFileSync(resolve(directory, "client.wasm")), go.importObject);
void go.run(instance);
for await (const line of createInterface({ input: process.stdin })) {
  process.stdout.write(globalThis.personaPake(line) + "\n");
}
process.exit(0);
