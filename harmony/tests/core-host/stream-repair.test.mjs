// Replays the Apple StreamRepair assertions and existing JNI-derived boundaries through the real Rust core's ArkTS
// host projection command. Expectations are independently transcribed from the Apple tests/source and the existing
// JNI replay, never from Rust output. Host evidence only: no NAPI addon, production adapter, HAP or device run.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { startCore } from './host.mjs';

// 22 fence expectations (21 repair inputs plus the additional `let a` end input): StreamRepairTests.closesAnOpenCodeFenceWithItsOwnMarker,
// holdsAFencesOpeningLineBackUntilItsInfoStringIsWhole, holdsAHalfWrittenClosingRunBack,
// changesNothingInsideAFenceButItsEnd and closesAFenceInTheListItemItOpenedIn. The `prefix` input is the existing
// JNI-derived partial-closing-run boundary, not an Apple test assertion. Every entry is a complete {repaired, end}
// object; the end values come from StreamRepair.end's code/fence rules, never from the repaired text or Rust output.
const fence = [
  [{text: 'text\n```ts\nconst a', repaired: 'text\n```ts\nconst a\n```', end: 'code'}, 'closesAnOpenCodeFenceWithItsOwnMarker'],
  [{text: '~~~~\nx\n', repaired: '~~~~\nx\n~~~~', end: 'code'}, 'closesAnOpenCodeFenceWithItsOwnMarker'],
  [{text: '```\na\n```\ndone', repaired: '```\na\n```\ndone', end: 'text'}, 'closesAnOpenCodeFenceWithItsOwnMarker'],
  [{text: '看这段：\n```swi', repaired: '看这段：\n', end: 'code'}, 'holdsAFencesOpeningLineBackUntilItsInfoStringIsWhole'],
  [{text: '```mermaid', repaired: '', end: 'code'}, 'holdsAFencesOpeningLineBackUntilItsInfoStringIsWhole'],
  [{text: '看这段：\n```swift\n', repaired: '看这段：\n```swift\n```', end: 'code'}, 'holdsAFencesOpeningLineBackUntilItsInfoStringIsWhole'],
  [{text: '```\nlet a = 1\n``', repaired: '```\nlet a = 1\n```', end: 'code'}, 'holdsAHalfWrittenClosingRunBack'],
  [{text: '````\nx\n```', repaired: '````\nx\n````', end: 'code'}, 'holdsAHalfWrittenClosingRunBack'],
  [{text: '````\nx\n```\n', repaired: '````\nx\n```\n````', end: 'code'}, 'holdsAHalfWrittenClosingRunBack'],
  [{text: 'prefix\n````\nx\n```\n', repaired: 'prefix\n````\nx\n```\n````', end: 'code'}, 'JNI-derived prefix + partial closer'],
  [{text: '```\n**x [a](b', repaired: '```\n**x [a](b\n```', end: 'code'}, 'changesNothingInsideAFenceButItsEnd'],
  [{text: '```\n-', repaired: '```\n-\n```', end: 'code'}, 'changesNothingInsideAFenceButItsEnd'],
  [{text: '1. 安装依赖：\n\n   ```bash\n   npm install', repaired: '1. 安装依赖：\n\n   ```bash\n   npm install\n   ```', end: 'code'}, 'closesAFenceInTheListItemItOpenedIn'],
  [{text: '1. 安装依赖：\n   ```bash\n   npm install\n', repaired: '1. 安装依赖：\n   ```bash\n   npm install\n   ```', end: 'code'}, 'closesAFenceInTheListItemItOpenedIn'],
  [{text: '- 运行：\n  ~~~\n  make', repaired: '- 运行：\n  ~~~\n  make\n  ~~~', end: 'code'}, 'closesAFenceInTheListItemItOpenedIn'],
  [{text: '- 运行：\n  ```\n  make\n  ``', repaired: '- 运行：\n  ```\n  make\n  ```', end: 'code'}, 'closesAFenceInTheListItemItOpenedIn'],
  [{text: '```\na\n  ', repaired: '```\na\n```', end: 'code'}, 'leavesOutALineOfOnlyIndentationSoFar'],
  [{text: '这是 **重要', repaired: '这是 **重要**', end: 'text'}, 'closesWhatTheGrowingLineLeavesOpen (#9286)'],
  [{text: '```swift\nlet a\n```', repaired: '```swift\nlet a\n```', end: 'code'}, 'tellsWhereTheTextEnds'],
  [{text: '```swift\nlet a\n```\n', repaired: '```swift\nlet a\n```\n', end: 'text'}, 'tellsWhereTheTextEnds'],
  [{text: '```swift\nlet a', repaired: '```swift\nlet a\n```', end: 'code'}, 'tellsWhereTheTextEnds'],
  [{text: '', repaired: '', end: 'text'}, 'tellsWhereTheTextEnds'],
];

