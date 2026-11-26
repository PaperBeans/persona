import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source=path=>readFileSync(new URL(path,import.meta.url),'utf8');
const view=source('../entry/src/main/ets/components/BeautifulSendStatus.ets');
const icons=source('../entry/src/main/ets/design/PhoneOutlineIcon.ets');
const chat=source('../entry/src/main/ets/components/Conversation.ets');

test('Beautiful send feedback uses the shipping glyph and text-sized, clipped compositor sweep',()=>{
  // The filled 24-unit sparkle in BeautifulUI/Support/Glyphs.swift.
  const glyph='M12 2l2.4 7.2L22 12l-7.6 2.8L12 22l-2.4-7.2L2 12l7.6-2.8z';
  assert.ok(icons.includes("name:'beautifulSparkle',width:24,height:24,path:'"+glyph+"'"));
  assert.match(view,/iconScale:13\/24,color:Colors.ai/);assert.match(view,/Row\(\{space:6\}\)/);
  assert.match(view,/\.width\(this.textWidth\*2\)/);assert.match(view,/0\.35.*0\.5.*0\.65/);
  assert.match(view,/this.travel \? 0\.5 : -1\.5/);assert.match(view,/duration:1400,curve:Curve.Linear/);
  assert.match(view,/blendMode\(BlendMode.DST_IN,BlendApplyType.OFFSCREEN\)/);assert.match(view,/no-hide-descendants/);
  assert.doesNotMatch(view,/setInterval|\.onframe\s*=|measureText\(/);
});

test('the reduced and hidden states remove the sweep and use system inks in both appearances',()=>{
  assert.match(view,/this.visible && this.env.foreground && !this.reduced/);
  assert.match(view,/if \(this.moves\(\) && this.textHeight > 0\)/);
  assert.match(view,/onAnimationReduceStateChange\(this.reducedChanged\)/);
  assert.match(view,/offAnimationReduceStateChange\(this.reducedChanged\)/);
  assert.match(view,/fontColor\(\$r\('sys.color.font_secondary'\)\)/);
  assert.match(view,/\$r\('sys.color.font_primary'\)/);assert.match(view,/duration:700,curve:Curve.Linear/);
  assert.match(chat,/if \(this.model.style === 'beautiful'\) BeautifulSendStatus/);
  assert.match(chat,/else ChatSendStatus\(\{phase:this.model.sendFeedbackPhase\(\)/);
  assert.match(view,/if \(this.workHeader\) this.runIcon\(\)/);
});
