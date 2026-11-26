import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { compactDraftRoom, composerReadingSize, restingComposerSize } from '../entry/src/main/ets/core/ComposerSize.ts';

const source = readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets', import.meta.url), 'utf8');
const tokens = readFileSync(new URL('../entry/src/main/ets/design/Tokens.ets', import.meta.url), 'utf8');
const Tokens = new Function('return (' + stripTypeScriptTypes(tokens.replace('export class', 'class')) + ')')();
function method(name) {
  const match = new RegExp('  private ' + name + '\\(').exec(source);
  assert.ok(match, name);
  const start = match.index, body = source.indexOf('{', start);
  let depth = 1, i = body + 1;
  for (; i < source.length && depth; i++) {
    if (source[i] === '{') depth++;
    else if (source[i] === '}') depth--;
  }
  assert.equal(depth, 0, name);
  return source.slice(start, i);
}
const Composer = new Function('Tokens', 'Edge', 'restingComposerSize', 'composerReadingSize', 'return ' + stripTypeScriptTypes('class Composer { get readingSize() { return composerReadingSize(this.environment.fontScale); } get restingSize() { return restingComposerSize(this.environment.fontScale); }' +
  ['hasComposerAction', 'trailingActionWidth', 'leftActionWidth', 'expandedComposer', 'composerColumns', 'sendColumns',
    'restingComposer', 'composerActionSize', 'restingStrokePx', 'composerPadding', 'controlsPadding', 'composerAvailable',
    'leftColumns', 'inlineKeyboardAction', 'verticalActions', 'maxControlsHeight', 'inlineEditorMaxHeight', 'revealOverflowingActions'].map(method).join('\n') + '}'))(Tokens, { Bottom: 'bottom' }, restingComposerSize, composerReadingSize);
function fixture(density = 3, width = 372) {
  return Object.assign(new Composer(), {
    composerWidth: width, fieldFocused: false, primaryStop: () => false, sending: false, active: true,
    composerStroke: Number(source.match(/private readonly composerStroke: number = ([\d.]+);/)[1]),
    model: { demo: false, draft: '', selected: {kind:'session'}, canCompose: () => true, decisionBlocksMessage: () => true },
    attachments: { attachments: [] }, environment: { fontScale: 1, keyboardHeightPx: 0 }, showContextMenu: true,
    availableHeight: 800, editorHeight: 0, controlsHeight: 80,
    getUIContext: () => ({ vp2px: value => value * density })
  });
}

test('the resting shell uses the next capped iPhone category for controls and both symbol roles', () => {
  // Native UIFontMetrics on the iPhone API: each value is the scaled 17pt metric, in thirds of a point.
  for (const [body, thirds, plus, headline] of [[14,46,20,15], [15,49,21,16], [16,51,22,17], [17,56,24,19],
    [19,60,26,21], [21,67,28,23], [23,79,34,28], [53,79,34,28]]) {
    const v = fixture(); v.environment.fontScale = body / 17;
    assert.ok(Math.abs(v.composerActionSize() - 44 * thirds / 51) < 1e-8);
    assert.equal(v.restingSize.plus, plus);
    assert.equal(v.restingSize.microphone, headline * 24 / 19);
    v.fieldFocused = true;
    assert.equal(v.composerActionSize(), v.restingSize.target);
  }
  const v = fixture(3,275);
  assert.equal(v.expandedComposer(), false);
  v.environment.fontScale = 3.2;
  assert.equal(v.expandedComposer(), true);
  assert.equal(v.composerActionSize(), v.restingSize.target);
});

