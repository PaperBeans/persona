// CI opts into Vitest's native file sharding. A plain local npm test stays complete.
// The small Node regression set has no shard option of its own, so it belongs to the first lane only.
export function vitestShard(value = process.env.PERSONA_WEB_TEST_SHARD) {
  if (value === undefined || value === "") return { args: [], nodeTests: true };
  const match = /^([1-9]\d*)\/([1-9]\d*)$/.exec(value);
  const index = Number(match?.[1]);
  const count = Number(match?.[2]);
  if (!Number.isSafeInteger(index) || !Number.isSafeInteger(count) || index > count) {
    throw new Error(`PERSONA_WEB_TEST_SHARD must be <index>/<count> with 1 <= index <= count; got ${JSON.stringify(value)}`);
  }
  return { args: [`--shard=${index}/${count}`], nodeTests: index === 1 };
}
