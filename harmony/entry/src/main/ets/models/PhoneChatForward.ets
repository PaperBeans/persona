import type { TranscriptItem } from '../core/Domain';
import { util } from '@kit.ArkTS';
import type { ClientModel } from './ClientModel';
import { ChatMessage, ResourceRow, Wire } from './Wire';
import { DraftForwardImage } from './ChatDraftStore';
import { ChatAttachments } from './ChatAttachments';
import { loadMessageImage } from './MessageImage';
import { segment } from './HubRepository';
import { sessionSpaceSlotLabel } from './SessionSpace';
import { tr } from '../design/Strings';
import { parseMessageContent } from '../core/MessageContent';
import { forwardLimit, forwardMaximum, forwardTimestamp, forwardedBody, makeForward } from '../core/ChatForwardWire';
import { withSourceContext } from './Compatibility';

export class ForwardSection {
  id:string;title:string;rows:ResourceRow[];
  constructor(id:string,title:string,rows:ResourceRow[]) {this.id=id;this.title=title;this.rows=rows;}
}
export class CompletedForward {
  refs:Record<string,Object>[]=[];
  images:DraftForwardImage[]=[];
  transfer:string=util.generateRandomUUID();
}
function itemID(m:ChatMessage):string { return m.itemId || m.id; }
function address(r:ResourceRow):string { return JSON.stringify([r.machine,r.session||r.id]); }
function label(row:ResourceRow):string {
  const data=row.data(),agent=Wire.text(data,'agent',Wire.text(data,'agent_type')).toLowerCase();
  const hint=Wire.text(data,'agent_label',Wire.text(data,'agent_label_hint'));
  if(agent==='codex')return 'Codex';
  if(['claude','claude_code','claude-code'].includes(agent))return 'Claude';
  if(['traex','trae-cli','traecli'].includes(agent))return 'TRAE CLI';
  if(agent==='paseo' && (!hint || hint.toLowerCase()==='paseo'))return Wire.text(data,'launcher',hint||'Paseo');
  return hint||Wire.text(data,'agent','Claude');
}
export function forwardRoute(model:ClientModel,ref:Record<string,Object>):string {
  const target=model.selected,from=Wire.text(ref,'machine');
  if(target.kind==='conversation'||Wire.bool(target.data(),'assistant'))return from?fmt('chat_forward_route_from_persona',from):tr('chat_forward_route_persona');
  const to=model.forwardDirectory().machines.find(r=>r.id===target.machine)?.title||'',agent=label(target);
  if(!to)return fmt('chat_forward_route_agent',agent);
  return from && from!==to ? fmt('chat_forward_route_cross',from,agent).replace('{3}',to):fmt('chat_forward_route_on',agent,to);
}
function when(row:ResourceRow):number {
  const value=row.data()['updated_at'];
  if(typeof value==='number')return value;
  return typeof value==='string' ? Date.parse(value)||0 : 0;
}
function fmt(key:string,a:string,b:string=''):string {return tr(key).replace('{1}',a).replace('{2}',b);}

@ObservedV2
export class PhoneChatForward {
  @Trace picked:string[]=[];
  @Trace include:boolean=true;
  @Trace query:string='';
  @Trace choosing:boolean=false;
  @Trace expanded:boolean=false;
  @Trace picking:boolean=false;
  @Trace busy:boolean=false;
  @Trace error:string='';
  @Trace recent:string[]=[];
  @Trace newDraft:CompletedForward|undefined=undefined;
  private source:ClientModel|undefined=undefined;
  private generation:number=0;
  private scope:string='';
  private target:string='';
  private group:string='';
  private ticket:number=0;
  private preferenceRevision:number=0;
  private single:string='';
  private offeredRows:ResourceRow[]=[];
  private cachedSource:ClientModel|undefined=undefined;
  private cachedMessages:ChatMessage[]|undefined=undefined;
  private items:Map<string,TranscriptItem>=new Map<string,TranscriptItem>();
  private eligible:Set<string>=new Set<string>();
  private questions:Map<string,string[]>=new Map<string,string[]>();

