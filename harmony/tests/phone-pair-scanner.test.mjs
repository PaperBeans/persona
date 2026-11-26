import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

let callbacks, actions;
const scan = {
  init() { actions.push('init'); },
  start(surface, callback) { actions.push('start:' + surface.surfaceId); callbacks.push(callback); },
  rescan() { actions.push('rescan'); },
  async stop() { actions.push('stop'); },
  async release() { actions.push('release'); }
};
globalThis.phoneScanFixture = scan;
registerHooks({
  resolve(path, context, next) {
    if (context.parentURL?.endsWith('/PhonePairScanner.ets'))
      return { url: 'phone-scan:' + path, shortCircuit: true };
    return next(path, context);
  },
  load(url, context, next) {
    let source;
    if (url.endsWith('/PhonePairScanner.ets')) source = stripTypeScriptTypes(readFileSync(new URL(url), 'utf8'));
    if (url === 'phone-scan:@kit.ScanKit') source = 'export const customScan = phoneScanFixture, scanCore = {ScanType:{QR_CODE:1}}, scanBarcode = {};';
    if (url === 'phone-scan:./Pairing') source = "export class Pairing { static parse(code) { if (code !== 'fixture-pair') throw Error('invalid_pairing'); } }";
    return source === undefined ? next(url, context) : { format:'module', source, shortCircuit:true };
  }
});
const { PhonePairScanner } = await import('../entry/src/main/ets/platform/PhonePairScanner.ets');
const frame = (...values) => values.map(originalValue => ({originalValue}));
const surface = surfaceId => ({surfaceId, width:370, height:280});
const settle = () => new Promise(resolve => setImmediate(resolve));

test('the inline camera reads one valid payload on its current surface and releases once', async () => {
  callbacks = []; actions = [];
  const reader = new PhonePairScanner(), codes = [];
  reader.start(surface('front'), value => codes.push(value), assert.fail);
  await settle();
  const [result] = callbacks;
  result(null, frame('unrelated'));
  result(null, frame('fixture-pair', 'other'));
  result(null, frame('fixture-pair', 'fixture-pair'));
  result(null, frame('fixture-pair'));
  await settle();
  assert.deepEqual(codes, ['fixture-pair']);
  assert.deepEqual(actions, ['init','start:front','rescan','rescan','stop','release']);
});

test('a surface replacement and a background cancellation ignore late camera callbacks', async () => {
  callbacks = []; actions = [];
  const reader = new PhonePairScanner(), codes = [], failures = [];
  const start = id => reader.start(surface(id), code => codes.push(code), () => failures.push(id));
  const first = start('one');
  await settle();
  const next = start('two');
  reader.stop(first);
  callbacks[0](null, frame('fixture-pair'));
  await settle();
  reader.stop(next);
  callbacks[1](new Error('closed'), []);
  await settle();
  assert.deepEqual(actions, ['init','start:one','stop','release','init','start:two','stop','release']);
  assert.deepEqual(codes, []); assert.deepEqual(failures, []);
});