test('resting touch targets sit 16vp from the sides and 10vp above the native safe bottom at every font scale', () => {
  for (const density of [1, 2, 2.75, 3, 3.125]) for (const scale of [1, 1.5, 3.2]) {
    const v = fixture(density);
    v.environment.fontScale = scale;
    const outside = v.controlsPadding(), inside = v.composerPadding(), stroke = v.restingStrokePx();
    const actual = edge => {
      assert.match(inside[edge], /^\d+px$/);
      return Math.round(outside[edge] * density) + parseInt(inside[edge], 10) + stroke;
    };
    assert.equal(v.restingComposer(), true);
    assert.ok(Math.abs(actual('left') - 16 * density) <= 1);
    assert.ok(Math.abs(actual('right') - 16 * density) <= 1);
    assert.ok(Math.abs(actual('bottom') - 10 * density) <= 1);
    if (density === 3) assert.deepEqual([actual('left'), actual('right'), actual('bottom')], [48, 48, 30]);
  }
});

test('focus, keyboard dismissal, draft, menu, narrow viewport and unavailable input keep the editing or dock insets', () => {
  for (const change of [
    v => { v.fieldFocused = true; }, v => { v.environment.keyboardHeightPx = 600; },
    v => { v.modeMenuOpen = true; }, v => { v.model.draft = 'Review'; },
    v => { v.composerWidth = 220; }, v => { v.active = false; },
    v => { v.model.decision = { id: 'pending' }; }, v => { v.model.canCompose = () => false; },
    v => { v.model.sourceContext = { title: 'Context' }; }, v => { v.attachments.attachments.push({ id: 'file' }); }
  ]) {
    const v = fixture();
    change(v);
    assert.equal(v.restingComposer(), false);
    if (v.expandedComposer()) {
      assert.deepEqual(v.controlsPadding(), {left:12,right:12,top:8,bottom:8});
      assert.deepEqual(v.composerPadding(), {left:'10px',right:'10px',top:0,bottom:'22px'});
    } else {
      assert.deepEqual(v.controlsPadding(), {left:16,right:16,top:8,bottom:4});
      assert.deepEqual(v.composerPadding(), {left:5,right:5,top:3,bottom:3});
    }
  }
});

test('compact Persona and Agent editors get the remaining width after fixed actions and equal gaps', () => {
  const v = fixture();
  assert.equal(v.expandedComposer(), false);
  assert.equal(v.composerColumns(), 1116);
  assert.equal(v.leftColumns(), 169);
  assert.equal(v.sendColumns(), 169);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 778);
  v.showContextMenu = false;
  assert.equal(v.leftColumns(), 169);
  assert.equal(v.composerColumns() - v.leftColumns() - v.sendColumns(), 778);
});

test('compact action gaps stay within one physical pixel at each native device density', () => {
  for (const density of [3, 2.75, 3.125]) for (const width of [372, 587.27, 647.68]) {
    const v = fixture(density, width), unit = width * density / v.composerColumns();
    assert.equal(v.expandedComposer(), false);
    for (const gap of [v.leftColumns() * unit - v.leftActionWidth(v.composerActionSize()) * density,
      v.sendColumns() * unit - v.composerActionSize() * density]) {
      assert.ok(Math.abs(gap - Tokens.gap * density) <= 1.01, { density, width, gap });
    }
  }
});

test('focus, a restored draft, a quote or an attachment opens the editing card independently', () => {
  for (const change of [
    v => { v.fieldFocused = true; },
    v => { v.model.draft = '你好'; },
    v => { v.model.sourceContext = { title: '引用' }; },
    v => { v.attachments.attachments.push({ id: 'file' }); }]) {
    const v = fixture();
    change(v);
    assert.equal(v.expandedComposer(), true);
    assert.equal(v.composerColumns(), v.composerWidth*3);
  }
});

test('a blurred empty composer returns to one line, with enough space for the keyboard and both toolbars', () => {
  const v = fixture();
  v.fieldFocused = true;
  assert.equal(v.expandedComposer(), true);
  v.fieldFocused = false;
  assert.equal(v.expandedComposer(), false);

  // The iPhone keeps the unedited attachment / field / microphone row compact at AX5.
  for (const scale of [1,1.5,3.2]) for (const width of [300,372]) {
    const phone=fixture(3,width); phone.environment.fontScale=scale;
    assert.equal(phone.expandedComposer(),false);
    assert.equal(phone.composerColumns(),width*3);
  }
  const narrow=fixture(3,220);
  assert.equal(narrow.expandedComposer(),true);
  assert.equal(narrow.composerColumns(),narrow.composerWidth*3);

});

