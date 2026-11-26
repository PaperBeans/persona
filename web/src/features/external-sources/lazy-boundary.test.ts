import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import ts from "typescript";
import { expect, it } from "vitest";

const root = resolve(import.meta.dirname, "../..");
const source = (path: string) => resolve(root, path);

function staticSources(entry: string): Set<string> {
  const seen = new Set<string>();
  const visit = (path: string) => {
    if (seen.has(path)) return;
    seen.add(path);
    const parsed = ts.createSourceFile(path, readFileSync(path, "utf8"), ts.ScriptTarget.Latest);
    for (const node of parsed.statements) {
      if (!ts.isImportDeclaration(node) && !ts.isExportDeclaration(node)) continue;
      if (ts.isImportDeclaration(node) ? node.importClause?.isTypeOnly : node.isTypeOnly) continue;
      if (!node.moduleSpecifier || !ts.isStringLiteral(node.moduleSpecifier)) continue;
      const spec = node.moduleSpecifier.text;
      if (!spec.startsWith(".")) continue;
      const target = resolve(dirname(path), spec);
      const resolved = [`${target}.ts`, `${target}.tsx`, resolve(target, "index.ts"), resolve(target, "index.tsx")].find(existsSync);
      if (resolved) visit(resolved);
    }
  };
  visit(source(entry));
  return seen;
}

it("keeps the binding API and both feature panels off the startup static imports", () => {
  const startup = new Set([...staticSources("main.tsx"), ...staticSources("App.tsx")]);
  for (const module of ["api.ts", "ExternalSourceBindingEditor.tsx", "ExternalSourceConnections.tsx", "ExternalSourceSettings.tsx", "ChannelDefaultsDialog.tsx"]) {
    expect(startup.has(source(`features/external-sources/${module}`)), module).toBe(false);
  }
});

it("does not statically load the editor from either of its work and Inbox entries", () => {
  const editor = source("features/external-sources/ExternalSourceBindingEditor.tsx");
  for (const entry of ["views/Todos.tsx", "views/inbox/InboxDetail.tsx", "features/external-sources/ExternalSourceConnections.tsx"]) {
    expect(staticSources(entry).has(editor), entry).toBe(false);
  }
});