// 14 unfinished-marker expectations: holdsALastLineOfOnlyMarkersBack, leavesALineThatHasEnded and existing JNI
// controls. All are complete objects. The ten-digit/full-width and marker-prefixed digit boundaries follow
// Scan.markersOnly's ASCII/length/position rules; Scan.string retains the newline before a held-back marker.
const marker = [
  [{text: '- 第一项\n-', repaired: '- 第一项\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '- 第一项\n- ', repaired: '- 第一项\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '1. 第一步\n2.', repaired: '1. 第一步\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '1. 第一步\n12', repaired: '1. 第一步\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '正文\n##', repaired: '正文\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '正文\n>', repaired: '正文\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '标题\n---', repaired: '标题\n', end: 'text'}, 'Apple markersOnly'],
  [{text: '- [ ]', repaired: '', end: 'text'}, 'Apple markersOnly'],
  [{text: '**', repaired: '', end: 'text'}, 'Apple markersOnly'],
  [{text: '- 第', repaired: '- 第', end: 'text'}, 'Apple markersOnly control'],
  [{text: '2026年', repaired: '2026年', end: 'text'}, 'Apple markersOnly control'],
  [{text: '1.5 倍', repaired: '1.5 倍', end: 'text'}, 'Apple markersOnly control'],
  [{text: '-\n', repaired: '-\n', end: 'text'}, 'leavesALineThatHasEnded'],
  [{text: '正文\n---\n', repaired: '正文\n---\n', end: 'text'}, 'existing JNI ended-line control'],
  [{text: '1234567890', repaired: '1234567890', end: 'text'}, 'existing JNI ten-digit boundary'],
  [{text: '- 12', repaired: '- 12', end: 'text'}, 'existing JNI marker-prefixed digits'],
  [{text: '１２', repaired: '１２', end: 'text'}, 'existing JNI full-width digits'],
  [{text: '\n-', repaired: '\n', end: 'text'}, 'existing JNI leading blank line'],
  [{text: '\n##', repaired: '\n', end: 'text'}, 'existing JNI leading blank line'],
];