test('a draft can expand above the keyboard, while a short unfocused draft keeps its quiet shape', () => {
  const Editor = new Function('Tokens','return '+stripTypeScriptTypes('class Editor {'+
    ['offersDraftEditor','rememberSelection','parkSelection','restoreSelection','draftEditorCurrent'].map(method).join('\n')+'}'))(Tokens);
  const v = Object.assign(new Editor(), {fieldFocused:false, model:{draft:'Review PR 42'}, composerWidth:370, unreservedDraftLines:1,
    environment:{fontScale:1}, draftEditorOwner:'hubA/review', sendCurrent:()=>true,composerAvailable:()=>true,
    composerOwnerKey:()=>'hubA/review'});
  assert.equal(v.offersDraftEditor(),false);v.fieldFocused=true;assert.equal(v.offersDraftEditor(),true);
  v.fieldFocused=false;v.model.draft='a\nb';assert.equal(v.offersDraftEditor(),true);
  v.model.draft='wrapped';v.unreservedDraftLines=6;assert.equal(v.offersDraftEditor(),true);
  v.rememberSelection(1,6);v.parkSelection();v.rememberSelection(0,0);
  v.model.draft='abc';let restored;
  v.restoreSelection({setTextSelection:(...range)=>{restored=range;}});
  assert.deepEqual(restored,[1,3]);assert.equal(v.draftEditorCurrent(),true);
  v.composerOwnerKey=()=>'hubB/review';assert.equal(v.draftEditorCurrent(),false);
});

test('resized phone viewport leaves a full keyboard and send target at both text sizes', () => {
  for (const scale of [1, 3.2]) for (const width of [242, 356, 372, 620]) {
    const v = fixture(3, width);
    v.fieldFocused = true;
    v.model.draft = 'Review PR 42\n'.repeat(20);
    v.environment.fontScale = scale;
    v.availableHeight = 270;
    v.environment.keyboardHeightPx = 1054;
    assert.equal(v.maxControlsHeight(), 270); // ArkUI has already removed the keyboard.
    assert.equal(v.verticalActions(), width < 260);
    const actionRows = v.verticalActions() ? 2 : 1;
    const reserved = actionRows * v.composerActionSize() + Tokens.gap + Tokens.footer + 8;
    const height = v.inlineEditorMaxHeight();
    assert.ok(height + reserved <= v.availableHeight);
    assert.ok([1,2,3,4,5,6].some(lines => v.readingSize.height(lines) === height));
    if (!v.verticalActions()) {
      const middle = (v.composerColumns() - v.leftColumns() - v.sendColumns()) * width / v.composerColumns();
      assert.ok(middle >= Tokens.page, 'native leading and trailing controls fit at their full size');
    }
    v.environment.keyboardHeightPx = 0;
    v.availableHeight = 690;
    assert.equal(v.maxControlsHeight(), 552);
    assert.ok(v.inlineEditorMaxHeight() >= height);
    assert.equal(v.model.draft, 'Review PR 42\n'.repeat(20));
  }
});

test('three scrollable Agent requests yield room to the active editor', () => {
  const v=fixture();
  Object.assign(v,{fieldFocused:true,availableHeight:460,agentComposerHeight:130,controlsHeight:415,editorHeight:64});
  Object.assign(v.model,{selected:{kind:'session'},decision:{kind:'question'}});
  Object.assign(v.environment,{fontScale:3.2,keyboardHeightPx:1000});
  assert.ok(v.inlineEditorMaxHeight()>=v.readingSize.height(2));
});