  private index(source:ClientModel):void {
    if(this.cachedSource===source && this.cachedMessages===source.messages)return;
    this.cachedSource=source;this.cachedMessages=source.messages;
    this.items.clear();this.eligible.clear();this.questions.clear();
    const messages=new Map<string,ChatMessage>();
    const target=source.target();
    for(const m of source.messages)if(m.ownerTarget===target && m.text.trim() && !m.held && !m.localOperation &&
      !m.deliveryState && ['user','assistant'].includes(m.role))messages.set(itemID(m),m);
    let waiting:string[]=[],answered=false;
    for(const item of source.transcriptItems()){
      this.items.set(item.id,item);
      if(item.wake){if(answered)waiting=[];answered=false;}
      if(!messages.has(item.id) || item.kind!=='text' || item.wake || item.role==='user' && (item.text||'').startsWith('/loop'))continue;
      if(item['injected'] && !this.relay(item))continue;
      this.eligible.add(item.id);
      if(item.role==='user'){if(answered)waiting=[];answered=false;waiting.push(item.id);}
      else if(item.role==='assistant'){this.questions.set(item.id,waiting.slice());answered=true;}
    }
  }
  private relay(item:TranscriptItem):string {
    if(item.role!=='user')return '';
    const d=Wire.record(item as Object),src=Wire.object(d,'source'),inj=Wire.object(d,'injected');
    const kind=Wire.text(src,'kind') || (Wire.text(inj,'kind')==='followup' ?
      (['daily','interval'].includes(Wire.text(inj,'state'))?'patrol':'followup') :
      Wire.text(inj,'kind')==='system' && Wire.text(inj,'state')==='supervise'?'supervision':'');
    if(!kind)return Wire.text(d,'from');
    const name=Wire.text(src,'title')||Wire.text(inj,'title')||tr('chat_forward_source_'+(['conversation','patrol','followup','scheduled_task','supervision','meeting'].includes(kind)?kind:'system'));
    return kind==='scheduled_task'?name:'@'+name;
  }

