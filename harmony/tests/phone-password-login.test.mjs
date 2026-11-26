import assert from 'node:assert/strict';
import { createHash, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

const exchangeRoute = '/api/v1/auth/password/exchange';
const calls = [];
let exchange = async () => ({ token: 'jd_fixture', id: 'dev_fixture', hub_id: 'hub_fixture' });
globalThis.phonePasswordTest = {
  random: size => randomBytes(size),
  hash: bytes => createHash('sha256').update(bytes).digest(),
  async request(endpoint, path, method, body) {
    calls.push({ endpoint, path, method, body: JSON.parse(body) });
    return JSON.stringify(await exchange());
  }
};
registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('/PhonePasswordLogin.ets')) {
      return { url: 'phone-password-test:' + specifier, shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    const module = source => ({ format: 'module', source, shortCircuit: true });
    if (url.endsWith('/PhonePasswordLogin.ets')) return module(stripTypeScriptTypes(readFileSync(new URL(url), 'utf8')));
    if (url === 'phone-password-test:@kit.ArkTS') return module(`
      class NativeURL extends URL { static parseURL(value) { return new NativeURL(value); } get params() { return this.searchParams; } }
      class Base64Helper { encodeToStringSync(bytes) { return Buffer.from(bytes).toString('base64'); } }
      class NativeTextEncoder { encodeInto(value) { return new TextEncoder().encode(value); } }
      export const url = { URL: NativeURL }; export const util = { Base64Helper, TextEncoder: NativeTextEncoder };`);
    if (url === 'phone-password-test:@kit.CryptoArchitectureKit') return module(`
      export const cryptoFramework = {
        createRandom() { return { async generateRandom(n) { return {data: phonePasswordTest.random(n)}; } }; },
        createMd() { let data; return { async update(value) { data=value.data; }, async digest() { return {data:phonePasswordTest.hash(data)}; } }; }
      };`);
    if (url === 'phone-password-test:./HubTransport') return module(`
      export class EndpointConfig { constructor(baseUrl, hubId, token, pin) {Object.assign(this,{baseUrl,hubId,token,pin});} }
      export class HubTransport { constructor(endpoint) { this.endpoint=endpoint; }
        request(...args) { return phonePasswordTest.request(this.endpoint,...args); } close() {} }`);
    return next(url, context);
  }
});
const { PhonePasswordLogin } = await import('../entry/src/main/ets/platform/PhonePasswordLogin.ets');
const code = 'S'.repeat(43);
const callback = (state, proof = code) => 'persona://password-login?state=' + state + '&code=' + proof;

function visit(login, address = 'example.test:1943', alive = () => true) {
  let opened;
  const page = new Promise(resolve => { opened = resolve; });
  const completed = login.authorize(address, 'My phone', async uri => { opened(new URL(uri)); }, alive);
  return { page, completed };
}

test('the system browser carries only the nonce and proof challenge and exchanges once on the original HTTPS hub', async () => {
  calls.length = 0;
  const login = new PhonePasswordLogin();
  const flow = visit(login), page = await flow.page, fields = new URLSearchParams(page.hash.slice(1));
  assert.equal(page.origin, 'https://example.test:1943');
  assert.equal(page.pathname, '/login/device');
  assert.equal(page.search, '');
  assert.deepEqual([...fields.keys()].sort(), ['challenge', 'kind', 'name', 'state']);
  assert.equal(fields.get('kind'), 'phone');
  assert.equal(fields.get('name'), 'My phone');
  const state = fields.get('state');
  assert.match(state, /^[A-Za-z0-9_-]{43}$/);
  assert.equal(login.receive(callback('T'.repeat(43))), false);
  assert.equal(login.receive(callback(state, 'short')), false);
  assert.equal(login.receive(callback(state) + '&state=' + state), false);
  assert.equal(login.receive(callback(state).replace('password-login', 'pair')), false);
  assert.equal(calls.length, 0);
  assert.equal(login.receive(callback(state)), true);
  assert.equal(login.receive(callback(state)), false);
  const endpoint = await flow.completed;
  assert.equal(endpoint.hubId, 'hub_fixture');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].endpoint.token, '');
  assert.equal(calls[0].endpoint.baseUrl, page.origin);
  assert.equal(calls[0].path, exchangeRoute);
  assert.equal(calls[0].method, 'POST');
  assert.equal(calls[0].body.code, code);
  assert.equal(createHash('sha256').update(calls[0].body.verifier).digest('base64url'), fields.get('challenge'));
});

test('a closed or replaced browser flow cannot authenticate the current sheet', async () => {
  calls.length = 0;
  const login = new PhonePasswordLogin(), old = visit(login), first = await old.page;
  const failed = assert.rejects(old.completed, /request_updated/);
  const fresh = visit(login), second = await fresh.page;
  await failed;
  const state = page => new URLSearchParams(page.hash.slice(1)).get('state');
  assert.notEqual(state(first), state(second));
  assert.equal(login.receive(callback(state(first))), false);
  const cancelled = assert.rejects(fresh.completed, /request_updated/);
  login.cancel();
  await cancelled;
  assert.equal(login.receive(callback(state(second))), false);
  assert.deepEqual(calls, []);
});

test('only a plain HTTPS hub origin can open the browser and the original sheet must still own the exchange', async () => {
  const login = new PhonePasswordLogin();
  for (const address of ['http://example.test', 'https://u:p@example.test', 'https://example.test/path', 'https://example.test/?a=b', 'https://example.test/#x']) {
    await assert.rejects(login.authorize(address, 'Phone', () => assert.fail('opened'), () => true), /phone_password_address/);
  }
  let current = true, finish, began;
  const started = new Promise(resolve => { began = resolve; });
  exchange = () => new Promise(resolve => { began(); finish = resolve; });
  try {
    const flow = visit(login, 'https://example.test', () => current), page = await flow.page;
    login.receive(callback(new URLSearchParams(page.hash.slice(1)).get('state')));
    await started;
    current = false;
    finish({ token: 'jd_fixture', id: 'dev_fixture', hub_id: 'hub_fixture' });
    await assert.rejects(flow.completed, /request_updated/);
  } finally { exchange = async () => ({ token: 'jd_fixture', id: 'dev_fixture', hub_id: 'hub_fixture' }); }
});
