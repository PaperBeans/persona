import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { goLicensePath } from "./go-license.mjs";

let root;
const layout = (files) => {
  root = mkdtempSync(join(tmpdir(), "go-license-"));
  const goroot = join(root, "go", "1.0", "libexec");
  mkdirSync(goroot, { recursive: true });
  for (const file of files) writeFileSync(join(goroot, file), "license");
  return goroot;
};
afterEach(() => rmSync(root, { recursive: true, force: true }));

describe("the Go runtime's license", () => {
  it("is read from GOROOT, as the official distribution has it", () => {
    const goroot = layout(["LICENSE", "../LICENSE"]);
    expect(goLicensePath(goroot)).toBe(join(goroot, "LICENSE"));
  });

  it("is read from GOROOT's parent, where Homebrew keeps it", () => {
    const goroot = layout(["../LICENSE"]);
    expect(goLicensePath(goroot)).toBe(join(root, "go", "1.0", "LICENSE"));
  });

  it("stops the build and names both places when neither has it", () => {
    const goroot = layout([]);
    expect(() => goLicensePath(goroot)).toThrow(`looked in ${join(goroot, "LICENSE")} and ${join(root, "go", "1.0", "LICENSE")}`);
  });
});
