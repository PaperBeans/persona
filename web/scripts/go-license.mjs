// Where the Go toolchain keeps its LICENSE. The official distribution puts it in GOROOT; Homebrew's Go sets GOROOT to
// the formula's libexec directory and keeps LICENSE one level up (#4559).
import { existsSync } from "node:fs";
import { resolve } from "node:path";

export function goLicensePath(goroot) {
  const tried = [resolve(goroot, "LICENSE"), resolve(goroot, "..", "LICENSE")];
  const found = tried.find((path) => existsSync(path));
  if (found) return found;
  throw new Error(`The Go runtime's LICENSE was not found; looked in ${tried.join(" and ")}`);
}