  private valid(source:ClientModel):boolean {
    return this.source===source && this.group===source.profile.id && source.owns(this.generation,this.scope) && source.target()===this.target;
  }
  isChoosing(source:ClientModel):boolean { return this.choosing && this.valid(source); }
  isPicking(source:ClientModel):boolean { return this.picking && this.valid(source); }
  isEligible(source:ClientModel,message:ChatMessage):boolean {
    if(message.ownerTarget!==source.target())return false;
    this.index(source);return this.eligible.has(itemID(message));
  }
  private begin(source:ClientModel,message:ChatMessage):boolean {
    if(!source.connected || !this.isEligible(source,message))return false;
    this.end();this.source=source;this.group=source.profile.id;this.scope=source.scope();
    this.target=source.target();this.generation=source.connectionGeneration();this.single=itemID(message);
    return true;
  }
  startForward(source:ClientModel,message:ChatMessage):void {
    if(this.begin(source,message))this.openPicker(source);
  }
  startPicking(source:ClientModel,message:ChatMessage):void {
    if(!this.begin(source,message))return;
    this.picked=[itemID(message)];this.picking=true;this.readInclude(source);
  }
  selectInstead(source:ClientModel):void {
    if(!this.isChoosing(source)||this.picking)return;
    const m=source.messages.find(v=>itemID(v)===this.single);
    if(m)this.startPicking(source,m);
  }
  private readInclude(source:ClientModel):void {
    const rev=this.preferenceRevision, ticket=this.ticket;
    source.loadView('pickIncludeQuestions').then(v=>{
      if(this.valid(source) && this.ticket===ticket && this.preferenceRevision===rev && v==='false')this.include=false;
    }).catch(()=>{});
  }
  private brought(source:ClientModel,ids:string[]):string[] {
    this.index(source);
    const got=new Set<string>();
    for(const id of ids)for(const q of this.questions.get(id)||[])if(!ids.includes(q))got.add(q);
    return Array.from(got);
  }
  offersQuestions(source:ClientModel):boolean {return this.isPicking(source) && this.brought(source,this.picked).length>0;}
  isBrought(source:ClientModel,m:ChatMessage):boolean {
    return this.isPicking(source) && this.include && this.brought(source,this.picked).includes(itemID(m));
  }
  isPicked(source:ClientModel,m:ChatMessage):boolean {
    return this.isPicking(source) && (this.picked.includes(itemID(m)) || this.isBrought(source,m));
  }
  toggle(source:ClientModel,m:ChatMessage):void {
    if(!this.isPicking(source)||this.choosing||!this.isEligible(source,m))return;
    const id=itemID(m),ids=this.picked.includes(id)?this.picked.filter(q=>q!==id):this.picked.concat(id);
    if(ids.length+(this.include?this.brought(source,ids).length:0)>forwardMaximum){this.error='chat_forward_limit';return;}
    this.error='';this.picked=ids;
  }
  setInclude(source:ClientModel,value:boolean):void {
    if(!this.isPicking(source)||this.choosing)return;
    if(value && this.picked.length+this.brought(source,this.picked).length>forwardMaximum){this.error='chat_forward_limit';return;}
    this.error='';this.include=value;this.preferenceRevision++;source.saveView('pickIncludeQuestions',String(value)).catch(()=>{});
  }
  selected(source:ClientModel):ChatMessage[] {
    if(!this.valid(source))return [];
    const ids=new Set<string>(this.picking ? this.picked.concat(this.include?this.brought(source,this.picked):[]) : [this.single]);
    return source.messages.filter(m=>ids.has(itemID(m)) && this.isEligible(source,m));
  }
  private openPicker(source:ClientModel):void {
    this.query='';this.error='';this.busy=false;this.expanded=false;this.choosing=true;this.recent=[];
    const directory=source.forwardDirectory(), found=new Set<string>();
    this.offeredRows=directory.conversations.concat(directory.sessions).filter(r=>{
      const key=address(r);if(!source.canForwardTo(r)||found.has(key))return false;found.add(key);return true;
    });
    const ticket=this.ticket;
    source.loadView('forwardTargets').then(raw=>{
      if(raw && this.valid(source) && this.choosing && this.ticket===ticket) {
        const rows=JSON.parse(raw) as string[];if(Array.isArray(rows))this.recent=rows.filter(v=>typeof v==='string').slice(0,8);
      }
    }).catch(()=>{});
  }
  forwardPicks(source:ClientModel):void {if(this.isPicking(source) && this.picked.length && !this.busy)this.openPicker(source);}
  cancelPicker():void {this.ticket++;this.choosing=false;this.query='';this.busy=false;this.error='';if(!this.picking)this.end();}
  end():void {
    this.ticket++;this.preferenceRevision++;this.source=undefined;this.newDraft=undefined;this.single='';this.picking=false;this.choosing=false;
    this.busy=false;this.picked=[];this.recent=[];this.offeredRows=[];this.expanded=false;this.cachedSource=undefined;this.cachedMessages=undefined;this.items.clear();this.eligible.clear();this.questions.clear();this.error='';this.include=true;this.query='';
  }
  main(source:ClientModel):ResourceRow|undefined {
    const dir=source.forwardDirectory();
    return this.offeredRows.find(r=>r.kind==='conversation' && r.id===dir.mainId && (!dir.mainMachineId||r.machine===dir.mainMachineId) && source.canForwardTo(r));
  }
  detail(source:ClientModel,row:ResourceRow,showMachine:boolean):string {
    if(row.kind==='conversation'||Wire.bool(row.data(),'assistant'))return 'Persona';
    const d=row.data(),machine=showMachine?source.forwardDirectory().machines.find(m=>m.id===row.machine)?.title||row.machine:'';
    return [label(row),Wire.text(d,'space_name')||row.path.split('/').filter(v=>!!v).pop()||'',machine].filter(v=>!!v).join(' · ');
  }
  sections(source:ClientModel):ForwardSection[] {
    if(!this.isChoosing(source))return [];
    const main=this.main(source),terms=this.query.trim().toLocaleLowerCase().split(/\s+/).filter(v=>!!v);
    const rows=this.offeredRows.filter(r=>r!==main && source.canForwardTo(r,terms.length>0));
    const latest=(a:ResourceRow,b:ResourceRow):number=>when(b)-when(a);
    if(terms.length){
      const found=rows.filter(r=>{const hay=(r.title+' '+this.detail(source,r,true)).toLocaleLowerCase();return terms.every(t=>hay.includes(t));}).sort(latest);
      return found.length?[new ForwardSection('results','',found)]:[];
    }
    const sections:ForwardSection[]=[],recent:ResourceRow[]=[];
    for(const key of this.recent){const row=rows.find(r=>address(r)===key);if(row && recent.length<5)recent.push(row);}
    if(recent.length)sections.push(new ForwardSection('recent',tr('chat_forward_recent'),recent));
    for(const computer of source.forwardDirectory().machines){
      const group=rows.filter(r=>r.kind!=='conversation'&&!Wire.bool(r.data(),'assistant')&&r.machine===computer.id&&!recent.includes(r)).sort(latest);
      if(group.length)sections.push(new ForwardSection(computer.id,computer.title,group));
    }
    return sections;
  }
  private async raw(source:ClientModel,m:ChatMessage,full:boolean,admit:()=>boolean):Promise<string> {
    this.index(source);const original=this.items.get(itemID(m));
    if(!original || !admit())throw new Error('request_updated');
    if(!full || original['full_size']===undefined && original['fullSize']===undefined)return original.text || m.text;
    const row=source.selected,route='/api/v1/sessions/'+segment(row.machine)+'/'+segment(row.session||row.id)+'/items/'+segment(original.id)+'/full';
    const whole=await source.repo().call(route);
    if(!admit() || typeof whole['text']!=='string')throw new Error('request_updated');
    return Wire.text(whole,'text');
  }
  private ref(source:ClientModel,m:ChatMessage,raw:string,count:number,uncut:boolean=false):Record<string,Object> {
    this.index(source);const row=source.selected,d=row.data(),item=this.items.get(itemID(m));
    const data=Wire.record(item as Object|undefined), assistant=row.kind==='conversation'||Wire.bool(d,'assistant');
    const relay=item?this.relay(item):Wire.text(data,'from'), role=relay?'other':m.role==='user'?'user':assistant?'persona':'agent';
    const author=role==='user'?tr('chat_forward_me'):relay||(assistant?'Persona':label(row));
    const machine=assistant?'':source.forwardDirectory().machines.find(r=>r.id===row.machine)?.title||'';
    const rawTime=Wire.text(data,'ts'),time=m.at || (rawTime?Date.parse(rawTime):Wire.number(data,'ts'));
    const project=assistant?'':[Wire.text(d,'space_name'),sessionSpaceSlotLabel(d)].filter(v=>!!v).join(' › ');
    const injected=Wire.object(data,'injected');
    if(relay && Wire.text(injected,'kind')==='followup')raw=Wire.text(injected,'detail',Wire.text(injected,'line',raw));
    const text=forwardedBody(raw,machine,uncut?Number.MAX_SAFE_INTEGER:forwardLimit(count),tr('chat_forward_cut'),(n:string,c:string):string=>c?fmt('chat_forward_computer_file',c,n):fmt('chat_forward_file',n),parseMessageContent);
    const block=makeForward({machineID:row.machine,sessionID:row.session||row.id,item:itemID(m),role,author,chat:row.title,
      project,machine,time:forwardTimestamp(time)},text,fmt('chat_forward_from',author)+(row.title?' · '+row.title:''));
    return Wire.record(block as Object);
  }
  previews(source:ClientModel):Record<string,Object>[] {
    const selected=this.selected(source);return selected.map(m=>this.ref(source,m,this.items.get(itemID(m))?.text||m.text,selected.length));
  }
  async complete(source:ClientModel,photos:boolean,admit:()=>boolean,uncut:boolean=false):Promise<CompletedForward> {
    const selected=this.selected(source);if(!selected.length || selected.length>forwardMaximum)throw new Error('request_updated');
    const ready=new CompletedForward();
    for(const m of selected){
      const raw=await this.raw(source,m,true,admit);if(!admit())throw new Error('request_updated');
      const ref=this.ref(source,m,raw,selected.length,uncut);ready.refs.push(ref);
      if(photos && Wire.text(ref,'role')==='user'){
        for(const path of parseMessageContent(raw).images){
          if(ready.images.length>=4)break;
          try{
            const data=await loadMessageImage(source,itemID(m),path);if(!admit())throw new Error('request_updated');
            const encoded=await ChatAttachments.forwardImage(data.data),file=new DraftForwardImage();
            file.id=util.generateRandomUUID();file.item=itemID(m);file.mime=encoded.mime;file.data=encoded.data;ready.images.push(file);
          }catch(_){if(!admit())throw new Error('request_updated');}
        }
      }
    }
    if(!admit())throw new Error('request_updated');return ready;
  }
  async choose(source:ClientModel,row:ResourceRow):Promise<boolean> {
    if(!this.isChoosing(source)||this.busy||!this.offeredRows.includes(row)||!source.canForwardTo(row))return false;
    const ticket=++this.ticket,admit=():boolean=>ticket===this.ticket && this.isChoosing(source) && source.canForwardTo(row);
    this.busy=true;this.error='';
    try{
      const ready=await this.complete(source,true,admit);
      await source.receiveForward(row,ready.refs,ready.images,ready.transfer,admit);
      if(!admit())return false;
      const key=address(row),recent=[key,...this.recent.filter(k=>k!==key)].slice(0,8);
      source.saveView('forwardTargets',JSON.stringify(recent)).catch(()=>{});this.end();return true;
    }catch(e){if(admit())this.error=(e as Error).message||'failed';return false;}
    finally{if(this.ticket===ticket)this.busy=false;}
  }
  async chooseNewAgent(source:ClientModel):Promise<boolean> {
    if(!this.isChoosing(source)||this.busy)return false;
    const ticket=++this.ticket,admit=():boolean=>ticket===this.ticket && this.isChoosing(source);
    this.busy=true;
    try{const ready=await this.complete(source,true,admit);if(!admit())return false;this.end();this.source=source;
      this.group=source.profile.id;this.scope=source.scope();this.generation=source.connectionGeneration();this.newDraft=ready;return true;}
    catch(e){if(admit())this.error=(e as Error).message||'failed';return false;}
    finally{if(this.ticket===ticket)this.busy=false;}
  }
  newFor(owner:ClientModel):CompletedForward|undefined {
    return this.newDraft && this.source && this.source.owns(this.generation,this.scope) && owner.profile.id===this.group &&
      owner.profile.hubId===this.source.profile.hubId ? this.newDraft : undefined;
  }
  newAllows(owner:ClientModel,launcher:Record<string,Object>):boolean {
    const ready=this.newFor(owner);
    if(!ready || !ready.images.length)return true;
    // These providers take their first prompt inline. Swift does not submit their start with photos attached.
    const provider=(Wire.text(launcher,'agent')||Wire.text(launcher,'agent_type')||Wire.text(launcher,'name')).toLowerCase();
    return provider!=='codex' && provider!=='omp';
  }
  removeNewPhotos(owner:ClientModel):void {
    const ready=this.newFor(owner);if(!ready)return;
    const updated=new CompletedForward();updated.transfer=ready.transfer;updated.refs=ready.refs;this.newDraft=updated;
  }
  newPrompt(owner:ClientModel,note:string):string {
    const ready=this.newFor(owner);
    if(!ready)return note;
    if(ready.images.length)return '';
    return withSourceContext(note,JSON.stringify(Wire.payload(['contexts'],[ready.refs])),owner.features).trimEnd();
  }
  /** Only run after the user confirms the normal New Agent form and its start has returned a real session. */
  async afterNew(owner:ClientModel,row:ResourceRow,note:string):Promise<void> {
    const ready=this.newFor(owner),source=this.source;
    if(!ready || !source)return;
    if(!ready.images.length){this.clearNew();return;}
    const group=owner.profile.id,scope=owner.scope(),gen=owner.connectionGeneration();
    const valid=():boolean=>owner.owns(gen,scope) && owner.profile.id===group && this.newFor(owner)===ready;
    try{
      await source.receiveForward(row,ready.refs,ready.images,ready.transfer,valid);
      if(!valid())return;
      await owner.select(row);
      if(!valid() || owner.selected.id!==row.id || owner.selected.machine!==row.machine)return;
      if(note)owner.updateDraft(owner.draft?owner.draft+'\n\n'+note:note);
      await owner.persistDraft();
      // A rejected/unknown first send remains in the normal outbox or editable target draft.
      await owner.send(valid);
    }catch(e){if(valid())owner.reportError(e as Error);}
    finally{if(this.newDraft===ready)this.clearNew();}
  }
  clearNew():void {this.newDraft=undefined;}
}