test('quotes, attachments and the dock reserve their measured space before growing the editor', () => {
  const v = fixture();
  Object.assign(v, { fieldFocused: true, availableHeight: 300, editorHeight: 170, controlsHeight: 310 });
  Object.assign(v.environment, { fontScale: 3.2, keyboardHeightPx: 1024 });
  assert.equal(v.inlineEditorMaxHeight(), v.readingSize.height(2));
  v.availableHeight = 125;
  assert.equal(v.inlineEditorMaxHeight(), v.readingSize.height(1));
  let scrolls = 0;
  v.controlsScroller = { scrollEdge(side) { assert.equal(side, 'bottom'); scrolls++; } };
  v.active = true;
  v.revealOverflowingActions();
  assert.equal(scrolls, 1);
  v.draftEditorOpen = true;
  v.revealOverflowingActions();
  v.draftEditorOpen = false;
  v.environment.keyboardHeightPx = 0;
  v.revealOverflowingActions();
  assert.equal(scrolls, 1);
});

test('opening and collapsing the page editor restores its edited draft and selection without sending', () => {
  const Page = new Function('ConversationFrame', 'return ' + stripTypeScriptTypes('class Page {' +
    ['draftEditorCurrent', 'rememberSelection', 'parkSelection', 'restoreSelection', 'expandDraftEditor',
      'focusDraftPage', 'finishPageInput', 'draftPageGone'].map(method).join('\n') + '}'))(class {
    constructor(action) { this.onFrame = action; }
  });
  const focused = [], selected = [];
  const v = Object.assign(new Page(), {
    fieldFocused: true, composerOwnerKey: () => 'hub/review42', sendCurrent: () => true, composerAvailable: () => true,
    model: { draft: 'Review PR 42' }, sendDraft: () => assert.fail('editing must never submit'),
    inlineField: { stopEditing() {}, setTextSelection: (...range) => selected.push(range) },
    pageField: { stopEditing() {}, setTextSelection: (...range) => selected.push(range) },
    getUIContext: () => ({ postFrameCallback: frame => frame.onFrame(),
      getFocusController: () => ({ requestFocus: id => focused.push(id) }) })
  });
  v.rememberSelection(2, 5);
  v.expandDraftEditor();
  v.focusDraftPage();
  v.model.draft = '核对完成\n还可以编辑';
  v.rememberSelection(4, 6);
  v.finishPageInput();
  v.draftEditorOpen = false;
  v.draftPageGone();
  assert.equal(v.model.draft, '核对完成\n还可以编辑');
  assert.deepEqual(focused, ['chat.editor.input', 'chat.input']);
  assert.deepEqual(selected, [[2, 5], [4, 6]]);
});

test('pending Agent cards derive the half page from the compact portrait window and keep the live keyboard and larger native insets clear', () => {
  let width=402,height=874,mode=1,insetTop=39,insetBottom=28,origin=0;
  const win={WindowStatusType:{FULL_SCREEN:1},findWindow:()=>({getWindowStatus:()=>mode,
    getWindowProperties:()=>({windowRect:{width:width*3,height:height*3,top:origin*3}})})};
  const Page=new Function('window','Tokens','return '+stripTypeScriptTypes('class Page {'+method('agentCardsRoom')+'}'))(win,Tokens);
  const view=Object.assign(new Page(),{getUIContext:()=>({px2vp:x=>x/3,getWindowName:()=>'main'}),
    availableHeight:741,availableWidth:402,contentTop:133,agentComposerHeight:68.33,agentCountHeight:15.33,
    restingSize:{target:48.3},model:{pendingDecisions:[{},{}],decision:{kind:'permission'}},environment:{}});
  const insets=()=>Object.assign(view.environment,{safeTopPx:insetTop*3,safeBottomPx:(origin+height-insetBottom)*3,keyboardHeightPx:0,nativeHoverAvailable:false});
  insets();assert.equal(view.agentCardsRoom(),389);
  height=974;view.availableHeight+=100;insets();assert.equal(view.agentCardsRoom(),439);
  height=874;view.availableHeight=741;insetTop=92;insetBottom=46;insets();assert.equal(view.agentCardsRoom(),368);
  insetTop=39;insetBottom=28;insets();view.availableHeight=404;view.contentTop=156;view.agentComposerHeight=115.33;view.environment.keyboardHeightPx=314*3;
  assert.ok(Math.abs(view.agentCardsRoom()-248.34)<.02);
  view.agentComposerHeight=223;assert.ok(view.agentCardsRoom()<150);
  view.availableHeight=741;view.contentTop=133;view.agentComposerHeight=68.33;insets();view.environment.nativeHoverAvailable=true;
  assert.equal(view.agentCardsRoom(),(846-133)/2);
  insets();mode=5;assert.equal(view.agentCardsRoom(),(846-133)/2);
  mode=1;width=874;height=402;insetTop=20;insetBottom=15;view.contentTop=90;view.availableWidth=874;view.availableHeight=312;insets();
  assert.equal(view.agentCardsRoom(),(402-15-90)/2);
  view.contentTop=354;view.availableHeight=48;view.agentComposerHeight=90;assert.equal(view.agentCardsRoom(),0);
});

