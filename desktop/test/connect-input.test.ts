import assert from "node:assert/strict";
import test from "node:test";
import { extractPersonaLinkURL, parseConnectInput } from "../src/connect-input.js";

const SAMPLE_FP = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

test("persona://pair link extracts Hub URL, #token fragment for 1-step login, and SPKI pin for direct TLS", () => {
  const raw = `persona://pair?url=http%3A%2F%2F192.0.2.10%3A18660&tls=18662&fp=${SAMPLE_FP}&token=jp_one_step`;
  const parsed = parseConnectInput(raw);
  assert.equal(parsed.kind, "hub");
  assert.equal(parsed.autoSignIn, true);
  assert.equal(parsed.hub.url.href, "https://192.0.2.10:18662/");
  assert.equal(parsed.initialURL, "https://192.0.2.10:18662/#token=jp_one_step");
  assert.deepEqual(parsed.pin, { origin: "https://192.0.2.10:18662", fp: SAMPLE_FP });
});

test("persona://pair behind HTTPS reverse proxy uses system trust without pinning", () => {
  const raw = `persona://pair?url=https%3A%2F%2Fhub.example.com%2Fpersona&tls=18662&fp=${SAMPLE_FP}&token=jp_proxy`;
  const parsed = parseConnectInput(raw);
  assert.equal(parsed.kind, "hub");
  assert.equal(parsed.autoSignIn, true);
  assert.equal(parsed.hub.url.href, "https://hub.example.com/persona/");
  assert.equal(parsed.initialURL, "https://hub.example.com/persona/#token=jp_proxy");
  assert.equal(parsed.pin, undefined);
});

test("HTTP(S) URL carrying #token or ?token preserves auto-sign-in and normalizes hub origin", () => {
  const withHash = parseConnectInput("https://hub.example.com/persona/#token=jp_hash_tok");
  assert.equal(withHash.kind, "hub");
  assert.equal(withHash.autoSignIn, true);
  assert.equal(withHash.hub.url.href, "https://hub.example.com/persona/");
  assert.equal(withHash.initialURL, "https://hub.example.com/persona/#token=jp_hash_tok");

  const withQuery = parseConnectInput("https://hub.example.com/?token=jp_query_tok");
  assert.equal(withQuery.kind, "hub");
  assert.equal(withQuery.autoSignIn, true);
  assert.equal(withQuery.hub.url.href, "https://hub.example.com/");
  assert.equal(withQuery.initialURL, "https://hub.example.com/#token=jp_query_tok");
});

test("persona://link or pasted CLI command is recognized as link-pair with suggestedHub and pin", () => {
  const cmd = `persona-link pair 'persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&tls=18662&fp=${SAMPLE_FP}&machine=laptop&token=jp_link'`;
  assert.equal(
    extractPersonaLinkURL(cmd),
    `persona://link?url=http%3A%2F%2F192.0.2.10%3A18660&tls=18662&fp=${SAMPLE_FP}&machine=laptop&token=jp_link`,
  );
  const parsed = parseConnectInput(cmd);
  assert.equal(parsed.kind, "link-pair");
  assert.equal(parsed.suggestedHub?.url.href, "https://192.0.2.10:18662/");
  assert.deepEqual(parsed.pin, { origin: "https://192.0.2.10:18662", fp: SAMPLE_FP });
});

test("plain Hub addresses parse without autoSignIn and reject malformed links", () => {
  const plain = parseConnectInput("hub.example.com/persona");
  assert.equal(plain.kind, "hub");
  assert.equal(plain.autoSignIn, false);
  assert.equal(plain.hub.url.href, "https://hub.example.com/persona/");
  assert.throws(() => parseConnectInput(""));
  assert.throws(() => parseConnectInput("persona://pair?url=https%3A%2F%2Fhub.example.com"));
});
