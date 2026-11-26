import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import {readFileSync} from 'node:fs';
registerHooks({load(url,context,next){
  if(url.endsWith('.ets'))return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''))};
  return next(url,context);
}});
const {knowledgePriorityTone,knowledgeRelevanceTone,knowledgeInk,knowledgeWash,KnowledgeLandingMenu}=await import('../entry/src/main/ets/models/KnowledgeListPresentation.ets');
const {relativeActivityDate,phoneDueDate,phoneTodoPlanDate}=await import('../entry/src/main/ets/core/PhoneDate.ts');
test('compact to-do plan dates retain localized weekdays, 24-hour Chinese times and all-day dates',()=>{
  const at=new Date(2026,10,23,18,5).getTime();
  assert.equal(phoneTodoPlanDate(at,at,'zh-CN',true),'11月23日 周一 18:05');
  assert.equal(phoneTodoPlanDate(at,at,'zh-CN',false),'11月23日 周一');
  assert.equal(phoneTodoPlanDate(at,new Date(2027,0,1).getTime(),'zh-CN',false),'2026年11月23日 周一');
  assert.equal(phoneTodoPlanDate(at,at,'en-US',true),phoneDueDate(at,at,'en-US',true));
  assert.equal(phoneTodoPlanDate(Number.NaN,at,'zh-CN',true),'');
});
test('compact to-do timestamps honor an explicit clock and the iPhone Simplified Chinese default after the minute range',()=>{
  const at=new Date(2026,10,23,9,57).getTime();
  const say=(age,locale,style)=>relativeActivityDate(at,at+age*60000,locale,'now','{n} min','yesterday',style);
  assert.equal(say(59,'zh-Hans','locale'),'59 min');
  assert.equal(say(60,'zh-Hans','locale'),'09:57');
  assert.equal(say(65,'zh-CN','24'),'09:57');
  assert.equal(say(65,'en-US','12'),new Date(at).toLocaleTimeString('en-US',{hour:'numeric',minute:'2-digit',hour12:true}));
  assert.equal(say(65,'zh-TW','locale'),new Date(at).toLocaleTimeString('zh-TW',{hour:'numeric',minute:'2-digit'}));
  assert.equal(say(1440,'zh-CN','locale'),'yesterday');
});
test('activity dates match the iPhone calendar-day, weekday, and relative-minute boundaries in the active locale',()=>{
  const now=new Date(2026,10,22,12,0).getTime(), local=(day,hour,minute=0)=>new Date(2026,10,day,hour,minute).toISOString();
  const say=(raw,locale='zh-CN')=>relativeActivityDate(Date.parse(raw),now,locale,'刚刚','{n} 分钟前','昨天');
  assert.equal(say(new Date(now-44000).toISOString()),'刚刚');
  assert.equal(say(new Date(now-45000).toISOString()),'1 分钟前');
  assert.equal(say(local(22,11,35)),'25 分钟前');assert.match(say(local(22,8,30)),/8:30/);
  assert.equal(say(local(21,23,30)),'昨天');assert.equal(say(local(18,9)),'星期三');assert.equal(say(local(18,9),'en-US'),'Wednesday');
  assert.equal(say(local(7,8)),new Date(local(7,8)).toLocaleDateString('zh-CN',{month:'short',day:'numeric'}));
  assert.equal(say('invalid'),'');assert.equal(say('1970-01-01T00:00:00Z'),'');
});
test('timed and all-day deadlines name the local weekday and show the year only when it changes',()=>{
  const when = new Date(2026,10,22,18,30).getTime();
  const day = phoneDueDate(when,when,'en-US',false);
  const timed = phoneDueDate(when,when,'en-US',true);
  assert.match(day,/Sun/); assert.doesNotMatch(day,/2026|18:30|6:30/);
  assert.match(timed,/6:30/);assert.match(phoneDueDate(when,new Date(2027,0).getTime(),'en-US',false),/2026/);
  assert.equal(phoneDueDate(NaN,when,'en-US',true),'');
});
test('priority and relevance have separate tone ranges and the same swatch remains legible on both standard appearances',()=>{
  assert.deepEqual(['P0','P1','P2','P3',''].map(knowledgePriorityTone),['attention','working','blue','secondary','secondary']);
  assert.deepEqual([100,70,69,40,39,0].map(knowledgeRelevanceTone),['attention','attention','working','working','neutral','neutral']);
  assert.equal(knowledgeInk('attention',false),'#007AFF');assert.equal(knowledgeInk('working',true),'#FFB224');
  assert.notEqual(knowledgeInk('attention',false),knowledgeInk('blue',false));assert.notEqual(knowledgeWash('neutral',true),knowledgeWash('neutral',false));
});
test('the top bar uses only its actual visible library, validates choices, and retires callbacks after navigation',()=>{
  let current=true,opened=0;const tags=[];
  const workspace={tags:['one','two'],tag:'two',query:'',setTag:x=>tags.push(x)}, other={tags:[],tag:'',query:'',setTag:()=>assert.fail()};
  const menu=new KnowledgeLandingMenu();menu.bind(workspace,()=>current,()=>opened++);
  menu.choose('one');menu.choose('unknown');workspace.query='search';menu.choose('two');workspace.query='';menu.choose('');menu.map();
  assert.deepEqual(tags,['one','']);assert.equal(opened,1);
  menu.clear(other);current=false;menu.choose('one');menu.map();assert.equal(opened,1);
  menu.clear(workspace);assert.deepEqual(menu.tags,[]);assert.deepEqual(menu.selected,'');menu.map();assert.equal(opened,1);
});

test('only an up-to-date menu may set a tag during an active global search',()=>{
  const applied=[];
  const workspace={tags:['first','second'],tag:'',query:'',setTag:tag=>applied.push(tag)};
  const menu=new KnowledgeLandingMenu();
  menu.bind(workspace,()=>true,()=>{});
  workspace.query='weather';
  menu.choose('first');
  menu.bind(workspace,()=>true,()=>{});
  menu.choose('second');
  workspace.tags=['first','new'];
  menu.choose('second');
  menu.choose('new');
  assert.deepEqual(applied,['second']);
});
