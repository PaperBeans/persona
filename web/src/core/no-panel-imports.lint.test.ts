// The headless core never imports the default panel. Test files may mount panel components; production files may not.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { describe, expect, it } from "vitest";

const SOURCE_FILE = /\.[cm]?[jt]sx?$/;
const TEST_FILE = /\.test\.[cm]?[jt]sx?$/;

function panelImports(source: string, file = "fixture.ts"): string[] {
  const kind = /\.[jt]sx$/.test(file) ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, kind);
  const out: string[] = [];
  const add = (node: ts.Node | undefined) => {
    let specifier: string | undefined;
    if (node && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))) specifier = node.text;
    else if (node && ts.isTemplateExpression(node)) specifier = node.head.text;
    if (specifier && /(^|\/)(views|components)(\/|$)/.test(specifier.replaceAll("\\", "/"))) out.push(specifier);
  };
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) add(node.moduleSpecifier);
    else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword) add(node.arguments[0]);
    else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) add(node.argument.literal);
    ts.forEachChild(node, visit);
  };
  visit(sf);
  return out;
}

describe("the headless-core panel boundary", () => {
  it("keeps production core and lib files out of views and components", () => {
    const core = fileURLToPath(new URL("./", import.meta.url));
    const violations: string[] = [];
    for (const root of [core, join(core, "../lib")]) {
      const files = (readdirSync(root, { recursive: true }) as string[])
        .filter((file) => SOURCE_FILE.test(file) && !TEST_FILE.test(file));
      for (const file of files) {
        for (const specifier of panelImports(readFileSync(join(root, file), "utf8"), file)) violations.push(`${file}: ${specifier}`);
      }
    }
    expect(violations).toEqual([]);
  });

  it("catches static imports, re-exports, dynamic imports and import types", () => {
    const source = [
      'import panel from "../views/panel";',
      'export { helper } from "../components/helper";',
      'export * from "../../views/shared";',
      'const panel = () => import("../views/panel");',
      "const component = () => import(`../components/${name}`);",
      'type Panel = import("../views/types").Panel;',
    ].join("\n");
    expect(panelImports(source)).toEqual([
      "../views/panel",
      "../components/helper",
      "../../views/shared",
      "../views/panel",
      "../components/",
      "../views/types",
    ]);
  });

  it("ignores non-panel imports and ordinary strings", () => {
    expect(panelImports('import { helper } from "../lib/helper";\nconst text = "../views/not-an-import";')).toEqual([]);
  });
});