// 39 table expectations (22 repair inputs, all 15 original end inputs including overlaps, and seven
// JNI/Foundation delimiter boundaries): from holdsATablesHalfWrittenLastRowBack,
// givesATableHeaderItsDelimiterRowEarly, findsATableRightUnderAParagraphLine, keepsATableInTheListItemItIsIn
// and findsNoTableInALazyLine. Complete end values follow the explicitly asserted table boundaries and
// StreamRepair.end's no-table/no-fence fallback. JNI-derived delimiter boundaries use Foundation .whitespaces:
// tab and Unicode Zs are trimmed; CR, vertical tab, NEL, line separator and paragraph separator are not.
const head = '| 文件 | 改动 |\n| --- | --- |\n| a.swift | 刷新 |\n';
const table = [
  [{text: head + '| b.swi', repaired: head, end: 'table'}, 'half-written last row'],
  [{text: head + '| b.swift | 超时 |\n', repaired: head + '| b.swift | 超时 |\n', end: 'table'}, 'finished last row'],
  [{text: '| 文件 | 改', repaired: '| 文件 | 改', end: 'text'}, 'unfinished header'],
  [{text: 'Use a | b\n| not a table', repaired: 'Use a | b\n| not a table', end: 'text'}, 'pipe in prose'],
  [{text: '| 文件 | 改动 |\n', repaired: '| 文件 | 改动 |\n| --- | --- |', end: 'table'}, 'early delimiter'],
  [{text: '| a | b |\n| --', repaired: '| a | b |\n| --- | --- |', end: 'table'}, 'partial delimiter'],
  [{text: '| a | b |\n|', repaired: '| a | b |\n| --- | --- |', end: 'table'}, 'partial delimiter'],
  [{text: '## 结果\n| a | b | c |\n', repaired: '## 结果\n| a | b | c |\n| --- | --- | --- |', end: 'table'}, 'heading + header'],
  [{text: '| a | b |\n| :-- | --: |', repaired: '| a | b |\n| :-- | --: |', end: 'table'}, 'aligned delimiter'],
  [{text: '| 不是表格\n', repaired: '| 不是表格\n', end: 'text'}, 'prose pipe row'],
  [{text: '| x |\n| a | b |\n', repaired: '| x |\n| a | b |\n', end: 'text'}, 'pipe row under pipe row'],
  [{text: '**结果：**\n| a | b |\n', repaired: '**结果：**\n| a | b |\n| --- | --- |', end: 'table'}, 'paragraph + header'],
  [{text: '**结果：**\n| a | b |\n| --', repaired: '**结果：**\n| a | b |\n| --- | --- |', end: 'table'}, 'paragraph + delimiter'],
  [{text: '**结果：**\n| a | b |\n| --- | --- |\n| 1', repaired: '**结果：**\n| a | b |\n| --- | --- |\n', end: 'table'}, 'Apple repair/end'],
  [{text: '- 项\n\n  | a | b |\n', repaired: '- 项\n\n  | a | b |\n  | --- | --- |', end: 'table'}, 'Apple list table/end'],
  [{text: '1. 项\n   | a | b |\n   | -', repaired: '1. 项\n   | a | b |\n   | --- | --- |', end: 'table'}, 'ordered list table'],
  [{text: '- 项\n  | a | b |\n  | --- | --- |\n  | 1', repaired: '- 项\n  | a | b |\n  | --- | --- |\n', end: 'table'}, 'Apple list repair/end'],
  [{text: '- 项\n\n  | a | b |\n  | --- | --- |\n  ', repaired: '- 项\n\n  | a | b |\n  | --- | --- |\n', end: 'table'}, 'Apple list repair/end'],
  [{text: '- 项\n| a | b |\n', repaired: '- 项\n| a | b |\n', end: 'text'}, 'lazy list continuation'],
  [{text: '- a\n  - b\n  | x |\n', repaired: '- a\n  - b\n  | x |\n', end: 'text'}, 'lazy list continuation'],
  [{text: '> 引用\n| a | b |\n', repaired: '> 引用\n| a | b |\n', end: 'text'}, 'quote continuation'],
  [{text: '- 项\n| a | b |\n| --- | --- |\n| 1', repaired: '- 项\n| a | b |\n| --- | --- |\n| 1', end: 'text'}, 'Apple lazy repair/end'],
  [{text: '**结果：**\n| a | b |\n| --- | --- |\n| 1 | 2 |\n', repaired: '**结果：**\n| a | b |\n| --- | --- |\n| 1 | 2 |\n', end: 'table'}, 'Apple finished table end'],
  [{text: '| a | b |\n| --- | --- |\nfoo', repaired: '| a | b |\n| --- | --- |\nfoo', end: 'table'}, 'Apple row without pipes'],
  [{text: '| a | b |\n| --- | --- |\n- 项', repaired: '| a | b |\n| --- | --- |\n- 项', end: 'text'}, 'Apple block after table'],
  [{text: '- 项\n\n  | a | b |\n  ', repaired: '- 项\n\n  | a | b |\n  | --- | --- |', end: 'table'}, 'Apple indented table end'],
  [{text: '| a | b |\n| --- | --- |\n| 1', repaired: '| a | b |\n| --- | --- |\n', end: 'table'}, 'tellsWhereTheTextEnds'],
  [{text: '| a | b |\n| --- | --- |\n| 1 | 2 |\n', repaired: '| a | b |\n| --- | --- |\n| 1 | 2 |\n', end: 'table'}, 'tellsWhereTheTextEnds'],
  [{text: '| a | b |\n', repaired: '| a | b |\n| --- | --- |', end: 'table'}, 'tellsWhereTheTextEnds'],
  [{text: '| a | b |\n| -', repaired: '| a | b |\n| --- | --- |', end: 'table'}, 'tellsWhereTheTextEnds'],
  [{text: '| a | b', repaired: '| a | b', end: 'text'}, 'tellsWhereTheTextEnds'],
  [{text: '| a | b |\n| --- | --- |\n\n后文', repaired: '| a | b |\n| --- | --- |\n\n后文', end: 'text'}, 'tellsWhereTheTextEnds'],
  [{text: '| a |\n| -\r |\n| x', repaired: '| a |\n| -\r |\n| x', end: 'text'}, 'existing JNI CR delimiter'],
  [{text: '| a |\n| -\u000b |\n| x', repaired: '| a |\n| -\u000b |\n| x', end: 'text'}, 'existing JNI vertical-tab delimiter'],
  [{text: '| a |\n| -\t |\n| x\n', repaired: '| a |\n| -\t |\n| x\n', end: 'table'}, 'existing JNI tab delimiter'],
  [{text: '| a |\n| -\u00a0 |\n| x\n', repaired: '| a |\n| -\u00a0 |\n| x\n', end: 'table'}, 'existing JNI NBSP delimiter'],
  [{text: '| a |\n| -\u0085 |\n| x', repaired: '| a |\n| -\u0085 |\n| x', end: 'text'}, 'existing JNI NEL delimiter'],
  [{text: '| a |\n| -\u2028 |\n| x', repaired: '| a |\n| -\u2028 |\n| x', end: 'text'}, 'existing JNI line-separator delimiter'],
  [{text: '| a |\n| -\u2029 |\n| x', repaired: '| a |\n| -\u2029 |\n| x', end: 'text'}, 'Apple-source paragraph-separator boundary'],
];

