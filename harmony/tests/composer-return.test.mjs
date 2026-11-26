import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

const source = readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets', import.meta.url), 'utf8');
const handlers = source.slice(source.indexOf('  private returnActive():'), source.indexOf('  @Builder replyGlow()'));
const codes = { KEYCODE_ENTER: 2054, KEYCODE_NUMPAD_ENTER: 2119 };
const types = { Down: 0, Up: 1 };
function fixture(subtype={id:'InputZhService',language:'chinese',mode:'lower'}) {
  const timers = [];
  const Composer = new Function('KeyType','KeyCode','EnterKeyType','setTimeout','inputMethod', 'return ' + stripTypeScriptTypes('class Composer {\n' + handlers + '\n}'))(
    types,codes,{Send:4},callback => timers.push(callback),{getCurrentInputMethodSubtype:()=>subtype});
  const c = new Composer();
  const writes = [];
  Object.assign(c, { look:{returnSends:true}, environment:{keyboardHeightPx:0}, fieldFocused:true, chooser:{}, inputRevision:0, modifierRevision:0,
    primaryStop:()=>false, primaryEnabled:()=>true, sendCurrent:()=>true, composerAvailable:()=>true,
    model:{draft:'draft',updateDraft(value){this.draft=value;}}, inlineField:{addText(value){c.model.draft+=value;}},
    sendDraft:async()=>{writes.push(c.model.draft);}, returnKeyMode:'' });
  return { c,writes,flush:()=>timers.splice(0).forEach(fn=>fn()) };
}
function key(modifier, type=types.Down) {
  return { keyCode:codes.KEYCODE_ENTER, type, metaKey:0, getModifierKeyState:([name])=> name===modifier };
}

test('the pre-IME observer lets every hardware Return reach the IME, including a composition private to its keyboard', async () => {
  const {c,writes,flush}=fixture();
  assert.equal(c.observeReturnModifiers(key()),false);
  assert.deepEqual(writes,[]);
  c.inputChanged('draft',{value:'ni',offset:5});
  assert.equal(c.observeReturnModifiers(key()),false);
  c.submittedInline(4);
  assert.deepEqual(writes,[]);
  c.inputChanged('draft你',{value:'',offset:-1});
  c.submittedInline(4);
  assert.deepEqual(writes,[]);
  flush();
  c.observeReturnModifiers(key());
  c.submittedInline(4);
  c.submittedInline(4);
  await Promise.resolve();
  assert.deepEqual(writes,['draft你']);
});

test('Shift and Option insert a break, pasted text and an enlarged editor keep their normal behavior', () => {
  for (const modifier of ['Shift','Alt']) {
    const {c,writes}=fixture();
    assert.equal(c.observeReturnModifiers(key(modifier)),true);
    c.submittedInline(4);
    assert.equal(c.model.draft,'draft\n');
    assert.deepEqual(writes,[]);
  }
  const {c,writes}=fixture();
  c.inputChanged('a\nb');
  assert.equal(c.model.draft,'a\nb');
  c.allowNextLineBreak();
  c.inputChanged('a\nb\n');
  assert.equal(c.model.draft,'a\nb\n');
  c.draftEditorOpen=true;
  c.submittedInline(4);
  assert.deepEqual(writes,[]);
});

test('an empty, blocked, inactive or already stopping composer never sends or interrupts on Return', () => {
  for (const override of [{primaryStop:()=>true},{primaryEnabled:()=>false},{fieldFocused:false},{sendCurrent:()=>false},{look:{returnSends:false}}]) {
    const {c,writes}=fixture();Object.assign(c,override);
    c.submittedInline(4);
    assert.deepEqual(writes,[]);
  }
});

test('only the software keyboard may submit while its unobservable candidate panel could be open', async () => {
  const {c,writes,flush}=fixture();
  c.environment.keyboardHeightPx=780;
  c.observeReturnModifiers(key());
  c.submittedInline(4);
  c.observeReturnModifiers(key(undefined,types.Up));
  assert.deepEqual(writes,[]);
  assert.equal(c.model.draft,'draft');
  flush();
  c.inputChanged('draft你',{value:'',offset:-1});
  c.submittedInline(4);
  assert.deepEqual(writes,['draft你']);
});

test('a known direct English input can send with a hardware Return, while unknown subtypes keep their candidates', () => {
  for (const [subtype,expected] of [
    [{id:'InputEnLowerService',language:'english',mode:'lower'},1],
    [{id:'other',language:'english',mode:'lower'},0],
    [{id:'InputEnLowerService',language:'chinese',mode:'lower'},0]
  ]) {
    const {c,writes}=fixture(subtype);
    c.environment.keyboardHeightPx=850;
    c.observeReturnModifiers(key());
    c.submittedInline(4);
    assert.equal(writes.length,expected);
  }
});

test('modified hardware Return leaves a private or exposed composition untouched', () => {
  const {c,writes}=fixture();
  c.environment.keyboardHeightPx=800;
  assert.equal(c.observeReturnModifiers(key('Shift')),false);
  c.submittedInline(4);
  assert.equal(c.model.draft,'draft');
  c.environment.keyboardHeightPx=0;
  c.observeComposition({value:'ni',offset:0});
  assert.equal(c.observeReturnModifiers(key('Alt')),false);
  c.submittedInline(4);
  assert.equal(c.model.draft,'draft');
  assert.deepEqual(writes,[]);
});
