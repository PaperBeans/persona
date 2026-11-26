import test from 'node:test';
import assert from 'node:assert/strict';
import { dictationLocale, dictationTexts } from '../entry/src/main/ets/core/DictationLanguage.ts';

test('automatic dictation reads this conversation’s draft and the owner’s recent messages', () => {
  const items = [
    { kind: 'text', role: 'user', text: 'Please review the updated draft.' },
    { kind: 'text', role: 'assistant', text: '这句话是助手写的。' },
    { kind: 'text', role: 'user', text: '系统通知不能用来判断语言。', wake: true },
    { kind: 'text', role: 'user', text: '另一个会话转给我的消息。', from: 'elsewhere' },
    { kind: 'text', role: 'user', text: '外部导入的会议纪要。', source: 'meeting' },
    { kind: 'text', role: 'user', text: '这里是自动插入的上下文。', injected: true }
  ];
  const text = dictationTexts('我想核对今天的天气', items);
  assert.deepEqual(text, ['我想核对今天的天气', items[0].text]);
  assert.equal(dictationLocale('auto', text, 'en-US', 'ja-JP'), 'zh-CN');
  assert.equal(dictationLocale('auto', dictationTexts('', items), 'zh-CN', 'ja-JP'), 'en-US');
  assert.equal(dictationLocale('auto', ['今日は会議のメモをまとめます'], '', 'zh-CN'), 'ja-JP');
});

test('manual choice precedes a language hint, and short ambiguous text falls back in order', () => {
  assert.equal(dictationLocale('keyboard', ['我想看看今天的天气'], 'zh-CN', 'zh-CN'), 'keyboard');
  assert.equal(dictationLocale('zh-CN', ['Please review the updated draft.'], '', 'en-US'), 'zh-CN');
  assert.equal(dictationLocale('auto', ['PR #42'], 'ja-JP', 'zh-CN'), 'ja-JP');
  assert.equal(dictationLocale('auto', ['OK'], '', 'en-GB'), 'en-GB');
});
