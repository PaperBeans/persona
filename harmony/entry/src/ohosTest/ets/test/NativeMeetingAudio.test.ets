import {describe,it,expect} from '@ohos/hypium';
import {fileIo} from '@kit.CoreFileKit';
import {util} from '@kit.ArkTS';
import {connection,http,socket} from '@kit.NetworkKit';
import {nativeTestContext} from './NativeContext';
import {fixtureArgument} from './NativeTransport.test';
import {HubTransport,EndpointConfig} from '../../../main/ets/platform/HubTransport';
import {NativeRecording} from '../../../main/ets/platform/NativeRecording';
import {meetingAudioNetwork} from '../../../main/ets/platform/MeetingAudioNetwork';
import {MeetingHubAudio,MeetingAudioIO,MeetingAudioPipe} from '../../../main/ets/models/MeetingHubAudio';
import {Wire} from '../../../main/ets/models/Wire';
import {newMeetingJournal} from '../../../main/ets/models/MeetingRoom';

const rate=16000;
function sleep(ms:number):Promise<void>{return new Promise<void>((resolve)=>{setTimeout(()=>resolve(),ms);});}
async function until(check:()=>boolean,label:string):Promise<void> {
  for(let i=0;i<220;i++){if(check())return;await sleep(70);}
  throw new Error('meeting_fixture_timeout_'+label);
}
async function upstream(value:string):Promise<void>{
  const port=Number(fixtureArgument('fixtureControlPort'));
  if(!Number.isInteger(port) || port<1 || port>65535 || !['failed','ok'].includes(value))throw new Error('isolated_fixture_required');
  const request=http.createHttp();
  try{
    const result=await request.request('http://127.0.0.1:'+String(port)+'/mock/meeting_transcription',{
      method:http.RequestMethod.POST,extraData:JSON.stringify(Wire.payload(['upstream'],[value]))});
    expect(result.responseCode).assertEqual(200);
  }finally{request.destroy();}
}