test('draft wrapping uses the full text width and measures before reserving the corner button', () => {
  const methods = ['offersDraftEditor','expandedFieldHeight','nativeDraftLines'].map(method).join('\n')+
    source.match(/@Computed (get unreservedDraftLines\(\): number \{[^}]+\})/)[1]+
    source.match(/@Computed (get visibleDraftLines\(\): number \{[\s\S]*?\n  \})/)[1];
  const Editor = new Function('Tokens', 'WordBreak', 'return '+stripTypeScriptTypes('class Editor {'+methods+'}'))(Tokens,{BREAK_WORD:'break-word'});
  const calls = [];
  const state = Object.assign(new Editor(), {composerWidth:370, model:{draft:'Review note 42'}, fieldFocused:false,
    restingSize:{target:68}, readingSize:composerReadingSize(3.2), draftTracking:-1, inlineEditorMaxHeight:()=>380,
    getUIContext:()=>({px2vp:px=>px/3, getMeasureUtils:()=>({measureTextSize:o=>{
      calls.push(o);
      return {height:3*state.readingSize.line*(Number.parseFloat(o.constraintWidth)<300?2:1)};
    }})})});
  assert.equal(state.expandedFieldHeight(),64);
  assert.equal(calls.at(-1).constraintWidth,'346vp');
  state.fieldFocused=true;
  assert.equal(state.expandedFieldHeight(),126);
  assert.equal(calls.at(-1).constraintWidth,'278vp');
  state.model.draft='核对照片\n';state.fieldFocused=false;
  assert.equal(state.offersDraftEditor(),true);
  state.expandedFieldHeight();assert.equal(calls.at(-1).textContent,'核对照片\n ');
  assert.equal(calls.at(-1).maxLines,7);
  assert.equal(composerReadingSize(1).height(1),23);
  assert.ok(composerReadingSize(1.63).points>composerReadingSize(1.4).points);
});

test('expanded native toolbar places its scaled controls between the two eight-unit card insets', () => {
  for (const scale of [1,1.5,3.2]) {
    const v=fixture(3,370);v.fieldFocused=true;v.model.draft='draft';v.environment.fontScale=scale;
    const size=v.restingSize.target, unit=v.composerWidth/v.composerColumns();
    assert.ok(Math.abs(v.leftColumns()*unit-(4+size+4+size))<=1/3);
    assert.ok(Math.abs(v.sendColumns()*unit-(4+size+4+size))<=1/3);
    assert.equal(v.verticalActions(),false);
    v.fieldFocused=false;assert.ok(Math.abs(v.leftColumns()*unit-(4+size))<=1/3);
  }
});

