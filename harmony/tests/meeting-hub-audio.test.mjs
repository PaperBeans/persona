import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
import {readFileSync} from 'node:fs';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {randomUUID} from 'node:crypto';
import {fileURLToPath} from 'node:url';

registerHooks({
 resolve(spec,ctx,next){if(spec==='../platform/NativeRecording')return {url:'data:text/javascript,export class NativeRecording {}',shortCircuit:true};if(ctx.parentURL?.endsWith('.ets')&&spec.startsWith('.'))return{url:new URL(spec+'.ets',ctx.parentURL).href,shortCircuit:true};return next(spec,ctx);},
 load(url,ctx,next){if(url.endsWith('.ets'))return{format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s*/g,''))};return next(url,ctx);}
});
const {KnowledgeRecording}=await import('../entry/src/main/ets/models/KnowledgeRecording.ets');
const {MeetingHubAudio,MeetingHubChoice}=await import('../entry/src/main/ets/models/MeetingHubAudio.ets');
const stamp=()=>new Date().toISOString();
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function waitFor(fn,ms=5500){
 const end=Date.now()+ms;while(Date.now()<end){const result=await fn();if(result)return result;await sleep(15);}
 assert.fail('Timed out waiting for the native audio protocol state');
}
async function hub(t,settings={}){
 const script=fileURLToPath(new URL('../../scripts/mock/mock_hub.py',import.meta.url));
 const child=spawn('python3',['-I',script],{env:{PATH:process.env.PATH,PORT:'0',PYTHONUNBUFFERED:'1',...settings},stdio:['ignore','pipe','pipe']});
 let output='';child.stdout.on('data',b=>output+=b.toString());child.stderr.on('data',b=>output+=b.toString());
 t.after(async()=>{if(child.exitCode===null){const gone=once(child,'exit');child.kill();await gone;}});
 const addr=await waitFor(()=>output.match(/mock hub on (127\.0\.0\.1:\d+)/)?.[1],18000);
 const call=async(path,method='GET',body)=>{
  const res=await fetch('http://'+addr+path,{method,headers:{'Authorization':'Bearer mock','Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)});
  const val=await res.json();assert.ok(res.ok,method+' '+path+' '+JSON.stringify(val));return val;
 };
 return {addr,call};
}
function platformIO(mock,rid,data) {
 const path='/api/v1/recordings/'+rid,seen=[],requested=[];
 const policy={owner:true,service:true,net:'wifi:1',pipes:0};
 const io={
  current:()=>policy.owner,network:()=>policy.net,authorize:async()=>{const s=await mock.call('/api/v1/assistant');return policy.service&&s.meeting_transcription_ready===true;},
  pcm:async(at,count)=>{requested.push([at,count]);return data.slice(at*2,(at+count)*2).buffer;},timeline:()=>({roles:[{at:0,attendance:'participant'}],speaking:[]}),
  live:from=>mock.call(path+'/live?from='+from),
  pipe:net=>{
   ++policy.pipes;let ws;
   const pipe={connect(onFrame,onState){
    return new Promise((resolve,reject)=>{
      ws=new WebSocket('ws://'+mock.addr+path+'/audio?token=mock');
      ws.onopen=()=>{onState('ready');resolve();};ws.onclose=()=>onState('closed');ws.onerror=()=>reject(new Error('ws_error'));
      ws.onmessage=e=>{seen.push(['in',JSON.parse(e.data)]);onFrame(e.data);};
    });
   },send(body){if(ws?.readyState!==1)return false;seen.push(['out',JSON.parse(body)]);ws.send(body);return true;},
   async sendBinaryAsync(bytes){if(!policy.owner||!policy.net||policy.net!==net||ws?.readyState!==1)throw new Error('withdrawn');seen.push(['pcm',bytes.byteLength]);ws.send(bytes);},
   close(){if(ws?.readyState===0||ws?.readyState===1)ws.close();}};
   return pipe;
  }
 };
 return {io,seen,requested,policy};
}
function tone(seconds) {
 const data=new Uint8Array(seconds*32000),view=new DataView(data.buffer);
 for(let x=0;x<data.length/2;x++)view.setInt16(x*2,Math.round(9000*Math.sin(2*Math.PI*330*x/16000)),true);
 return data;
}
test('official local Hub accepts the meeting and streams caption/line before stop, resumes the same PCM at ready.from, and finalizes',async t=>{
 const mock=await hub(t),id=randomUUID(),path='/api/v1/recordings/'+id,pcm=tone(10),{io,seen,policy}=platformIO(mock,id,pcm);
 await mock.call('/mock/meeting_transcription','POST',{ready:true,line_seconds:2,final_seconds:.1});
 let revision=1;const meta={started_at:stamp(),kind:'meeting',language:'zh-CN',device:'phone',title:'Synthetic meeting audio',room:{attendance:'participant'},segments:[],bookmarks:[],duration:0,complete:false,transcription:'hub'};
 const ack=await mock.call(path,'PUT',{...meta,revision:revision++});assert.equal(ack.transcription,'hub');
 const audio=new MeetingHubAudio();t.after(()=>audio.clear());audio.begin(io,'participant');audio.captured(16000*5);
 assert.equal(policy.pipes,0);audio.effective(ack);
 await waitFor(()=>audio.latestLine && audio.ackSeconds>=5);
 assert.equal(audio.phase,'live');assert.ok(seen.find(([dir,f])=>dir==='in'&&f.type==='interim'));assert.ok(seen.find(([dir,f])=>dir==='in'&&f.type==='line'));
 const first=audio.latestLine;assert.notEqual(first,'');const before=seen.filter(f=>f[0]==='pcm').length;
 policy.net='';audio.networkChanged();audio.captured(16000*10);await sleep(80);
 assert.equal(audio.phase,'waiting_network');assert.equal(seen.filter(f=>f[0]==='pcm').length,before);
 policy.net='wifi:2';audio.networkChanged();
 await waitFor(()=>policy.pipes===2 && audio.ackSeconds>=10);
 const ready=seen.filter(([dir,f])=>dir==='in'&&f.type==='ready').map(([,f])=>f.from);
 assert.deepEqual(ready,[0,5]);
 audio.finish(16000*10);
 await waitFor(()=>seen.some(([dir,f])=>dir==='out'&&f.type==='end'));
 await mock.call(path,'PUT',{...meta,revision:revision++,ended_at:stamp(),duration:10,complete:true});
 await waitFor(async()=>{const info=await mock.call(path+'/live');return info.transcription?.state==='final';},9000);
 await waitFor(()=>audio.phase==='final',6500);
 assert.equal(seen.filter(x=>x[0]==='pcm').reduce((sum,x)=>sum+x[1],0),pcm.byteLength);
});
test('default, unconfirmed modes, provider refusal and withdrawal never leak PCM',async()=>{
 for(const mode of ['none','legacy','device','provider','revoked']){
  let reads=0,opened=0;const io={current:()=>true,network:()=>'wifi:1',authorize:async()=>mode!=='provider',pipe:()=>{opened++;throw Error('socket');},
   pcm:async()=>{reads++;return new ArrayBuffer(2)},live:async()=>({}),timeline:()=>({})};
  const audio=new MeetingHubAudio();audio.begin(io,'off');if(mode==='revoked')audio.withdraw();
  if(mode!=='none')audio.effective(mode==='legacy'?{}:{transcription:['provider','revoked'].includes(mode)?'hub':'device'});
  audio.captured(16000);await sleep(25);assert.equal(reads,0,mode);assert.equal(opened,0,mode);audio.clear();
 }
});

test('the actual recording start, local PCM callback and stop agree with the Hub room and its final transcript without requesting a microphone',async t=>{
 const mock=await hub(t),data=tone(8),manifests=new Map(),journals=new Map();let produced=0,notify,openId,wire,roomRole,permissions=0;
 await mock.call('/mock/meeting_transcription','POST',{ready:true,line_seconds:2,final_seconds:.1});
 const native={initialize:async()=>{},load:async()=>[],list:async()=>[],save:async entry=>{manifests.set(entry.id,structuredClone(entry));},
  loadRoom:id=>journals.get(id),saveRoom:r=>journals.set(r.id,structuredClone(r)),saveMeetingConsent:()=>{},clearMeetingConsent:()=>{},meetingConsent:()=>undefined,ensureMicrophonePermission:async()=>{permissions++;},
  startRoomSpeech:async()=>{},async start(_c,id,_limit,participant,captured){openId=id;notify=captured;roomRole=participant;return '/synthetic/'+id;},
  async stop(){return {path:'/synthetic/'+openId,bytes:produced*2,duration:produced/16000};},stopPlayback:async()=>{},stopRoomVoice:()=>{},cancelTranscription:async()=>{},
  readMeetingPCM:async(id,at,n)=>{assert.equal(id,openId);assert.ok(at+n<=produced);return data.slice(at*2,(at+n)*2).buffer;},
  meetingNetworkKey:()=>'wifi:1',meetingNetworkBinding:(key)=>({current:()=>key==='wifi:1'}),remove:async()=>{}};
 const repo={call:mock.call,transport:{meetingCredentialKey:()=> 'M'.repeat(43),async request(path,method='GET',body){return JSON.stringify(await mock.call(path,method,body?JSON.parse(body):undefined));},
  stream(path,rule){assert.ok(rule.current());assert.match(path,new RegExp('^/api/v1/recordings/'+openId+'/audio$'));
    wire??=platformIO(mock,openId,data);return wire.io.pipe('wifi:1');}}};
 const client={connected:true,demo:false,features:['meeting_attendance','meeting_conversation','meeting_live_view','meeting_transcription'],profile:{manager:'assistant',id:'synthetic-profile',hubId:'synthetic-hub',pin:'synthetic-pin',baseUrl:'http://'+mock.addr},
  scope:()=>'scope',connectionGeneration:()=>1,owns:(n,s)=>n===1&&s==='scope',repo:()=>repo,viewFor:async()=>client,observeEvents:()=>()=>{}};
 const model=new KnowledgeRecording(native);t.after(()=>model.dispose());
 await model.open(client,'assistant',{filesDir:'/synthetic'});assert.equal(await model.start('meeting','Synthetic, via recording controls','listener',false,new MeetingHubChoice(client,'','',false)),true);
 assert.equal(permissions,1,'only the synthetic permission boundary was invoked');assert.equal(roomRole,false);
 produced=16000*5;notify(produced);
 await waitFor(()=>model.hubAudio.phase==='live' && model.hubAudio.latestLine && model.hubAudio.ackSeconds>=5);
 assert.equal(model.captureState,'recording');assert.match(model.hubAudio.latestLine,/\S/);
 produced=16000*8;notify(produced);assert.equal(await model.stop(),true);
 await model.room.sync();await waitFor(()=>model.hubAudio.phase==='final',10000);
 assert.equal(model.captureState,'stopped');assert.equal(model.entry.audioBytes,data.length);assert.ok(manifests.has(openId));
 const live=await mock.call('/api/v1/recordings/'+openId+'/live');assert.equal(live.transcription.mode,'hub');assert.equal(live.transcription.state,'final');
 assert.ok(wire.seen.find(([k,f])=>k==='in'&&f.type==='line'));
});

test('the official upstream fault swaps the live source to the phone, no-interim keeps Hub lines, and recovery restores Hub lines while still recording',async t=>{
 const mock=await hub(t),id=randomUUID(),path='/api/v1/recordings/'+id,pcm=tone(12);
 const {io,seen}=platformIO(mock,id,pcm);
 await mock.call('/mock/meeting_transcription','POST',{line_seconds:1,final_seconds:.1});
 const meta={revision:1,started_at:stamp(),kind:'meeting',device:'phone',language:'zh-CN',title:'Synthetic source switching',
  room:{attendance:'listener'},transcription:'hub',segments:[],bookmarks:[],duration:0,complete:false};
 const audio=new MeetingHubAudio();t.after(()=>audio.clear());audio.begin(io,'listener');audio.effective(await mock.call(path,'PUT',meta));audio.captured(5*16000);
 await waitFor(()=>audio.liveNext>=4);assert.equal(audio.usePhoneLines('listener'),false);assert.equal(audio.usePhoneLines('off'),true);
 await mock.call('/mock/meeting_transcription','POST',{upstream:'no_interim'});await waitFor(()=>audio.problem==='no_interim');
 assert.equal(audio.showCaption(),false);assert.equal(audio.usePhoneLines('listener'),false);
 await mock.call('/mock/meeting_transcription','POST',{upstream:'failed'});await waitFor(()=>audio.problem==='upstream_failed');
 assert.equal(audio.phase,'live');assert.equal(audio.showCaption(),false);assert.equal(audio.usePhoneLines('listener'),true);assert.equal(audio.caption,'');
 const existing=audio.liveNext;audio.captured(7*16000);await waitFor(async()=>(await mock.call('/mock/meeting_transcription')).recordings[id].bytes>=7*32000);
 await mock.call('/mock/meeting_transcription','POST',{upstream:'ok'});audio.captured(12*16000);
 await waitFor(()=>audio.liveNext>existing && !audio.problem);assert.equal(audio.usePhoneLines('listener'),false);
 assert.ok(seen.some(([dir,frame])=>dir==='in'&&frame.type==='problem'&&frame.code===null));
});

test('a stopped consented recording repairs only the missing samples from its private file using the official gap PUT after relaunch',async t=>{
 const mock=await hub(t),id=randomUUID(),path='/api/v1/recordings/'+id,pcm=tone(4);const {io,policy,requested}=platformIO(mock,id,pcm);
 await mock.call('/mock/meeting_transcription','POST',{line_seconds:1,final_seconds:.1});
 let rev=1;const meta={started_at:stamp(),kind:'meeting',device:'phone',language:'zh-CN',title:'Synthetic private-range recovery',
  room:{attendance:'listener'},transcription:'hub',segments:[],bookmarks:[],duration:0,complete:false};
 await mock.call(path,'PUT',{...meta,revision:rev++});
 const put=async(at,data)=>{
  const reply=await fetch('http://'+mock.addr+path+'/audio?at='+String(at/16000),{method:'PUT',body:data,headers:{Authorization:'Bearer mock','Content-Type':'application/octet-stream'}});
  assert.equal(reply.status,200);return await reply.json();
 };
 await put(0,pcm.slice(0,32000));await put(3*16000,pcm.slice(3*32000));
 const ended=Date.now();await mock.call(path,'PUT',{...meta,revision:rev++,ended_at:new Date(ended).toISOString(),duration:4,complete:true});
 const before=await mock.call(path+'/live');assert.deepEqual(before.transcription.gaps,[[1,3]]);
 policy.net='any:1';let puts=0,retired=0;
 io.uploadGap=async(key,at,bytes,admit)=>{assert.ok(admit()&&key===policy.net);puts++;return await put(at,bytes);};io.retire=()=>retired++;
 const audio=new MeetingHubAudio();t.after(()=>audio.clear());audio.begin(io,'listener');audio.resumeStopped(4*16000,ended);
 await waitFor(()=>audio.phase==='final',10000);assert.equal(puts,1);assert.equal(policy.pipes,0);assert.deepEqual(requested,[[16000,32000]]);assert.equal(retired,1);
 const done=(await mock.call('/mock/meeting_transcription')).recordings[id];
 assert.equal(done.bytes,4*32000);assert.equal(done.transcription.state,'final');assert.equal(done.frames.filter(f=>f.type==='upload').length,3);
});

test('a network, provider or owner change while a private-file read is awaiting never dispatches the recovered bytes',async()=>{
 for(const changed of ['owner','network','withdraw']){
  let reads=0,uploads=0,retired=0;const state={owner:true,network:'any:4'};let release;
  const io={current:()=>state.owner,network:()=>state.network,authorize:async()=>true,
   pcm:async()=>{reads++;await new Promise(done=>{release=done;});return new ArrayBuffer(32000);},
   live:async()=>({transcription:{state:'finishing',mode:'hub',gaps:[[0,1]]}}),timeline:()=>({}),pipe:()=>assert.fail('no audio socket'),
   retire:()=>retired++,uploadGap:async()=>{uploads++;return {gaps:[]};}};
  const audio=new MeetingHubAudio();audio.begin(io,'listener');audio.resumeStopped(16000,Date.now());await waitFor(()=>reads===1);
  if(changed==='owner')state.owner=false;else if(changed==='network')state.network='';else audio.withdraw();
  release();await sleep(30);assert.equal(uploads,0,changed);if(changed==='withdraw')assert.equal(retired,1);audio.clear();
 }
});

test('the official Hub accepts an uninvited meeting owner: audio can transcribe privately without posting a live room line',async t=>{
 const mock=await hub(t),id=randomUUID(),path='/api/v1/recordings/'+id;
 await mock.call('/mock/meeting_transcription','POST',{line_seconds:1,final_seconds:.1});
 const meta={revision:1,started_at:stamp(),kind:'meeting',device:'phone',language:'zh-CN',title:'Solo synthetic test',room:{attendance:'off'},transcription:'hub',segments:[],bookmarks:[],duration:0,complete:false};
 const ack=await mock.call(path,'PUT',meta);assert.equal(ack.attendance,'off');assert.equal(ack.transcription,'hub');
 const {io,seen}=platformIO(mock,id,tone(5));io.timeline=()=>({roles:[{at:0,attendance:'off'}],speaking:[]});
 const audio=new MeetingHubAudio();t.after(()=>audio.clear());audio.begin(io,'off');audio.captured(80000);audio.effective(ack);
 await waitFor(()=>audio.ackSeconds===5);assert.equal(audio.usePhoneLines('off'),true);assert.equal(audio.latestLine,'');assert.ok(seen.some(([d,x])=>d==='in'&&x.type==='interim'));
 assert.equal((await mock.call(path+'/live')).next,0);
});

test('a real pre-feature manager acknowledging the recording without transcription never authorizes its audio, even after a newer response',async t=>{
 const mock=await hub(t,{MOCK_NO_MEETING_TRANSCRIPTION:'1'}),id=randomUUID(),path='/api/v1/recordings/'+id;
 const ack=await mock.call(path,'PUT',{revision:1,started_at:stamp(),kind:'meeting',device:'phone',language:'zh-CN',room:{attendance:'listener'},transcription:'hub',segments:[],bookmarks:[],duration:0,complete:false});
 assert.equal(ack.id,id);assert.equal(ack.transcription,undefined);
 const {io,policy,requested}=platformIO(mock,id,tone(1));const audio=new MeetingHubAudio();t.after(()=>audio.clear());
 audio.begin(io,'listener');audio.effective(ack);audio.captured(16000);audio.effective({transcription:'hub'});
 await sleep(30);assert.equal(audio.phase,'fallback');assert.equal(policy.pipes,0);assert.deepEqual(requested,[]);
});

test('a refused actual-mode handshake keeps the terminal status when transport reports its close before rejecting the promise',async()=>{
 let opens=0,reads=0;const audio=new MeetingHubAudio();
 audio.begin({current:()=>true,network:()=>'any:1',authorize:async()=>true,
  pipe:()=>({connect:async(_,state)=>{opens++;state('offline');throw Object.assign(new Error('upgrade_rejected'),{status:403});},close:()=>{}}),
  pcm:async()=>{reads++;return new ArrayBuffer(0);},live:async()=>({}),timeline:()=>({})},'listener');
 audio.effective({transcription:'hub'});audio.captured(16000);
 await waitFor(()=>audio.phase==='fallback');assert.equal(audio.problem,'audio_forbidden');assert.equal(reads,0);assert.equal(opens,1);audio.clear();
});

test('a private on-device meeting persists the real private mark with attendance off and transmits no audio or live lines',async t=>{
 const mock=await hub(t),journal=new Map(),manifests=new Map(),writes=[];
 const native={initialize:async()=>{},load:async()=>[],list:async()=>[],save:async e=>manifests.set(e.id,structuredClone(e)),
  saveRoom:r=>journal.set(r.id,structuredClone(r)),loadRoom:id=>journal.get(id),ensureMicrophonePermission:async()=>{},
  startRoomSpeech:()=>assert.fail('off does not open a live-room recognizer'),async start(c,id,limit,participant,samples){
    assert.equal(participant,false);assert.equal(samples,undefined);return '/synthetic/'+id;},
  stop:async()=>({path:'/synthetic/local',bytes:32000,duration:1}),stopPlayback:async()=>{},stopRoomVoice:()=>{},
  cancelTranscription:async()=>{},clearMeetingConsent:()=>{},remove:async()=>{}};
 const repo={transport:{async request(path,method='GET',body){const value=body?JSON.parse(body):undefined;writes.push({path,method,value});return JSON.stringify(await mock.call(path,method,value));},
  stream(){assert.fail('a private mark does not authorize cloud streaming');}}};
 const profile={manager:'assistant',id:'synthetic-private',hubId:'synthetic',baseUrl:'http://'+mock.addr,pin:'synthetic'};
 const client={profile,connected:true,demo:false,features:['meeting_conversation','meeting_attendance'],
  scope:()=>profile.id,connectionGeneration:()=>1,owns:(g,s)=>g===1 && s===profile.id,repo:()=>repo,viewFor:async()=>client};
 const model=new KnowledgeRecording(native);t.after(()=>model.dispose());await model.open(client,'assistant',{filesDir:'/synthetic'});
 assert.equal(await model.start('meeting','Synthetic private discussion','off',true),true);
 await model.room.sync();const id=model.entry.id,path='/api/v1/recordings/'+id;
 await waitFor(()=>writes.some(w=>w.path===path && w.method==='PUT'));
 const live=await mock.call(path+'/live');assert.equal(live.private,true);assert.equal(live.attendance,'off');assert.equal(live.next,0);
 assert.equal(await model.askRoom('Not participating'),false);assert.equal(await model.stop(),true);await model.room.sync();
 const detail=(await mock.call('/api/v1/kb/recordings/'+id)).detail;
 assert.equal(detail.meta.private,true);assert.equal(manifests.get(id).privateMark,true);
 assert.equal(writes.some(w=>w.path.includes('/audio') || w.method==='POST' && w.path.endsWith('/live')),false);
 const observations=(await mock.call('/mock/meeting_transcription')).recordings[id];assert.equal(observations?.bytes || 0,0);
 client.features=[];assert.equal(await model.start('meeting','Legacy Hub','off',true),false);assert.equal(model.error,'phone_meeting_unavailable');
});
