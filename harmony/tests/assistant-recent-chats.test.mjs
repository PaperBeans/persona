import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier,context,next) {
    if (specifier === './Wire' && context.parentURL?.endsWith('AssistantRecentChats.ets'))
      return {url:new URL('./Wire.ets',context.parentURL).href,shortCircuit:true};
    if (specifier === '../core/PhoneDate' && context.parentURL?.endsWith('AssistantRecentChats.ets'))
      return {url:new URL('../core/PhoneDate.ts',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*|@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {allAssistantChatGroups,allChatDetail,assistantChatTitleParts,assistantChatSource,assistantSidebarChats,sidebarConversationLabel,ScheduledChatOrigins,SidebarTaskHistory}=await import('../entry/src/main/ets/models/AssistantRecentChats.ets');
const chat=(id,extra={})=>ResourceRow.from({session_id:id,machine_id:'computer',manager:'assistant',title:id,...extra},'conversation');
const ids=rows=>rows.map(row=>row.id);

test('Recent separates the main chat and directions from scheduled chats and keeps other recorded origins',()=>{
  const rows=[chat('home'),chat('lead-b'),chat('run'),chat('mine'),chat('lead-a'),chat('background'),
    chat('channel',{origin:'channel'}),chat('inbox',{origin:'inbox'}),chat('scheduled',{origin:'task'}),chat('archived',{archived:true}),chat('foreign',{manager:'other'})];
  const history=new ScheduledChatOrigins([{title:'Routine',session_id:'mine',run_session_id:'run'}],[{session_id:'mine'}]);
  const prefs={leaders:['lead-a','home','lead-b','missing'],background_supervisor:'background'};
  const off=assistantSidebarChats(rows,'home','computer',prefs,history,'group','assistant');
  assert.deepEqual(ids(off.recent),['channel','inbox','mine']);
  assert.deepEqual(ids(off.directions),['lead-a','lead-b']);
  const on=assistantSidebarChats(rows,'home','computer',{...prefs,recent_task_chats:true},history,'group','assistant');
  assert.deepEqual(ids(on.recent),['background','channel','inbox','mine','run']);
  const both=[chat('scheduled',{origin:'task'}),chat('mine')];
  assert.deepEqual(ids(assistantSidebarChats(both,'','',{recent_task_chats:true},history,'group','assistant').recent),['mine','scheduled']);
});

test('older scheduled runs require a matching task title and preserve conversations a task came from',()=>{
  const h=new ScheduledChatOrigins([{session_id:'original',title:'  Weekly review  ',run_session_id:'dedicated',last_session_id:'last'}],[{session_id:'original'},{session_id:'history'}]);
  for (const id of ['dedicated','last','history']) assert.equal(h.contains(chat(id)),true);
  assert.equal(h.contains(chat('original')),false);
  assert.equal(h.contains(chat('old',{title:' 0123 | 定时 | Weekly'})),true);
  assert.equal(h.contains(chat('mine',{title:'0123 | 定时 | Another topic'})),false);
  assert.equal(h.contains(chat('mine',{title:'å123 | 定时 | Weekly'})),false);
  assert.equal(h.contains(chat('mine',{title:'123 | 定时 | Weekly'})),false);
});

test('Recent takes five newest, preserves a same-id conversation on another computer and sorts equal times by id',()=>{
  const rows=[chat('z'),chat('a'),chat('b'),chat('old'),chat('x',{updated_at:'2026-08-01T01:02:03.000100Z'}),
    chat('y',{updated_at:'2026-08-01T01:02:03.000800Z'}),chat('home',{machine_id:'second',updated_at:'2026-12-01T00:00:00Z'}),chat('home')];
  assert.deepEqual(ids(assistantSidebarChats(rows,'home','computer',{},new ScheduledChatOrigins([],[]),'group','assistant').recent),['home','y','x','a','b']);
});

test('a drawer read is shared while visible and cannot apply to a retired assistant, and failures retain its last index',async()=>{
  const history=new SidebarTaskHistory();let scope='first',generation=1,blocked=true,fail=false;const order=[],gate=Promise.withResolvers();
  const repo={async call(path){order.push(path);if(blocked) await gate.promise;if(fail)throw Error('offline'); return path.endsWith('/tasks')?{tasks:[{last_session_id:'run'}]}:{runs:[]};}};
  const owner={scope:()=>scope,connectionGeneration:()=>generation,owns:(gen,key)=>gen===generation&&key===scope,repo:()=>repo,conversations:[chat('open')],connected:true};
  const first=history.refresh(owner,true);await history.refresh(owner,true);assert.equal(order.length,2);
  scope='second';generation++;blocked=false;await history.refresh(owner,true);gate.resolve();await first;
  assert.equal(history.known('first').contains(chat('run')),false);assert.equal(history.known('second').contains(chat('run')),true);
  await history.refresh(owner,false);fail=true;await history.refresh(owner,true);assert.equal(history.known('second').contains(chat('run')),true);
});

function strings(locale) {
  const path=new URL(`../entry/src/main/resources/${locale}/element/string.json`,import.meta.url);
  const values=new Map(JSON.parse(readFileSync(path)).string.map(item=>[item.name,item.value]));
  return key=>{assert.ok(values.has(key),key);return values.get(key);};
}

test('a screen reader hears the native drawer title, localized activity and last message in a single action',()=>{
  const now=new Date(2026,10,23,2).getTime(),at=new Date(2026,10,21,22).toISOString();
  const my=chat('mine',{title:'帮我约周五和韩梅梅吃饭',state:'idle',updated_at:at,last_text:'已经在日历里加好了。'});
  const history=new ScheduledChatOrigins([],[]);
  assert.equal(sidebarConversationLabel(my,history,'zh-CN',now,strings('zh_CN')),'帮我约周五和韩梅梅吃饭，星期六，已经在日历里加好了。');
  const old=chat('old',{title:'Claude Code',status:'blocked',last_text:'# **Review**\n> `notes`',updated_at:at});
  assert.equal(sidebarConversationLabel(old,history,'en-US',now,strings('en_US')),'New Chat, Needs You, Review notes');
  const sparse=chat('empty',{title:'',state:'idle',updated_at:'bad'});
  assert.equal(sidebarConversationLabel(sparse,history,'ja-JP',now,strings('ja_JP')),'新しいチャット');
});

test('automatic conversations announce the local source and active states take priority over their timestamp',()=>{
  const now=Date.now(),activity={updated_at:new Date(now).toISOString(),last_text:'**OK**'};
  const history=new ScheduledChatOrigins([{last_session_id:'run'}],[]);
  const words=strings('zh_CN');
  for (const [origin,source] of [['task','定时任务'],['inbox','收件箱'],['spawned','对话派生'],['extension','来自扩展'],['lark_agent','来自 飞书 /agent'],['other','其他']]) {
    const row=chat('s',{...activity,title:'标题',state:'unread',origin});
    assert.equal(sidebarConversationLabel(row,history,'zh-CN',now,words),`标题，${source}，完成未看，OK`);
  }
  const task=chat('run',{...activity,title:'作業',origin:'user',state:'working'});
  assert.equal(sidebarConversationLabel(task,history,'ja-JP',now,strings('ja_JP')),'作業、定期タスク、作業中、OK');
});


test('All Chats filters the full owned list by source, preserves scheduled runs, and excludes the main, archived and foreign rows', () => {
  const history = new ScheduledChatOrigins([{session_id:'mine',run_session_id:'legacy-task',title:'Release'}],[]);
  const stamp='2026-11-22T10:00:00.000Z';
  const rows=[chat('main',{origin:'user'}),chat('inbox',{origin:'inbox',state:'needs_you',last_text:'an unrelated preview',updated_at:stamp}),
    chat('mine',{origin:'channel',updated_at:stamp}),chat('scheduled',{origin:'task',updated_at:stamp}),
    chat('legacy-task',{origin:'user'}),chat('other',{origin:'future-integration'}),
    chat('lark',{origin:'lark_agent'}),chat('spawned',{origin:'spawned'}),chat('ext',{origin:'extension'}),
    chat('foreign',{manager:'another'}),chat('archived',{origin:'inbox',archived:true})];
  const foreignGroup=chat('different-profile',{origin:'task'});foreignGroup.group='bad';rows.push(foreignGroup);
  const groups=allAssistantChatGroups(rows,'main','computer','group','assistant',true,history);
  assert.deepEqual(groups.map(g=>[g.id,g.rows.length]),[['all',8],['mine',1],['inbox',1],['task',2],['lark_agent',1],['spawned',1],['extension',1],['other',1]]);
  assert.deepEqual(groups[0].rows.slice(0,3).map(r=>r.id),['inbox','mine','scheduled']);
  assert.equal(groups[1].rows[0].id,'mine');
  const k=x=>({inbox:'Inbox',phone_todo_section_needs:'Needs you',kb_minutes_ago:'{n} minutes ago'}[x] || x);
  assert.equal(allChatDetail(rows[1],assistantChatSource(rows[1],history),'all','en-US',Date.parse(stamp),k),'Inbox · Needs you');
  assert.equal(allChatDetail(rows[1],'inbox','inbox','en-US',Date.parse(stamp),k),'Needs you');
  assert.equal(allChatDetail(rows[2],'','mine','en-US',Date.parse(stamp)+120000,k),'2 minutes ago');
  assert.deepEqual(assistantChatTitleParts('1105 | 类型 | first | second'),['1105 | 类型 | ','first | second']);
  assert.deepEqual(assistantChatTitleParts('My | conversation'),['','My | conversation']);
  for (const [origin,word] of [['lark_agent','飞书 /agent'],['extension','扩展']]) {
    assert.equal(allChatDetail(rows[1],origin,'all','zh-CN',Date.parse(stamp),strings('zh_CN')),word+' · 需要你');
  }
  const legacy=allAssistantChatGroups([chat('own'),chat('legacy-task')],'main','computer','group','assistant',false,history);
  assert.deepEqual(legacy.map(g=>[g.id,g.rows.map(r=>r.id)]),[['all',['legacy-task','own']],['mine',['own']]]);
});
