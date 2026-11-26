import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier,context,next) {
    if (specifier.endsWith('/core/PhoneSettingsPermissions')) return {url:new URL(specifier+'.ts',context.parentURL).href,shortCircuit:true};
    if (context.parentURL?.endsWith('/PhoneAgentSettings.ets') && specifier==='./Wire')
      return {url:new URL('./Wire.ets',context.parentURL).href,shortCircuit:true};
    return next(specifier,context);
  },
  load(url,context,next) {
    if (!url.endsWith('.ets')) return next(url,context);
    const source=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(source),shortCircuit:true};
  }
});
const {PhoneAgentSettings,AgentSettingTextDraft,agentPreferencePath,validAgentSetting,agentSettingValue,cleanAgentFolder}=
  await import('../entry/src/main/ets/models/PhoneAgentSettings.ets');
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');

test('new conversation permissions require a matching manager receipt and restore a refused choice',async()=>{
  let saved='default',answer='accept',release;
  const env=harness(async(path,verb,body,admit)=>{
    assert.equal(path,'/api/v1/assistant');
    if(verb==='GET') return {manager:'work',default_permission_mode:saved};
    assert.equal(verb,'PUT');assert.deepEqual(Object.keys(body),['default_permission_mode']);
    if(answer==='deferred') await new Promise(resolve=>{release=resolve;});
    if(!admit()) throw Error('retired');
    if(answer==='refuse') return {manager:'work',default_permission_mode:saved};
    if(answer==='wrong-owner') return {manager:'other',default_permission_mode:body.default_permission_mode};
    saved=body.default_permission_mode;return {manager:'work',default_permission_mode:saved};
  }),form=new PhoneAgentSettings();
  await form.open(env.model,'phone_default_permissions');
  assert.equal(await form.setConversationPermission('acceptEdits'),true);
  await form.open(env.model,'phone_default_permissions');assert.equal(form.data.default_permission_mode,'acceptEdits');
  for(const kind of ['refuse','wrong-owner']) {answer=kind;assert.equal(await form.setConversationPermission('auto'),false);assert.equal(form.data.default_permission_mode,'acceptEdits');}
  answer='deferred';const write=form.setConversationPermission('plan');assert.equal(form.data.default_permission_mode,'plan');
  assert.equal(await form.setConversationPermission('bypassPermissions'),false);form.close();release();
  assert.equal(await write,false);assert.deepEqual(form.data,{});assert.equal(saved,'acceptEdits');
  const legacy=harness(async()=>({manager:'work'}));await form.open(legacy.model,'phone_default_permissions');
  assert.equal(await form.setConversationPermission('default'),false);assert.equal(legacy.calls.length,1);
});

function harness(request) {
  const calls=[],state={generation:1,scope:'work'};
  const machine=(id,manager,connected=true) => ResourceRow.from({id,machine_id:id,manager,state:connected?'online':'offline'},'machine');
  const rows=[machine('my/mac','work'),machine('else','other'),machine('gone','work',false)];
  const repository={async call(path,verb='GET',body={},op='',admit){
    calls.push({path,verb,body,op});
    return request(path,verb,body,admit);
  }};
  const model={profile:{id:'h',manager:'work'},machines:rows,connected:true,repo:()=>repository,
    connectionGeneration:()=>state.generation,scope:()=>state.scope,owns:(gen,scope)=>gen===state.generation && scope===state.scope};
  return {model,state,calls};
}
test('machine choices and preference writes use the selected owner and typed server controls',async()=>{
  const enumOption={key:'approval',type:'enum',options:['ask','allow'],default:'ask'};
  const fields=[enumOption,{key:'visible',type:'bool',default:true,value:false}];
  const env=harness(async (_path,verb,body,admit)=>{
    if (verb==='PUT') {assert.ok(admit());fields[0].value=body.value}
    return {settings:fields};
  });
  const settings=new PhoneAgentSettings();await settings.open(env.model,'phone_codex_config','else');
  assert.deepEqual(settings.machines(env.model).map(m=>m.id),['my/mac']);
  assert.equal(agentPreferencePath('phone_codex_config',settings.machine),'/api/v1/machines/my%2Fmac/codex-settings');
  assert.equal(agentSettingValue(fields[1]),false);assert.equal(validAgentSetting(enumOption,false),false);
  await settings.setOption('unknown',true);await settings.setOption('approval','outside');await settings.setOption('approval','ask');
  assert.equal(env.calls.length,1);
  await settings.setOption('approval','allow');assert.equal(env.calls.length,2);
  assert.deepEqual(env.calls[1].body,{key:'approval',value:'allow'});
  assert.equal(env.calls[1].op,'','the durable mutation store must issue a fresh id for each user operation');
});
test('two devices changing folder rules receive a conflict or keep both; older Hubs are read only',async()=>{
  const rule=pattern=>({pattern,action:'hide',enabled:true});
  let rules=[];
  const request=async(_path,verb,body,admit)=>{
    if (verb==='PUT') {
      assert.ok(admit());
      if (JSON.stringify(body.expected_rules)!==JSON.stringify(rules)) throw Object.assign(new Error('conflict'),{status:409});
      rules=structuredClone(body.rules);
    }
    return {supports_expected_rules:true,rules:structuredClone(rules)};
  };
  const x=harness(request),y=harness(request),a=new PhoneAgentSettings(),b=new PhoneAgentSettings();
  await Promise.all([a.open(x.model,'phone_agent_folders'),b.open(y.model,'phone_agent_folders')]);
  const acknowledgements=await Promise.all([a.changeFolder('~/one',rule('~/one'),true),b.changeFolder('~/two',rule('~/two'),true)]);
  assert.deepEqual(acknowledgements.sort(),[false,true]);
  assert.equal(rules.length,1);
  const loser=a.notice?a:b;assert.equal(loser.notice,'phone_rules_changed');assert.deepEqual(loser.data.rules,rules);
  const missing=rules[0].pattern==='~/one'?'~/two':'~/one';await loser.changeFolder(missing,rule(missing),true);
  assert.deepEqual(rules.map(r=>r.pattern).sort(),['~/one','~/two']);assert.equal(loser.notice,'');
  const legacy=harness(async()=>({rules:[rule('~/old')]})),old=new PhoneAgentSettings();await old.open(legacy.model,'phone_agent_folders');
  await old.changeFolder('~/old',undefined);
  assert.ok(legacy.calls.every(c=>c.verb==='GET'));
  assert.equal(cleanAgentFolder(' ~/weather/// '),'~/weather');
  for(const bad of ['relative','/','*','~/']) assert.equal(cleanAgentFolder(bad),'');
});
test('a late machine read and an admitted save cannot cross an owner change',async()=>{
  let release, entered;
  const ready=new Promise(resolve=>{entered=resolve});
  const gate=new Promise(resolve=>{release=resolve});
  const env=harness(async(_path,verb,_body,admit)=>{
    if (verb==='PUT') {entered();await gate;assert.equal(admit(),false);throw new Error('request_updated')}
    return {agent_tasks:true};
  });
  const settings=new PhoneAgentSettings();await settings.open(env.model,'phone_agent_followup');
  await settings.setOption('made_up',true);const pending=settings.setOption('agent_tasks',false);
  await ready;env.state.generation++;settings.close();release();await pending;
  assert.deepEqual(settings.data,{});assert.equal(settings.error,'');
});