export default function nativeMeetingAudioTests():void {
  describe('NativeMeetingAudio',():void=>{
    it('uses_the_native_authenticated_audio_socket_and_private_PCM_without_opening_a_microphone',0,async ():Promise<void>=>{
      const addr=fixtureArgument('fixtureAddress'),pin=fixtureArgument('fixturePin');
      if(fixtureArgument('meetingAudioFixture')!=='synthetic' || !/^https:\/\/127\.0\.0\.1:\d+$/.test(addr) || pin.length!==43)
        throw new Error('isolated_fixture_required');
      const ctx=nativeTestContext(),recorder=new NativeRecording(),id=util.generateRandomUUID().toLowerCase(),path='/api/v1/recordings/'+id;
      const transport=new HubTransport(new EndpointConfig(addr,'00000000-0000-4000-8000-000000000001','synthetic-fixture-token',pin,'assistant'));
      const get=async(url:string):Promise<Record<string,Object>>=>Wire.parse(await transport.request(url));
      const started=new Date().toISOString(),body=Wire.payload(['revision','started_at','device','kind','title','language','room','transcription','complete','duration','segments','bookmarks'],
        [1,started,'phone','meeting','Synthetic native audio session','zh-CN',Wire.payload(['attendance'],['participant']),'hub',false,0,[],[]]);
      const audio=new MeetingHubAudio();let online=true,opened=0,sampleReads=0,bytes=0,owns=true;
      await recorder.initialize(ctx);await meetingAudioNetwork.initialize(ctx);
      const fixturePath=ctx.filesDir+'/knowledge-recordings/'+id+'.pcm';
      const tone=new ArrayBuffer(rate*10*2),samples=new DataView(tone);
      for(let i=0;i<rate*10;i++)samples.setInt16(i*2,Math.round(9000*Math.sin(2*Math.PI*330*i/rate)),true);
      const fs=fileIo.openSync(fixturePath,fileIo.OpenMode.CREATE|fileIo.OpenMode.READ_WRITE|fileIo.OpenMode.TRUNC);
      try{fileIo.writeSync(fs.fd,tone);}finally{fileIo.closeSync(fs);}
      const restricted=!!meetingAudioNetwork.key(true);
      const expectedRoute=fixtureArgument('meetingNetwork');
      if(expectedRoute==='wifi' && !restricted)throw new Error('fixture_has_no_unmetered_wifi');
      if(expectedRoute==='metered' && restricted)throw new Error('fixture_expected_a_restricted_path');
      const netKey=():string=>online ? meetingAudioNetwork.key(restricted):'';
      const io:MeetingAudioIO={current:()=>owns, network:netKey,
        authorize:async():Promise<boolean>=>Wire.bool(await get('/api/v1/assistant'),'meeting_transcription_ready'),
        pipe:(key:string):MeetingAudioPipe=>{opened++;return transport.stream(path+'/audio',meetingAudioNetwork.binding(key,restricted));},
        pcm:async(at:number,n:number):Promise<ArrayBuffer>=>{sampleReads++;const data=await recorder.readMeetingPCM(id,at,n);bytes+=data.byteLength;return data;},
        timeline:()=>Wire.payload(['roles','speaking'],[[Wire.payload(['at','attendance'],[0,'participant'])],[]]),
        live:(from:number):Promise<Record<string,Object>>=>get(path+'/live?from='+String(from))
      };
      try{
        expect(!!netKey()).assertTrue();
        const probe=new DataView(await recorder.readMeetingPCM(id,173,6));
        for(let i=0;i<6;i++)expect(probe.getInt16(i*2,true)).assertEqual(samples.getInt16((173+i)*2,true));
        const peer=transport.meetingCredentialKey();
        expect(peer.length).assertEqual(43);
        const scopedSocket=socket.constructTCPSocketInstance();
        try{
          await scopedSocket.bind({address:'0.0.0.0',port:0,family:1});
          await connection.getDefaultNetSync().bindSocket(scopedSocket);
        }finally{await scopedSocket.close();}
        recorder.saveMeetingConsent({id:id,profileId:'qa',hubId:'qa-hub',managerId:'assistant',endpoint:addr,pin:pin,
          credentialKey:peer,serviceRef:'',serviceName:'Persona',wifiOnly:true});
        expect(recorder.meetingConsent(id)?.credentialKey).assertEqual(peer);
        recorder.clearMeetingConsent(id);expect(recorder.meetingConsent(id)===undefined).assertTrue();
        const room=newMeetingJournal(id,'qa','qa-hub','assistant',started,'Synthetic room','zh-CN','off',true,'hub');
        room.effectiveTranscription='hub';room.next=3;recorder.saveRoom(room);
        expect(recorder.loadRoom(id,'qa','qa-hub','assistant')?.next).assertEqual(3);
        if(!restricted){
          await meetingAudioNetwork.setWifiOnly(true);
          expect(netKey()).assertEqual('');
          audio.begin(io,'participant');audio.effective(Wire.payload(['transcription'],['hub']));audio.captured(rate*5);
          await sleep(150);
          expect(audio.phase).assertEqual('waiting_network');expect(bytes).assertEqual(0);expect(opened).assertEqual(0);
          audio.clear();await meetingAudioNetwork.setWifiOnly(false);
        }
        // Calling the app's recorder would request the microphone; this suite owns the generated PCM above.
        audio.begin(io,'participant');audio.captured(rate*5);await sleep(100);expect(opened).assertEqual(0);expect(sampleReads).assertEqual(0);
        const ack=Wire.parse(await transport.request(path,'PUT',JSON.stringify(body)));expect(Wire.text(ack,'transcription')).assertEqual('hub');
        audio.effective(ack);
        await until(()=>audio.ackSeconds>=5 && !!audio.latestLine,'live_'+String(opened));
        expect(audio.phase).assertEqual('live');expect(bytes).assertEqual(rate*5*2);
        expect(audio.usePhoneLines('participant')).assertFalse();expect(audio.usePhoneLines('off')).assertTrue();
        await upstream('failed');
        await until(()=>audio.problem==='upstream_failed','provider-failed');
        expect(audio.usePhoneLines('participant')).assertTrue();expect(audio.showCaption()).assertFalse();
        online=false;audio.networkChanged();audio.captured(rate*10);await sleep(150);expect(bytes).assertEqual(rate*5*2);
        await upstream('ok');
        online=true;audio.networkChanged();await until(()=>audio.ackSeconds>=10 && opened>=2,'resume');
        await until(()=>!audio.problem && !audio.usePhoneLines('participant'),'provider-back');
        audio.finish(rate*10);body['complete']=true;body['ended_at']=new Date().toISOString();body['duration']=10;body['revision']=2;
        await transport.request(path,'PUT',JSON.stringify(body));await until(()=>audio.phase==='final','final');
        expect(bytes).assertEqual(tone.byteLength);expect(!!audio.latestLine).assertTrue();
        const live=await get(path+'/live');expect(Wire.text(Wire.object(live,'transcription'),'state')).assertEqual('final');
        audio.clear();audio.begin(io,'participant');audio.withdraw();audio.effective(ack);audio.captured(rate*10);await sleep(100);
        expect(opened).assertEqual(2);expect(bytes).assertEqual(tone.byteLength);
      }finally{
        owns=false;audio.clear();transport.close();fileIo.unlinkSync(fixturePath);
      }
    });
    it('after_a_relaunch_only_the_missing_private_samples_get_a_pinned_http_gap_PUT',0,async ():Promise<void>=>{
      const addr=fixtureArgument('fixtureAddress'),pin=fixtureArgument('fixturePin');
      if(fixtureArgument('meetingAudioFixture')!=='synthetic' || !/^https:\/\/127\.0\.0\.1:\d+$/.test(addr) || pin.length!==43)
        throw new Error('isolated_fixture_required');
      const ctx=nativeTestContext(),recorder=new NativeRecording(),id=util.generateRandomUUID().toLowerCase(),path='/api/v1/recordings/'+id;
      const transport=new HubTransport(new EndpointConfig(addr,'00000000-0000-4000-8000-000000000001','synthetic-fixture-token',pin,'assistant'));
      await recorder.initialize(ctx);
      const data=new ArrayBuffer(rate*4*2),samples=new DataView(data);
      for(let i=0;i<rate*4;i++)samples.setInt16(i*2,Math.round(8000*Math.sin(2*Math.PI*190*i/rate)),true);
      const file=ctx.filesDir+'/knowledge-recordings/'+id+'.pcm';
      const spool=fileIo.openSync(file,fileIo.OpenMode.CREATE|fileIo.OpenMode.READ_WRITE|fileIo.OpenMode.TRUNC);
      try{fileIo.writeSync(spool.fd,data);}finally{fileIo.closeSync(spool);}
      const audio=new MeetingHubAudio();let uploads=0,readAt=-1,readSamples=0;
      const key=():string=>meetingAudioNetwork.key(false);
      const gap=async (network:string,from:number,buffer:ArrayBuffer,admit:()=>boolean):Promise<Record<string,Object>>=>{
        if(!admit() || !network.startsWith('any:'))throw new Error('unexpected_fixture_path');
        const route=path+'/audio?at='+String(from/rate);
        const pipe=transport.stream(route,meetingAudioNetwork.binding(network,false));
        try{return Wire.parse(await pipe.putMeetingGap(route,buffer));}finally{pipe.close();}
      };
      const io:MeetingAudioIO={
        current:()=>true,network:key,authorize:async():Promise<boolean>=>Wire.bool(Wire.parse(await transport.request('/api/v1/assistant')),'meeting_transcription_ready'),
        pipe:(_network:string):MeetingAudioPipe=>{throw new Error('stopped_fixture_cannot_open_socket');},
        pcm:async(from:number,n:number):Promise<ArrayBuffer>=>{readAt=from;readSamples+=n;return recorder.readMeetingPCM(id,from,n);},
        timeline:():Record<string,Object>=>({}),live:async(from:number):Promise<Record<string,Object>>=>Wire.parse(await transport.request(path+'/live?from='+String(from))),
        uploadGap:async(network:string,from:number,buffer:ArrayBuffer,admit:()=>boolean):Promise<Record<string,Object>>=>{
          uploads++;return gap(network,from,buffer,admit);
        }
      };
      const start=new Date().toISOString(),body=Wire.payload(['revision','started_at','device','kind','title','language','room','transcription','complete','duration','segments','bookmarks'],
        [1,start,'phone','meeting','Synthetic stopped native audio session','zh-CN',Wire.payload(['attendance'],['listener']),'hub',false,0,[],[]]);
      try{
        expect(key().startsWith('any:')).assertTrue();
        const acceptance=Wire.parse(await transport.request(path,'PUT',JSON.stringify(body)));expect(Wire.text(acceptance,'transcription')).assertEqual('hub');
        await gap(key(),0,data.slice(0,rate*2),()=>true);await gap(key(),3*rate,data.slice(rate*3*2),()=>true);
        const ended=Date.now();body['revision']=2;body['complete']=true;body['duration']=4;body['ended_at']=new Date(ended).toISOString();
        await transport.request(path,'PUT',JSON.stringify(body));
        const before=await io.live(0);expect(JSON.stringify(Wire.object(before,'transcription')['gaps'])).assertEqual('[[1,3]]');
        audio.begin(io,'listener');audio.resumeStopped(rate*4,ended);
        await until(()=>audio.phase==='final','restored-final');
        expect(uploads).assertEqual(1);expect(readAt).assertEqual(rate);expect(readSamples).assertEqual(rate*2);
        const final=await io.live(0);expect(Wire.text(Wire.object(final,'transcription'),'state')).assertEqual('final');
        expect(JSON.stringify(Wire.object(final,'transcription')['gaps'])).assertEqual('[]');
      }finally{audio.clear();transport.close();fileIo.unlinkSync(file);}
    });
  });
}
