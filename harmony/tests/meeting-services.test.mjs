import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import {readFileSync} from 'node:fs';
import {createServer} from 'node:http';

registerHooks({
  resolve(spec,context,next) {
    if(context.parentURL?.endsWith('.ets') && spec.startsWith('.')) return {url:new URL(spec+'.ets',context.parentURL).href,shortCircuit:true};
    return next(spec,context);
  },
  load(url,context,next) {
    if(url.endsWith('.ets')) return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''))};
    return next(url,context);
  }
});
const {MeetingReminderQueue,MeetingOccurrence,PublishedMeetingReminder,upcomingMeetingReminders,meetingReminderFailure}=await import('../entry/src/main/ets/models/MeetingCalendarReminders.ets');
const {meetingGlossary,meetingSpeechServices,PhoneMeetingTranscription}=await import('../entry/src/main/ets/models/PhoneMeetingTranscription.ets');
const now=Date.UTC(2026,4,1,10);
function occurrence(id,minutes,props={}) {
  return Object.assign(new MeetingOccurrence(),{id,title:'Synthetic '+id,start:now+minutes*60000,end:now+(minutes+30)*60000,...props});
}
test('each timed, accepted and titled occurrence schedules once a minute before it, over the next 36 hours, earliest 16 first',()=>{
  const input=[occurrence('repeat',9),occurrence('repeat',9),occurrence('repeat',1200),occurrence('recording',15),occurrence('early',1),
    occurrence('allday',10,{allDay:true}),occurrence('declined',20,{declined:true}),occurrence('blank',8,{title:'  '}),occurrence('late',36*60+1)];
  const wanted=upcomingMeetingReminders(input,now,'recording');
  assert.deepEqual(wanted.map(i=>i.id),['repeat','repeat']);assert.equal(new Set(wanted.map(r=>r.key())).size,2);assert.equal(wanted[0].start-60000,now+8*60000);
  assert.equal(upcomingMeetingReminders([...Array(21)].map((_,i)=>occurrence(String(i),i+5)).reverse(),now).at(-1).id,'15');
});
test('reconciliation leaves matching alarms, replaces renamed and moved meetings, cancels stale entries, and finishes an overlapping disable',async()=>{
  let events=[occurrence('a',10),occurrence('b',20)],systemId=0,active=true,block,waiting;
  const existing=new Map();const calls=[];
  const system={existing:async()=>[...existing.values()],calendar:async()=>events,async publish(e){
    calls.push(['add',e.key()]);existing.set(++systemId,new PublishedMeetingReminder(systemId,e.key(),e.title));if(waiting){waiting();await block;}
  },async cancel(id){calls.push(['remove',id]);existing.delete(id);}};
  const queue=new MeetingReminderQueue(), refresh=()=>queue.refresh(system,()=>active,()=>'',()=>now);
  await refresh();assert.equal(calls.length,2);await refresh();assert.equal(calls.length,2);
  events=[occurrence('a',10,{title:'Renamed'}),occurrence('b',25)];await refresh();assert.equal(existing.size,2);
  assert.equal([...existing.values()].some(r=>r.title==='Renamed'),true);assert.equal(calls.filter(r=>r[0]==='remove').length,2);
  let release,arrived; block=new Promise(r=>release=r);const first=new Promise(r=>arrived=r);waiting=arrived;
  events.push(occurrence('c',30));const running=refresh();await first;active=false;const off=refresh();release();await Promise.all([running,off]);
  assert.equal(existing.size,0);
});
test('glossary normalizes Unicode terms and rejects excess entries; external options are advertised transcription contributions',()=>{
  assert.deepEqual(meetingGlossary(['  Zephyr  lab','zephyr lab','  ','📝']),['Zephyr lab','📝']);
  assert.equal(meetingGlossary(['📝'.repeat(41)]),undefined);assert.equal(meetingGlossary([...Array(201)].map((_,i)=>'term'+i)),undefined);
  assert.deepEqual(meetingSpeechServices([{id:'voice',name:'Voice'},{id:'test',name:'Test',contributes:{transcription:['draft','draft','live'],other:['x']}},{id:'not-asr',name:'Unused'}]).map(x=>x.ref),['','test/draft','test/live']);
});
async function isolated(t,fields) {
  const state={...fields}, requests=[];let pause,arrival,hold=false;
  const service=createServer(async(req,res)=>{
    const chunks=[];for await(const c of req)chunks.push(c);const text=Buffer.concat(chunks).toString();
    requests.push([req.method,req.url,text ? JSON.parse(text) : {}]);
    if(hold && req.method==='GET' && req.url==='/api/v1/assistant'){hold=false;arrival();await new Promise(r=>pause=r);}
    if(req.method==='PUT')Object.assign(state,JSON.parse(text));
    const payload=req.url.includes('plugins') || req.url.includes('extensions') ? {plugins:[{id:'test',name:'Isolated service',contributes:{transcription:['speech']}}]} : state;
    res.setHeader('content-type','application/json');res.end(JSON.stringify(payload));
  });
  await new Promise(r=>service.listen(0,'127.0.0.1',r));t.after(()=>new Promise(r=>service.close(r)));
  const url='http://127.0.0.1:'+service.address().port;
  const repo={call:async(path,method='GET',body={},op,admit)=>{if(admit&&!admit())throw new Error('scope');return (await fetch(url+path,{method,body:method==='GET'?undefined:JSON.stringify(body)})).json();}};
  const cfg={scope:'local/A'},owner={connected:true,profile:{manager:'synthetic'},features:[],repo:()=>repo,connectionGeneration:()=>1,scope:()=>cfg.scope,
    owns:(g,s)=>g===1&&s===cfg.scope,operationBlocked:()=>false};
  return {state,requests,owner,cfg,waitRead:()=>{hold=true;return new Promise(r=>arrival=r)},resume:()=>pause()};
}
test('legacy, unready and configured local hubs truthfully expose explicit settings; only the selected field is written and confirmed',async t=>{
  const {state,requests,owner}=await isolated(t,{manager:'synthetic',unrelated:'must stay'}), form=new PhoneMeetingTranscription();
  await form.open(owner);assert.equal(form.offered,false);assert.equal(requests.length,1);
  Object.assign(state,{meeting_transcription_default:'device',meeting_transcription_ready:false,meeting_glossary:[],meeting_transcription_extension:''});
  await form.open(owner);assert.equal(form.offered,true);assert.equal(form.ready,false);assert.equal(form.mode,'device');
  assert.equal(await form.choose('meeting_transcription_default','hub'),true);assert.equal(form.mode,'hub');assert.equal(form.ready,false);
  assert.equal(await form.choose('meeting_transcription_extension','test/speech'),true);assert.equal(form.serviceName(),'Isolated service');
  assert.equal(await form.choose('meeting_glossary',['  Ada','ada',' Team  A']),true);assert.deepEqual(state.meeting_glossary,['Ada','Team A']);
  assert.deepEqual(requests.filter(r=>r[0]==='PUT').map(r=>Object.keys(r[2])),[['meeting_transcription_default'],['meeting_transcription_extension'],['meeting_glossary']]);
  assert.equal(state.unrelated,'must stay');assert.equal(requests.some(([,url])=>url.includes('audio')||url.includes('recordings')),false);
  state.meeting_glossary=['elsewhere'];assert.equal(await form.choose('meeting_glossary',['my unsaved draft']),false);assert.equal(form.error,'request_updated');
  state.manager='foreign';await form.open(owner);assert.equal(form.offered,false);
});
test('scope change while reading admission or saving never targets a second owner',async t=>{
  const {owner,cfg,waitRead,resume,requests}=await isolated(t,{manager:'synthetic',meeting_transcription_default:'device',meeting_transcription_ready:true});
  const form=new PhoneMeetingTranscription();await form.open(owner);
  const arrived=waitRead(),saving=form.choose('meeting_transcription_default','hub');await arrived;cfg.scope='replaced';resume();
  assert.equal(await saving,false);assert.equal(requests.some(r=>r[0]==='PUT'),false);
});

