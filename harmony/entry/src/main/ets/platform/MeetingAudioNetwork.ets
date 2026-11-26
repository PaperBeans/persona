import {common} from '@kit.AbilityKit';
import {connection,socket} from '@kit.NetworkKit';
import {LocalStore} from './LocalStore';
import type {StreamNetworkRule} from './HubTransport';

const wifiPref='phone.meeting.transcription.wifi';

@ObservedV2
export class MeetingAudioNetwork {
  @Trace wifiOnly:boolean=false;
  private initialized:boolean=false;
  private prefs:LocalStore=new LocalStore();
  private load:Promise<void>|undefined;
  private prefWrite:Promise<void>=Promise.resolve();
  private prefVersion:number=0;
  async initialize(context:common.UIAbilityContext):Promise<void> {
    if (!this.load) this.load=this.prefs.open(context).then(async ():Promise<void>=>{
      const stored=(await this.prefs.get(wifiPref))==='true';
      if(this.prefVersion===0 || stored)this.wifiOnly=stored;
      this.initialized=true;
    })
      .catch((error:Error):void=>{this.load=undefined;throw error;});
    await this.load;
  }
  async setWifiOnly(value:boolean):Promise<void> {
    if(!this.load)throw new Error('storage_unavailable');
    const version=++this.prefVersion;
    if(value)this.wifiOnly=true;
    await this.load;
    const write=this.prefWrite.catch(()=>{}).then(async():Promise<void>=>{await this.prefs.put(wifiPref,String(value));});
    this.prefWrite=write;await write;
    if(version===this.prefVersion)this.wifiOnly=value;
  }
  key(sessionWifiOnly:boolean):string {
    if(!this.initialized)return '';
    try {
      const net=connection.getDefaultNetSync();if(!net || net.netId<=0)return '';
      const restricted=sessionWifiOnly || this.wifiOnly;
      if(!restricted)return 'any:'+String(net.netId);
      const caps=connection.getNetCapabilitiesSync(net);
      if(connection.isDefaultNetMeteredSync() || caps.bearerTypes.includes(connection.NetBearType.BEARER_VPN) || caps.bearerTypes.includes(connection.NetBearType.BEARER_CELLULAR) ||
        !caps.bearerTypes.some((b:connection.NetBearType)=>b===connection.NetBearType.BEARER_WIFI || b===connection.NetBearType.BEARER_ETHERNET))return '';
      return 'wifi:'+String(net.netId);
    }catch(_){return '';}
  }
  binding(key:string,sessionWifiOnly:boolean,admit:()=>boolean=()=>true):StreamNetworkRule {
    return new BoundMeetingNetwork(this,key,sessionWifiOnly,admit);
  }
}
class BoundMeetingNetwork implements StreamNetworkRule {
  readonly directOnly:boolean;
  private owner:MeetingAudioNetwork;
  private key:string;
  private sessionWifiOnly:boolean;
  private admit:()=>boolean;
  constructor(owner:MeetingAudioNetwork,key:string,sessionWifiOnly:boolean,admit:()=>boolean) {
    this.owner=owner;this.key=key;this.sessionWifiOnly=sessionWifiOnly;this.admit=admit;this.directOnly=key.startsWith('wifi:');
  }
  current():boolean {return this.admit() && !!this.key && this.owner.key(this.sessionWifiOnly)===this.key;}
  async bind(tcp:socket.TCPSocket):Promise<void> {
    if(!this.current())throw new Error('network_changed');
    if(this.directOnly){
      const net=connection.getDefaultNetSync();await net.bindSocket(tcp);
      if(!this.current())throw new Error('network_changed');
    }
  }
}
export const meetingAudioNetwork:MeetingAudioNetwork=new MeetingAudioNetwork();
