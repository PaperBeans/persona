import assert from 'node:assert/strict';
import test from 'node:test';
import {briefWaitDuration} from '../entry/src/main/ets/core/PhoneBrief.ts';

test('briefings use two nonzero units, the one-minute floor and the same rounding as native iPhone', () => {
  const en = seconds => briefWaitDuration(seconds, 'en-US', '{count}d', '{count}h', '{count}m');
  for (const [value, label] of [[0, '1m'], [89, '1m'], [90, '2m'], [150, '2m'], [3570, '1h'],
    [3690, '1h 2m'], [4800, '1h 20m'], [86460, '1d 1m'], [93780, '1d 2h'], [95400, '1d 2h'], [172799, '2d']]) {
    assert.equal(en(value), label);
  }
  assert.equal(briefWaitDuration(4800, 'zh-Hans', '{count}天', '{count}小时', '{count}分钟'), '1小时20分钟');
  assert.equal(briefWaitDuration(93600, 'ja-JP', '{count}日', '{count}時間', '{count}分'), '1日2時間');
});
