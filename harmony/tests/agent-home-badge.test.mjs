import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier,context,next) {
    if (specifier==='./Wire' && context.parentURL?.endsWith('/AgentAttention.ets')) return {url:new URL('./Wire.ets',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {agentAttentionCards,waitingAgentBadge}=await import('../entry/src/main/ets/models/AgentAttention.ets');
const fixtures=JSON.parse(readFileSync(new URL('../../spec/fixtures/needs-you/cases.json',import.meta.url),'utf8'));
for (const example of fixtures.cases) test('home badge follows the shared Needs You case '+example.name,()=>{
  const {sessions,interactions,mutes,quiet}=example.input;
  const rows=sessions.map(s=>ResourceRow.from(s,'session'));
  const todos=[ResourceRow.from({id:'quiet',quiet:true,agents:quiet.map(key=>{const [machine_id,session_id]=key.split('/');return {machine_id,session_id};})},'todo')];
  const muted=row=>{const key=row.machine+'/'+row.id,space=row.machine+'/'+row.data().space_name;
    return mutes.sessions.includes(key) || !mutes.allowed.includes(key) && mutes.spaces.includes(space);};
  assert.equal(waitingAgentBadge(rows,todos,agentAttentionCards(interactions,'owner'),'group','owner',muted,true),example.expected.badge);
});

test('the home badge isolates Hub and assistant scopes and falls back to session summaries until the Hub answers',()=>{
  const local=ResourceRow.from({session_id:'same',machine_id:'mac',state:'idle',pending_interactions:2},'session');local.group='a';local.manager='one';
  const foreign=ResourceRow.from({session_id:'same',machine_id:'mac',state:'needs_you'},'session');foreign.group='b';foreign.manager='two';
  const cards=agentAttentionCards([{manager:'two',interaction:{id:'elsewhere',session_id:'',machine_id:'mac',kind:'question'}},
    {manager:'one',interaction:{id:'owned',session_id:'',machine_id:'mac',kind:'question'}},
    {manager:'one',interaction:{id:'owned',session_id:'',machine_id:'mac',kind:'question'}}],'one');
  assert.equal(waitingAgentBadge([local,foreign],[],[],'a','one',()=>false,false),1);
  assert.equal(waitingAgentBadge([local,foreign],[],[],'a','one',()=>false,true),0);
  assert.equal(waitingAgentBadge([local,foreign],[],cards,'a','one',()=>false,true),1);
});

test('the badge reuses decoded session fields and reads a changed or restored wire row once',()=>{
  const make=id=>ResourceRow.from({session_id:id,machine_id:'studio',state:'idle',space_name:'review',children:{needs_you:1}},'session');
  const sessions=Array.from({length:80},(_,index)=>make(String(index)));
  const restored=new ResourceRow('restored','Restored','session');
  restored.machine='studio';
  restored.raw=JSON.stringify({pending_interactions:1,space_name:'quiet'});
  sessions.push(restored);
  let reads=0;
  for(const row of sessions){
    const original=row.data.bind(row);
    row.data=()=>{reads++;return original();};
  }
  const count=()=>waitingAgentBadge(sessions,[],[],'group','owner',(_row,space)=>space==='quiet',false);
  assert.equal(count(),80);
  assert.equal(count(),80);
  assert.equal(reads,1);
  const change=sessions[0].data();
  change.folded_under='1';
  sessions[0].raw=JSON.stringify(change);
  restored.raw=JSON.stringify({pending_interactions:1,space_name:'public'});
  assert.equal(count(),80);
  assert.equal(count(),80);
  assert.equal(reads,4);
});
