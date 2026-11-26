import { ClientModel } from './ClientModel';
import { Wire } from './Wire';
import { HttpFailure } from '../platform/HubTransport';

const MINUTE = 60000;
const DAY = 24 * 60 * MINUTE;
export const usageTicks: number[] = [0,6,12,24,72,120,168];
const HIDDEN_PROVIDERS = 'usage.hiddenProviders';

export class SubscriptionWindow {
  readonly label: string;
  readonly model: string;
  readonly minutes: number;
  readonly percent: number;
  readonly reset: number;
  readonly protection: number;
  constructor(data: Record<string,Object>) {
    this.label=Wire.text(data,'label'); this.model=Wire.text(data,'model'); this.percent=Wire.number(data,'used_percent');
    this.reset=Date.parse(Wire.text(data,'resets_at')); this.protection=Wire.number(data,'protect_percent',Number.NaN);
    const match=/^([\d.]+)\s*([mhdw])$/.exec(this.label.trim().toLowerCase());
    const scale:Record<string,number>={'m':1,'h':60,'d':1440,'w':10080};
    this.minutes=match && Number.isFinite(Number(match[1])) && Number(match[1])>0 ? Math.round(Number(match[1])*scale[match[2]]) : Math.max(0,Wire.number(data,'window_minutes'));
  }
  rank():number { return this.model ? 2 : this.minutes===300 ? 0 : this.minutes===10080 ? 1 : 2; }
  pace(now:number,stale:boolean):number {
    if (stale || !this.minutes || !Number.isFinite(this.reset)) return Number.NaN;
    const progress=1-(this.reset-now)/(this.minutes*MINUTE);
    return progress>=0.05 && progress<=1 ? progress*100 : Number.NaN;
  }
}

export class SubscriptionAccount {
  readonly id:string;
  readonly provider:string;
  readonly snapshot:string;
  readonly name:string;
  readonly plan:string;
  readonly windows:SubscriptionWindow[];
  readonly sampled:number;
  readonly staleAt:number;
  readonly stale:boolean;
  readonly problem:string;
  readonly machines:string[];
  readonly sources:string[];
  readonly local:boolean;
  constructor(data:Record<string,Object>,hub:string) {
    this.snapshot=JSON.stringify(data);
    this.id=JSON.stringify([hub,Wire.text(data,'id')]); this.provider=Wire.text(data,'provider').toLowerCase(); this.name=Wire.text(data,'name');
    const plan=Wire.text(data,'plan').trim(); this.plan=plan && !this.name.toLowerCase().includes(plan.toLowerCase()) ? plan : '';
    this.sampled=Date.parse(Wire.text(data,'sampled_at')); this.staleAt=Date.parse(Wire.text(data,'stale_at')); this.stale=Wire.bool(data,'stale');
    this.problem=Wire.text(data,'problem');this.sources=Wire.strings(data,'sources');this.machines=Wire.strings(data,'machines');this.local=Wire.bool(data,'local');
    this.windows=Wire.rows(data,'windows').map((w:Record<string,Object>)=>new SubscriptionWindow(w));
  }
  outdated(now:number):boolean { return this.stale || (Number.isFinite(this.staleAt) && now>=this.staleAt); }
  liveWindows(now:number):SubscriptionWindow[] {
    return this.windows.filter((w:SubscriptionWindow)=>!Number.isFinite(w.reset) || w.reset>now).sort((a:SubscriptionWindow,b:SubscriptionWindow)=>a.rank()-b.rank());
  }
  planReset(minutes:number,now:number):number {
    const window=this.windows.find((w:SubscriptionWindow)=>!w.model && w.minutes===minutes && (!Number.isFinite(w.reset) || w.reset>now));
    return window?.reset ?? Number.NaN;
  }
  visible(now:number):boolean {
    return !(this.sampled>946684800000 && now-this.sampled>3*DAY) && this.windows.some((w:SubscriptionWindow)=>!Number.isFinite(w.reset) || w.reset>now);
  }
}

export class SubscriptionProvider {
  readonly provider:string;
  accounts:SubscriptionAccount[]=[];
  sampled:number=Number.NaN;
  constructor(provider:string) { this.provider=provider; }
  cardKey():string { return JSON.stringify([this.provider,this.accounts.map((a:SubscriptionAccount)=>[a.id,a.snapshot])]); }
}

export function subscriptionGroups(accounts:SubscriptionAccount[],now:number):SubscriptionProvider[] {
  const groups=new Map<string,SubscriptionProvider>();
  accounts.forEach((a:SubscriptionAccount)=>{
    if (!a.visible(now)) return;
    let group=groups.get(a.provider);
    if (!group) { group=new SubscriptionProvider(a.provider);groups.set(a.provider,group); }
    group.accounts.push(a);
    if (Number.isFinite(a.sampled) && (!Number.isFinite(group.sampled) || a.sampled>group.sampled)) group.sampled=a.sampled;
  });
  return Array.from(groups.values()).map((group:SubscriptionProvider)=>{
    group.accounts.sort((a:SubscriptionAccount,b:SubscriptionAccount)=>{
      const left=a.planReset(10080,now),right=b.planReset(10080,now);
      if (Number.isFinite(left)!==Number.isFinite(right)) return Number.isFinite(left) ? -1 : 1;
      return Number.isFinite(left) && left!==right ? left-right : a.name.localeCompare(b.name);
    });
    return group;
  });
}
export function usageTimelinePosition(hours:number):number {
  const h=Math.max(0,Math.min(168,hours));return h<=24 ? h/24*0.45 : 0.45+(h-24)/144*0.55;
}
export function usageProviderTitle(provider:string,other:string):string {
  if (!provider) return other;
  if (provider==='xai') return 'xAI';
  return provider[0].toUpperCase()+provider.slice(1);
}

