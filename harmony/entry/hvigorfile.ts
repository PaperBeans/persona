import { hapTasks } from '@ohos/hvigor-ohos-plugin';
import type { HvigorNode, HvigorPlugin } from '@ohos/hvigor';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { basename, join, resolve } from 'node:path';

// Run the generated bincode codec as JS. ArkTS sees its emitted public declarations,
// while UTF-8 conversion uses the platform implementation available on the phone.
function registerNativeReplay(node: HvigorNode): void {
  const entry = node.getNodeDir().getPath();
  const root = resolve(entry, '../..');
  const wire = join(entry, 'src/main/ets/core/rustcore/Wire.ts');
  const generated = join(entry, 'src/ohosTest/ets/generated');
  const fixtures = join(root, 'spec/fixtures/continuity');
  const packager = join(root, 'native/app-core/scripts/package-ohos-fixtures.py');
  const resources = join(entry, 'src/ohosTest/resources/rawfile/core-fixtures');
  node.registerTask({
    name: 'prepareNativeCoreReplay',
    postDependencies: ['ohosTest@PreBuild'],
    input(input) {
      input.files([wire, packager, join(entry, 'hvigorfile.ts')]);
      input.files(readdirSync(fixtures).filter(file => file.endsWith('.json')).map(file => join(fixtures, file)));
    },
    output(output) {
      output.files([join(generated, 'Wire.js'), join(generated, 'Wire.d.ts')]);
      output.files([join(resources, 'continuity.json'), join(resources, 'manifest.json')]);
    },
    run() {
      const ts = createRequire(require.resolve('@ohos/hvigor'))('typescript');
      const program = ts.createProgram([wire], {
        target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext, declaration: true, skipLibCheck: true,
      });
      mkdirSync(generated, { recursive: true });
      const utf8 = `import util from '@ohos.util';
class TextEncoder { encode(text) { return new util.TextEncoder().encodeInto(text); } }
class TextDecoder { decode(bytes) { return util.TextDecoder.create('utf-8').decodeToString(bytes); } }
`;
      const result = program.emit(undefined, (name, contents) => {
        writeFileSync(join(generated, basename(name)), (name.endsWith('.js') ? utf8 : '') + contents);
      });
      if (result.emitSkipped || result.diagnostics.length) {
        const diagnostics = result.diagnostics.map(item => ts.flattenDiagnosticMessageText(item.messageText, '\n'));
        throw new Error('Failed to emit the native core replay codec. ' + diagnostics.join('\n'));
      }
      execFileSync('python3', ['-I', packager, '--fixtures', fixtures, '--output', resources], { stdio: 'inherit' });
    },
  });
}

const nativeReplay: HvigorPlugin = {
  pluginId: 'persona-native-core-replay',
  apply(node) {
    node.afterNodeEvaluate(target => {
      if (target.getTaskByName('ohosTest@PreBuild')) registerNativeReplay(target);
    });
  },
};

export default { system: hapTasks, plugins: [nativeReplay] };
