import assert from "node:assert/strict";
import { test } from "node:test";
import { vitestShard } from "./test-shard.mjs";

test("an unset or empty shard keeps the complete suite and the Node regressions", (t) => {
  const previous = process.env.PERSONA_WEB_TEST_SHARD;
  delete process.env.PERSONA_WEB_TEST_SHARD;
  t.after(() => {
    if (previous === undefined) delete process.env.PERSONA_WEB_TEST_SHARD;
    else process.env.PERSONA_WEB_TEST_SHARD = previous;
  });
  for (const value of [undefined, ""]) {
    assert.deepEqual(vitestShard(value), { args: [], nodeTests: true });
  }
});

test("only the first hosted shard runs the Node regressions", () => {
  assert.deepEqual(vitestShard("1/2"), { args: ["--shard=1/2"], nodeTests: true });
  assert.deepEqual(vitestShard("2/2"), { args: ["--shard=2/2"], nodeTests: false });
  assert.deepEqual(vitestShard("3/4"), { args: ["--shard=3/4"], nodeTests: false });
});

test("a malformed or reversed shard fails before any test starts", () => {
  for (const value of ["0/2", "3/2", "1/0", "1", "a/b", " 1/2", "1/2 ", "1.5/2"]) {
    assert.throws(() => vitestShard(value), /PERSONA_WEB_TEST_SHARD must be <index>\/<count>/, value);
  }
});

test("shard bounds must be safe integers before comparison and formatting", () => {
  const max = "9007199254740991";
  assert.deepEqual(vitestShard(`${max}/${max}`), { args: [`--shard=${max}/${max}`], nodeTests: false });
  for (const value of [
    "9007199254740993/9007199254740992",
    "1/9007199254740992",
    "1000000000000000000000000000000001/1000000000000000000000000000000000",
    `1/${"9".repeat(400)}`,
  ]) {
    assert.throws(() => vitestShard(value), /PERSONA_WEB_TEST_SHARD must be <index>\/<count>/, value);
  }
});