test('full-height shared footer reserves a half-page of requests or one accessible card above the genuine keyboard', () => {
  const light=compactDraftRoom(402,874,402,39,28,0,true,false);
  const ime=compactDraftRoom(402,874,402,39,28,314,true,false);
  assert.deepEqual([light.page,light.visible,light.bottomLift],[778,778,6]);
  assert.deepEqual([ime.page,ime.visible,ime.bottomLift],[778,483,15]);
  const Frame=new Function('Tokens','return '+stripTypeScriptTypes('class Frame {'+
    ['preferredCardSpace','sharedDraftLimit'].map(method).join('\n')+'}'))(Tokens);
  const v=Object.assign(new Frame(),{environment:{keyboardHeightPx:0},agentCountHeight:23,composerActionSize:()=>68,readingSize:composerReadingSize(3.2)});
  assert.equal(v.preferredCardSpace(light),389);assert.equal(v.sharedDraftLimit(light),245);
  v.environment.keyboardHeightPx=942;
  assert.equal(v.preferredCardSpace(ime),44);assert.equal(v.sharedDraftLimit(ime),295);
  assert.equal(compactDraftRoom(402,500,402,39,28,180,true,false),undefined);
  assert.equal(compactDraftRoom(402,874,300,39,28,200,true,false),undefined);
  assert.equal(compactDraftRoom(402,874,402,39,28,314,false,false),undefined);
  assert.equal(compactDraftRoom(402,874,402,39,28,314,true,true),undefined);
  assert.equal(compactDraftRoom(402,874,402,70,44,410,true,false).bottomLift,0);
  assert.equal(compactDraftRoom(402,874,402,39,28,150,true,false).bottomLift,0);
  assert.ok(Math.abs(composerReadingSize(1).height(6)-130.3)<.02);
});

test('the full draft uses capped navigation controls, shell-sized send and the native sheet safe area', () => {
  const source=readFileSync(new URL('../entry/src/main/ets/design/PhoneStyle.ets',import.meta.url),'utf8');
  const styleClass=source.slice(source.indexOf('export class PhoneStyle'),source.indexOf('@ComponentV2')).replace('export class','class');
  const style=new Function('Tokens','return ('+stripTypeScriptTypes(styleClass)+')')(Tokens);
  const Page=new Function('PhoneStyle','SheetSize','Tokens','return '+stripTypeScriptTypes('class Page {'+
    ['pageToolbarSize','pageSendSize','pageSheetHeight','measurePageSheet'].map(method).join('\n')+'}'))(style,{LARGE:'large'},Tokens);
  for (const [scale,top] of [[1,44],[3.2,57.7]]) {
    const page=Object.assign(new Page(),{compactDraftSpace:{},environment:{fontScale:scale,windowHeightPx:2622,safeTopPx:117},restingSize:restingComposerSize(scale),getUIContext:()=>({px2vp:v=>v/3,vp2px:v=>v*3})});
    assert.equal(page.pageToolbarSize(),top);assert.equal(page.pageSendSize(),Math.round(restingComposerSize(scale).target*3)/3);
    assert.equal(page.pageSheetHeight(),812);page.environment.safeTopPx=216;assert.equal(page.pageSheetHeight(),794);
    page.compactDraftSpace=undefined;assert.equal(page.pageSheetHeight(),'large');assert.equal(page.pageToolbarSize(),44);assert.equal(page.pageSendSize(),44);
  }
});

test('empty chat prompts use one fitted shell line while the editable draft keeps its reading size', () => {
  const field = source.slice(source.indexOf('TextArea({ text: this.model.draft, controller:this.inlineField'),
    source.indexOf("}.width('100%').constraintSize", source.indexOf(".id('chat.input')")));
  assert.match(field, /placeholder:this\.composerPlaceholder\(\)/);
  assert.match(field, /placeholderColor\(Color\.Transparent\)/);
  assert.match(field, /fontSize\(this\.expandedComposer\(\) \? String\(this\.readingSize\.points\)/);
  const prompt = field.slice(field.indexOf('if (!this.model.draft)'));
  assert.match(prompt, /Text\(this\.composerPlaceholder\(\)\).*maxLines\(1\)/);
  assert.match(prompt, /minFontSize\(String\(this\.restingSize\.promptPoints \/ 2\)/);
  assert.match(prompt, /maxFontSize\(String\(this\.restingSize\.promptPoints\)/);
  assert.match(prompt, /hitTestBehavior\(HitTestMode\.None\)\.accessibilityLevel\('no'\)/);
  assert.equal(restingComposerSize(1).promptPoints, 56 / 3);
  assert.equal(restingComposerSize(3.2).promptPoints, 79 / 3);
  assert.equal(composerReadingSize(3.2).points, 53);
});
