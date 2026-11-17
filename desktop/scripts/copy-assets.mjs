import { copyFile, rename, cp } from "node:fs/promises";
import { join } from "node:path";

await copyFile(join("src", "setup.html"), join("dist", "setup.html"));
await copyFile(join("src", "setup.css"), join("dist", "setup.css"));
await copyFile(join("src", "setup.js"), join("dist", "setup.cjs"));
await copyFile(join("src", "island.html"), join("dist", "island.html"));
await copyFile(join("src", "island.css"), join("dist", "island.css"));
await copyFile(join("src", "island.js"), join("dist", "island.cjs"));
await cp(join("src", "locales"), join("dist", "locales"), { recursive: true });
await cp(join("src", "assets"), join("dist", "assets"), { recursive: true });
for (const name of ["preload", "setup-preload", "island-preload", "contract", "link-contract", "notification-permission"]) {
  await rename(join("dist-preload", "desktop", "src", `${name}.cjs`), join("dist", `${name}.cjs`));
}