class HubUsageSnapshot {
  owner:ClientModel;
  scope:string;
  accounts:SubscriptionAccount[];
  constructor(owner:ClientModel,accounts:SubscriptionAccount[]) { this.owner=owner;this.scope=owner.scope();this.accounts=accounts; }
}

@ObservedV2
export class MachineSubscriptionUsage {
  @Trace accounts:SubscriptionAccount[]=[];
  @Trace hidden:string[]=[];
  @Trace filterReady:boolean=false;
  private persisted:string[]=[];
  private hubs:Map<string,HubUsageSnapshot>=new Map();
  private source='';
  private lastFetch=0;
  private loadRevision=0;
  private preferenceOwner:ClientModel|undefined;
  private preferenceRevision=0;
  private saving:Promise<void>=Promise.resolve();

  private readFilter(client:ClientModel):void {
    if (this.preferenceOwner===client) return;
    this.preferenceOwner=client;this.filterReady=false;const revision=++this.preferenceRevision;
    client.loadView(HIDDEN_PROVIDERS).then((saved:string)=>{
      if (this.preferenceOwner===client && this.preferenceRevision===revision) {this.persisted=Wire.strings(Wire.parse(saved),'hidden');this.hidden=this.persisted;}
    }).catch(()=>{}).finally(()=>{if (this.preferenceOwner===client) this.filterReady=true;});
  }
  setShown(client:ClientModel,provider:string,shown:boolean):void {
    this.readFilter(client);if(!this.filterReady)return;const prior=this.hidden;const next=shown ? prior.filter((k:string)=>k!==provider) : Array.from(new Set(prior.concat([provider]))).sort();
    const revision=++this.preferenceRevision;this.hidden=next;
    const write=this.saving.then(()=>client.saveView(HIDDEN_PROVIDERS,JSON.stringify({'hidden':next})));
    this.saving=write.then(()=>{if(this.preferenceOwner===client)this.persisted=next;}).catch(()=>{if(this.preferenceOwner===client && this.preferenceRevision===revision)this.hidden=this.persisted;});
  }
  retainOwners(client:ClientModel):void {
    const active=client.directoryOwners();
    const valid=new Map<string,HubUsageSnapshot>();
    active.forEach((owner:ClientModel)=>{const saved=this.hubs.get(owner.profile.id);if(saved && saved.owner===owner && saved.scope===owner.scope()) valid.set(owner.profile.id,saved);});
    if (valid.size!==this.hubs.size) {this.hubs=valid;this.accounts=Array.from(valid.values()).flatMap((v:HubUsageSnapshot)=>v.accounts);}
  }
  async refresh(client:ClientModel,force:boolean):Promise<void> {
    if (!client.isAppInForeground()) return;
    this.readFilter(client);
    const owners=client.directoryOwners(),base=client.scope(),source=JSON.stringify(owners.map((owner:ClientModel)=>[owner.profile.id,owner.scope(),owner.connected]));
    const now=Date.now();
    if (!force && this.source===source && now-this.lastFetch<MINUTE) return;
    this.source=source;this.lastFetch=now;const run=++this.loadRevision;const epoch=client.connectionGeneration();
    const next=new Map<string,HubUsageSnapshot>();
    owners.forEach((owner:ClientModel)=>{
      const saved=this.hubs.get(owner.profile.id);
      if (saved && saved.owner===owner && saved.scope===owner.scope()) next.set(owner.profile.id,saved);
    });
    if (next.size!==this.hubs.size) { this.hubs=new Map(next);this.accounts=Array.from(next.values()).flatMap((a:HubUsageSnapshot)=>a.accounts); }
    await Promise.all(owners.map(async(owner:ClientModel)=>{
      if (!owner.connected) return;
      const generation=owner.connectionGeneration(),scope=owner.scope(),hub=owner.profile.id;
      try {
        const response=await owner.repo().call('/api/v1/usage/windows');
        if (owner.owns(generation,scope) && (!Wire.has(response,'accounts') || response['accounts']===null || Array.isArray(response['accounts']))) next.set(hub,new HubUsageSnapshot(owner,Wire.rows(response,'accounts').map((r:Record<string,Object>)=>new SubscriptionAccount(r,hub))));
      } catch (error) {
        if (error instanceof HttpFailure && error.status===404 && owner.owns(generation,scope)) next.delete(hub);
      }
    }));
    if (this.loadRevision!==run || !client.owns(epoch,base)) return;
    const live=client.directoryOwners();this.hubs=new Map();const all:SubscriptionAccount[]=[];
    live.forEach((owner:ClientModel)=>{const part=next.get(owner.profile.id);if (part && part.owner===owner && part.scope===owner.scope()) {this.hubs.set(owner.profile.id,part);all.push(...part.accounts);}});
    this.accounts=all;
  }
  cancel():void { this.loadRevision++;this.source='';this.lastFetch=0; }
}