async function withHost(context, run) {
  const host = startCore();
  context.after(() => host.kill(), {timeout: 1_000});
  try {
    await run(host);
  } finally {
    try {
      await host.end();
    } finally {
      host.kill();
    }
  }
}
const options = {timeout: 10_000};

async function replay(host, cases, label) {
  for (const [{text, ...expected}, source] of cases) {
    assert.deepEqual(Object.keys(expected).sort(), ['end', 'repaired'], `${label}: complete expectation shape`);
    const actual = await host.projection('stream_repair', {text});
    assert.deepEqual(actual, expected, `${label}: ${JSON.stringify(text)} (${source})`);
  }
}

function wrongObject(actual, mutation) {
  const wrong = structuredClone(actual);
  mutation(wrong);
  assert.notDeepEqual(wrong, actual, 'the negative control must differ before the comparison');
  assert.throws(() => assert.deepEqual(actual, wrong), assert.AssertionError);
}

test('ArkTS host replays every fence expectation as a complete object', options, async (context) => {
  await withHost(context, async (host) => {
    assert.equal(fence.length, 22);
    await replay(host, fence, 'fence');
    console.log(`  stream_repair fence host replay: ${fence.length} complete objects`);
  });
});

test('ArkTS host replays every marker and boundary expectation as a complete object', options, async (context) => {
  await withHost(context, async (host) => {
    assert.equal(marker.length, 19);
    await replay(host, marker, 'marker');
    console.log(`  stream_repair marker host replay: ${marker.length} complete objects`);
  });
});

test('ArkTS host replays every table and delimiter expectation as a complete object', options, async (context) => {
  await withHost(context, async (host) => {
    assert.equal(table.length, 39);
    await replay(host, table, 'table');
    console.log(`  stream_repair table host replay: ${table.length} complete objects`);
  });
});

test('ArkTS host rejects malformed inputs and wrong end, repaired and extra-field expectations', options, async (context) => {
  await withHost(context, async (host) => {
    await assert.rejects(() => host.projection('stream_repair', {}), /stream_repair: input does not decode/);
    await assert.rejects(() => host.projection('stream_repair', {text: 1}), /stream_repair: input does not decode/);
    for (const text of ['text\n```ts\nconst a', '| a | b |\n| -', '正文\n##']) {
      const actual = await host.projection('stream_repair', {text});
      wrongObject(actual, (wrong) => { wrong.end = 'not-an-end'; });
      wrongObject(actual, (wrong) => { wrong.repaired += '\nwrong'; });
      wrongObject(actual, (wrong) => { wrong.extra = true; });
    }
  });
});