test('a conflicting addition with the same folder name is never acknowledged as a saved local draft',async()=>{
  const remote={pattern:'~/weather',enabled:true,action:'block'};
  let reads=0;
  const env=harness(async(_path,verb)=>{
    if (verb==='PUT') throw Object.assign(new Error('conflict'),{status:409});
    return {supports_expected_rules:true,rules:++reads===1?[]:[remote]};
  });
  const page=new PhoneAgentSettings();await page.open(env.model,'phone_agent_folders');
  assert.equal(await page.changeFolder('~/weather',{...remote,action:'hide'},true),false);
  assert.deepEqual(page.data.rules,[remote]);assert.equal(page.notice,'phone_rules_changed');
});

test('failed machine string saves keep the entered model name across a read retry',async()=>{
  let stored='original',failure=true;
  const current=()=>({settings:[{key:'model',type:'string',value:stored}]});
  const env=harness(async(_path,method,body)=>{
    if (method==='PUT') {
      if (failure) throw new Error('temporarily unavailable');
      stored=body.value;
    }
    return current();
  });
  const form=new PhoneAgentSettings(),draft=new AgentSettingTextDraft();
  await form.open(env.model,'phone_codex_config');
  const target='work/macbook/codex';
  draft.begin(target,'model',stored);draft.edit('user-typed-model');
  await draft.save(form,target);assert.equal(form.error,'failed');
  assert.ok(draft.isEditing(target,'model'));assert.equal(draft.text,'user-typed-model');assert.equal(stored,'original');
  assert.equal(await form.setOption('model','original'),false,'an unverified value cannot dismiss an error-state editor');
  failure=false;await form.read();assert.equal(draft.text,'user-typed-model');
  await draft.save(form,target);assert.equal(stored,'user-typed-model');assert.equal(draft.key,'');
});
test('a delayed string acknowledgement cannot clear newer typing, identical retyping, or another machine',async()=>{
  for (const edit of ['changed','same-again','another-owner']) {
    let stored='before',entered,release;
    const sending=new Promise(resolve=>{entered=resolve}),gate=new Promise(resolve=>{release=resolve});
    const env=harness(async(_path,method,body)=>{
      if (method==='PUT'){entered();await gate;stored=body.value;}
      return {settings:[{key:'model',type:'string',value:stored}]};
    });
    const form=new PhoneAgentSettings(),draft=new AgentSettingTextDraft(),target='work/macbook';
    await form.open(env.model,'phone_codex_config');
    draft.begin(target,'model',stored);draft.edit('sent');
    const pending=draft.save(form,target);await sending;
    if (edit==='changed') draft.edit('next');
    if (edit==='same-again'){draft.edit('next');draft.edit('sent');}
    if (edit==='another-owner') draft.begin('personal/macbook','model','sent');
    release();await pending;
    const current=edit==='another-owner'?'personal/macbook':target;
    assert.ok(draft.isEditing(current,'model'));assert.equal(draft.text,edit==='changed'?'next':'sent');
    const writes=()=>env.calls.filter(c=>c.verb==='PUT').length;
    assert.equal(writes(),1);
    if(edit==='same-again'){await draft.save(form,target);assert.equal(draft.key,'');assert.equal(writes(),1);}
    if(edit==='another-owner'){await draft.save(form,target);assert.ok(draft.isEditing(current,'model'));assert.equal(writes(),1);}
  }
});