test('a background-reminder quota rejection is distinct from a notification refusal and never counted as an owned schedule',async()=>{
  const system={existing:async()=>[],calendar:async()=>[occurrence('test',10)],cancel:async()=>{},publish:async()=>{throw Object.assign(new Error('quota'),{code:1700002})}};
  const queue=new MeetingReminderQueue();
  await assert.rejects(queue.refresh(system,()=>true,()=>'',()=>now),e=>meetingReminderFailure(e.code)==='meeting_reminder_quota');
  assert.equal((await system.existing()).length,0);assert.equal(meetingReminderFailure(1700001),'notifications_denied');
  assert.equal(meetingReminderFailure(500),'meeting_reminder_unavailable');
});

test('retention shown before capture belongs to the selected assistant and is kept only for a positive whole number of days',async t=>{
  const env=await isolated(t,{manager:'synthetic',meeting_transcription_default:'hub',meeting_transcription_ready:true,recording_days:14});
  const speech=new PhoneMeetingTranscription();
  await speech.open(env.owner);assert.equal(speech.retentionDays,14);
  for(const value of [0,-3,4.2,366,undefined]){env.state.recording_days=value;await speech.open(env.owner);assert.equal(speech.retentionDays,0);}
  env.state.recording_days=28;env.state.manager='foreign';await speech.open(env.owner);assert.equal(speech.retentionDays,0);
});
