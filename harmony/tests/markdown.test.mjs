import assert from 'node:assert/strict';
import test from 'node:test';
import { agentReplyBodySize, markdownProseRuns, markdownVisibleText, parseMarkdown, recapGlyphSize } from '../entry/src/main/ets/core/Markdown.ts';

test('Agent reply prose follows the reading metric at each Dynamic Type category and between categories', () => {
  const bodySizes = [14,15,16,17,19,21,23,28,33,40,47,53];
  const markdownSizes = [15,15,16,17,19,20,22,26,31,37,43,48];
  for (const [index,size] of bodySizes.entries()) {
    assert.ok(Math.abs(agentReplyBodySize(size/17) - markdownSizes[index]) < 0.001);
  }
  assert.equal(agentReplyBodySize(3.2),48);
  assert.equal(agentReplyBodySize(3.2,true),45.293);
  assert.ok(agentReplyBodySize(2)>31 && agentReplyBodySize(2)<37);
});

test('recap glyphs grow continuously in Latin and CJK at the native reading sizes', () => {
  for (const [han, regular, largest] of [[true, 16.4, 43.6], [false, 17, 42]]) {
    assert.equal(recapGlyphSize(17, han), regular);
    assert.equal(recapGlyphSize(48, han), largest);
    assert.ok(Math.abs(recapGlyphSize(32.5, han) - (regular + largest) / 2) < 0.001);
    assert.ok(recapGlyphSize(14, han) < regular);
  }
});

test('only Han and full-width punctuation use the narrower CJK fallback advance in reading prose', () => {
  const source = 'PR #42 看完了：max-age，名\u{e0100}字。 👩🏽‍💻 café';
  const runs = markdownProseRuns(source);
  assert.equal(runs.map(run => run.text).join(''),source);
  assert.deepEqual(runs.filter(run => run.han).map(run => run.text),['看完了：','，名\u{e0100}字。']);
});

function linkUrls(blocks, result = []) {
  for (const block of blocks) {
    for (const node of block.inlines) {
      if (node.kind === 'link') result.push(node.url);
      linkUrlsFromInline(node, result);
    }
    linkUrls(block.children, result);
    for (const item of block.items) linkUrls(item.children, result);
    for (const row of block.rows) {
      for (const cell of row.cells) {
        for (const node of cell) {
          if (node.kind === 'link') result.push(node.url);
          linkUrlsFromInline(node, result);
        }
      }
    }
  }
  return result;
}

function linkUrlsFromInline(node, result) {
  for (const child of node.children) {
    if (child.kind === 'link') result.push(child.url);
    linkUrlsFromInline(child, result);
  }
}

test('parses chat Markdown into readable semantic blocks without losing CJK text', () => {
  const blocks = parseMarkdown(
    '# 结果 Results\n\n这是 **粗体** 和 _斜体_，还有 `片段`。\n\n- 第一项\n- 二つ目\n\n> 引用内容\n\n---\n\n| 项目 | 值 |\n| --- | :---: |\n| 结果 | 完了 |',
  );

  assert.deepEqual(blocks.map((block) => block.kind), ['heading', 'paragraph', 'list', 'quote', 'rule', 'table']);
  assert.deepEqual(blocks[1].inlines.map((node) => node.kind), ['text', 'bold', 'text', 'italic', 'text', 'code', 'text']);
  assert.equal(blocks[2].items.length, 2);
  assert.equal(blocks[3].children[0].kind, 'paragraph');
  assert.equal(blocks[5].rows.length, 2);
  assert.equal(blocks[5].rows[0].header, true);
  const visible = markdownVisibleText(blocks);
  for (const value of ['结果 Results', '粗体', '斜体', '片段', '第一项', '二つ目', '引用内容', '项目', '完了']) {
    assert.ok(visible.includes(value), `missing visible text: ${value}`);
  }

  const oneColumn = parseMarkdown('| 状态 |\n| --- |\n| 完了 |');
  assert.equal(oneColumn[0].kind, 'table');
  assert.equal(oneColumn[0].rows.length, 2);
});

test('keeps fenced code verbatim and labels its language', () => {
  const code = 'const 你好 = "**still code**";\nconsole.log(你好);';
  const blocks = parseMarkdown('```ts\n' + code + '\n```');
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, 'code');
  assert.equal(blocks[0].language, 'ts');
  assert.equal(blocks[0].text, code + '\n');
  assert.equal(markdownVisibleText(blocks), code + '\n');
});

test('retains ordered list numbers and nests indented list items', () => {
  const blocks = parseMarkdown('3. 开始\n   - 子項目\n4. 完了');
  assert.equal(blocks.length, 1);
  assert.equal(blocks[0].kind, 'list');
  assert.equal(blocks[0].ordered, true);
  assert.equal(blocks[0].items[0].marker, '3.');
  assert.equal(blocks[0].items[1].marker, '4.');
  assert.equal(blocks[0].items[0].children[1].kind, 'list');
  assert.ok(markdownVisibleText(blocks).includes('子項目'));
});

test('preserves malformed fences and unsupported inline syntax literally', () => {
  const unfinishedFence = '```js\nconst value = "still visible";';
  const fence = parseMarkdown(unfinishedFence);
  assert.equal(fence[0].kind, 'raw');
  assert.equal(fence[0].text, unfinishedFence);

  const unsupported = '~~not parsed~~ and *an unfinished emphasis';
  const inline = parseMarkdown(unsupported);
  assert.equal(inline[0].kind, 'paragraph');
  assert.equal(markdownVisibleText(inline), unsupported);
});

test('only explicit HTTP(S) links become clickable and image syntax stays text', () => {
  const source = '[safe](https://example.test/a?q=1) [run](javascript:alert(1)) [data](data:text/html,x) ![image](https://cdn.example.test/p.png) <script>https://evil.test</script>';
  const blocks = parseMarkdown(source);
  assert.deepEqual(linkUrls(blocks), ['https://example.test/a?q=1']);
  const visible = markdownVisibleText(blocks);
  assert.ok(visible.includes('[run](javascript:alert(1))'));
  assert.ok(visible.includes('[data](data:text/html,x)'));
  assert.ok(visible.includes('![image](https://cdn.example.test/p.png)'));
  assert.ok(visible.includes('<script>https://evil.test</script>'));
  assert.ok(visible.includes('safe'));
});

test('keeps bare HTTP(S) links clickable while preserving trailing punctuation', () => {
  const blocks = parseMarkdown('查看 https://example.test/path_(x). 然后结束。');
  assert.deepEqual(linkUrls(blocks), ['https://example.test/path_(x)']);
  assert.ok(markdownVisibleText(blocks).includes('https://example.test/path_(x).'));

  const cjk = parseMarkdown('访问 https://example.test/x。结束');
  assert.deepEqual(linkUrls(cjk), ['https://example.test/x']);
  assert.ok(markdownVisibleText(cjk).includes('https://example.test/x。结束'));
});
