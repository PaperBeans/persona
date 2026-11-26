import type { ClientModel } from './ClientModel';
import { Wire } from './Wire';

type Fields = Record<string,Object>;
const DOCUMENT = '/api/v1/kb/selection';
export const MESSAGE_RULES:string[]=['mentions','mine','dm','group','bot','official','muted','collapsed','done'];
export const DOC_RULES:string[]=['mentions','comment_mentions','authored','viewed'];
const ITEM_FIELDS:string[]=['source','kind','id','name','read','store','days'];

function record(value:Object|undefined):boolean { return !!value && typeof value==='object' && !Array.isArray(value); }
function only(value:Fields,names:string[]):boolean { return Object.keys(value).every((key:string) => names.includes(key)); }
function numberIn(value:Object|undefined,lo:number,hi:number):boolean {
  return value==null || typeof value==='number' && Number.isInteger(value) && value>=lo && value<=hi;
}
function copy(value:Fields):Fields { return Wire.parse(JSON.stringify(value)); }
function applyFields(current:Fields,desired:Fields,keys:string[]):Fields {
  const result:Fields={};
  Object.keys(current).forEach((key:string) => { if (!keys.includes(key)) result[key]=current[key]; });
  keys.forEach((key:string) => { if (desired[key]!=null) result[key]=desired[key]; });
  return result;
}
function canonical(value:Object|undefined):string {
  if (value==null) return 'null';
  if (Array.isArray(value)) return '['+(value as Object[]).map((v:Object) => canonical(v)).join(',')+']';
  if (typeof value==='object') {
    const r=value as Fields;
    return '{'+Object.keys(r).sort().map((k:string) => JSON.stringify(k)+':'+canonical(r[k])).join(',')+'}';
  }
  return JSON.stringify(value);
}
function same(a:Object|undefined,b:Object|undefined):boolean { return canonical(a)===canonical(b); }
function nested(document:Fields,part:string):Fields { return Wire.object(document,part); }
function rules(document:Fields,part:string):Fields { return Wire.object(nested(document,part),'rules'); }
export function selectionOrder(document:Fields,part:string):string[][] {
  const order=nested(document,part)['order'];
  return Array.isArray(order) ? order as string[][] : [];
}
export function selectionItemKey(item:Fields):string {
  return JSON.stringify([Wire.text(item,'source'),Wire.text(item,'kind'),Wire.text(item,'id')]);
}
function selectionItem(document:Fields,key:string):Fields|undefined {
  return Wire.rows(document,'items').find((entry:Fields) => selectionItemKey(entry)===key);
}
export function selectionItemMode(item:Fields):string { return !Wire.bool(item,'read') ? 'skip' : Wire.bool(item,'store') ? 'all' : 'rules'; }
export function selectionNumber(raw:string,min:number,max:number):number {
  const text=raw.trim();
  if (!text) return -1;
  if (!/^[0-9]+$/.test(text)) return -2;
  const n=Number(text);
  return Number.isInteger(n) && n>=min && n<=max ? n : -2;
}
export function validSelection(document:Fields):boolean {
  if (document['version']!==1 || !only(document,['version','messages','docs','items','history'])) return false;
  for (const part of ['messages','docs']) {
    const group=nested(document,part), entries=rules(document,part), names=part==='messages' ? MESSAGE_RULES : DOC_RULES;
    const order=selectionOrder(document,part);
    if (!only(group,['order','rules']) || !Array.isArray(group['order']) || !record(group['rules']) ||
      !order.length || !order.every((level:string[]) => Array.isArray(level) && level.length>0 &&
        level.every((v:string) => names.includes(v)))) return false;
    const flat=order.flat();
    if (flat.length!==names.length || new Set<string>(flat).size!==names.length ||
      Object.keys(entries).length!==names.length || !only(entries,names)) return false;
    for (const name of names) {
      const rule=Wire.object(entries,name);
      if (!only(rule,['on','days','attachment_days','include_all','context']) || typeof rule['on']!=='boolean' ||
        !numberIn(rule['days'],1,36500) || !numberIn(rule['attachment_days'],1,36500) ||
        part==='docs' && rule['attachment_days']!=null ||
        rule['include_all']!=null && (part!=='messages' || name!=='mentions' || typeof rule['include_all']!=='boolean') ||
        rule['context']!=null && (part!=='messages' || name!=='mine' || !numberIn(rule['context'],0,200))) return false;
    }
  }
  const items=document['items'];
  if (items!=null && !Array.isArray(items)) return false;
  const keys=new Set<string>();
  for (const item of Wire.rows(document,'items')) {
    const key=selectionItemKey(item);
    if (!only(item,ITEM_FIELDS) || !Wire.text(item,'source') || !Wire.text(item,'id') || !['chat','doc'].includes(Wire.text(item,'kind')) ||
      item['name']!=null && typeof item['name']!=='string' || typeof item['read']!=='boolean' || typeof item['store']!=='boolean' ||
      Wire.bool(item,'store') && !Wire.bool(item,'read') || !numberIn(item['days'],1,36500) || keys.has(key)) return false;
    keys.add(key);
  }
  if (document['history']!=null && !record(document['history'])) return false;
  const history=Wire.object(document,'history');
  return Object.keys(history).every((key:string) => !!key && record(history[key]) &&
    only(Wire.object(history,key),['back_days']) && numberIn(Wire.object(history,key)['back_days'],1,36500));
}

export class KnowledgeMapData {
  items:Fields[]=[];
  details:Map<string,Fields>=new Map<string,Fields>();
  failed:number=0;
  curating:boolean|undefined=undefined;
}

@ObservedV2
export class PhoneKnowledgeSelectionModel {
  @Trace data:Fields={};
  @Trace loading:boolean=false;
  @Trace saving:boolean=false;
  @Trace problem:string='';
  @Trace topicsOn:boolean|undefined=undefined;
  @Trace topicProblem:string='';
  @Trace topicSaving:boolean=false;
  @Trace entries:Fields[]=[];
  @Trace pickerBusy:boolean=false;
  @Trace pickerError:string='';
  @Trace mapMode:string='topics';
  @Trace mapRows:Fields[]=[];
  @Trace mapDetails:Fields={};
  @Trace mapBusy:boolean=false;
  @Trace mapError:string='';
  private client:ClientModel|undefined;
  private scope:string='';
  private generation:number=-1;
  private epoch:number=0;
  private reads:number=0;
  private picks:number=0;
  private mapReads:number=0;
  private topicReads:number=0;
  private pickerKind:string='';
  private offEvents:(()=>void)|undefined;

  private path(base:string):string {
    const manager=this.client?.profile.manager || '';
    return manager ? base+(base.includes('?') ? '&' : '?')+'manager='+encodeURIComponent(manager) : base;
  }
  private live(epoch:number):boolean { return !!this.client && epoch===this.epoch && this.client.connected && this.client.owns(this.generation,this.scope); }
  async open(client:ClientModel,mapOnly:boolean=false):Promise<void> {
    this.close(); this.client=client; this.scope=client.scope(); this.generation=client.connectionGeneration();
    if (!client.connected) { this.problem='offline'; return; }
    if (mapOnly) return;
    const epoch=this.epoch;
    this.offEvents=client.observeEvents((raw:string) => {
      if (!this.live(epoch) || this.saving || raw.length>1024*1024) return;
      const frame=Wire.parse(raw);
      if (Wire.text(frame,'type')==='kb_selection' && (!Wire.text(frame,'manager') || Wire.text(frame,'manager')===client.profile.manager)) this.load();
    });
    await Promise.all([this.load(),this.loadTopics()]);
  }
  close():void {
    this.offEvents?.(); this.offEvents=undefined; this.epoch++; this.reads++; this.picks++; this.mapReads++; this.topicReads++; this.client=undefined;
    this.loading=false; this.saving=false; this.problem=''; this.data={}; this.topicsOn=undefined; this.topicSaving=false;
    this.topicProblem=''; this.entries=[]; this.pickerBusy=false; this.pickerError=''; this.mapMode='topics'; this.mapRows=[]; this.mapDetails={}; this.mapBusy=false; this.mapError='';
  }
  private selectionFailure(error:Error,loading:boolean):string {
    const e=error as Object as Fields;
    if (Wire.number(e,'status')===404) return 'ks_old_hub';
    if (Wire.number(e,'status')===403) return 'permission_denied';
    if (Wire.bool(e,'resultUnknown')) return 'result_unknown';
    return loading ? 'ks_load_error' : 'ks_save_error';
  }
  private checked(doc:Fields):Fields {
    if (!validSelection(doc)) throw new Error('ks_future');
    return doc;
  }
  async load():Promise<void> {
    const client=this.client, epoch=this.epoch, read=++this.reads;
    if (!client || !this.live(epoch) || this.saving) return;
    this.loading=true; this.problem='';
    try {
      const data=await client.repo().call(this.path(DOCUMENT));
      if (this.live(epoch) && read===this.reads && !this.saving) {
        this.data=this.checked(data);
      }
    } catch (failure) {
      if (this.live(epoch) && read===this.reads) {
        const why=failure as Error;
        this.problem=why.message==='ks_future' ? 'ks_future' : this.selectionFailure(why,true);
        if (['ks_future','ks_old_hub'].includes(this.problem)) this.data={};
      }
    } finally { if (this.epoch===epoch && read===this.reads) this.loading=false; }
  }
  rule(part:string,name:string):Fields { return Wire.object(rules(this.data,part),name); }
  levels(part:string):string[][] { return selectionOrder(this.data,part); }
  items():Fields[] { return Wire.rows(this.data,'items'); }

  private async change(edit:(document:Fields)=>boolean):Promise<boolean> {
    const client=this.client, epoch=this.epoch, path=this.path(DOCUMENT);
    if (!client || !this.live(epoch) || !Object.keys(this.data).length || this.saving) return false;
    if (client.operationBlocked(path)) { this.problem='result_unknown'; return false; }
    const before=this.data, shown=copy(before);
    if (!edit(shown)) { this.problem='ks_changed'; return false; }
    if (!validSelection(shown)) { this.problem='ks_invalid'; return false; }
    if (same(shown,before)) return true;
    this.reads++; this.loading=false; this.saving=true; this.problem=''; this.data=shown;
    const live=():boolean => this.live(epoch);
    let rollback=before;
    try {
      const current=this.checked(await client.repo().call(path));
      if (!live()) return false;
      rollback=current;
      const next=copy(current);
      if (!edit(next) || !validSelection(next)) { this.data=current; this.problem='ks_changed'; return false; }
      const saved=same(next,current) ? current : this.checked(await client.repo().call(path,'PUT',next,'',live,false,validSelection));
      if (!live()) return false;
      this.data=saved;
      return true;
    } catch (failure) {
      if (live()) {
        const why=failure as Error;
        this.problem=why.message==='ks_future' ? 'ks_future' : this.selectionFailure(why,false);
        this.data=this.problem==='ks_future' || this.problem==='ks_old_hub' ? {} : rollback;
      }
      return false;
    } finally { if (this.epoch===epoch) this.saving=false; }
  }

  saveRule(part:string,name:string,original:Fields,desired:Fields):Promise<boolean> {
    if (!(part==='messages' ? MESSAGE_RULES : part==='docs' ? DOC_RULES : []).includes(name)) return Promise.resolve(false);
    const fields=Array.from(new Set<string>(Object.keys(original).concat(Object.keys(desired)))).filter((k:string) => !same(original[k],desired[k]));
    return this.change((doc:Fields):boolean => {
      const current=Wire.object(rules(doc,part),name);
      if (fields.some((k:string) => !same(current[k],original[k]) && !same(current[k],desired[k]))) return false;
      rules(doc,part)[name]=applyFields(current,desired,fields);
      return true;
    });
  }
  reorder(part:string,rule:string,action:string):Promise<boolean> {
    const original=this.levels(part), anchor=original.find((level:string[]) => level.includes(rule));
    if (!anchor) return Promise.resolve(false);
    return this.change((doc:Fields):boolean => {
      const order=selectionOrder(doc,part), index=order.findIndex((level:string[]) => level.includes(rule));
      if (index<0 || !same(order,original)) return false;
      if (action==='up' || action==='down') {
        const next=index+(action==='up' ? -1 : 1);
        if (next<0 || next>=order.length) return false;
        const prev=order[index]; order[index]=order[next]; order[next]=prev;
      } else if (action==='join' && index>0 || action==='split' && order[index].length>1) {
        order[index]=order[index].filter((name:string) => name!==rule);
        if (action==='join') order[index-1].push(rule); else order.splice(index+1,0,[rule]);
        nested(doc,part)['order']=order.filter((level:string[]) => level.length>0);
      } else return false;
      return true;
    });
  }
  saveItem(original:Fields|undefined,desired:Fields):Promise<boolean> {
    const key=selectionItemKey(desired);
    if (original && selectionItemKey(original)!==key) return Promise.resolve(false);
    return this.change((doc:Fields):boolean => {
      const current=selectionItem(doc,key);
      if (!current && original || current && !original && !same(current,desired)) return false;
      const items=Wire.rows(doc,'items'), index=items.findIndex((item:Fields) => selectionItemKey(item)===key);
      if (current && original) {
        const changed=['read','store','days','name'].filter((field:string) => !same(original[field],desired[field]));
        if (changed.some((field:string) => !same(current[field],original[field]) && !same(current[field],desired[field]))) return false;
        items[index]=applyFields(current,desired,changed);
      } else if (!current) items.push(copy(desired));
      doc['items']=items; return true;
    });
  }
  removeItem(original:Fields):Promise<boolean> {
    const key=selectionItemKey(original);
    return this.change((doc:Fields):boolean => {
      const current=selectionItem(doc,key);
      if (current && !same(current,original)) return false;
      doc['items']=Wire.rows(doc,'items').filter((e:Fields) => selectionItemKey(e)!==key); return true;
    });
  }

  cancelPicker():void { this.picks++; this.pickerKind=''; this.pickerBusy=false; this.entries=[]; this.pickerError=''; }
  async listEntries(kind:string):Promise<void> {
    this.cancelPicker(); if (kind!=='chat' && kind!=='doc') return;
    const client=this.client, epoch=this.epoch, pick=this.picks;
    if (!client || !this.live(epoch)) return;
    this.pickerKind=kind; this.pickerBusy=true;
    try {
      const data=await client.repo().call(this.path('/api/v1/kb/'+(kind==='chat' ? 'chats' : 'docs')));
      if (this.live(epoch) && pick===this.picks) this.entries=Wire.rows(data,'items').filter((e:Fields) => !!Wire.text(e,'id'));
    } catch (_) { if (this.live(epoch) && pick===this.picks) this.pickerError='ks_load_error'; }
    finally { if (pick===this.picks) this.pickerBusy=false; }
  }
  async pick(kind:string,entry:Fields):Promise<Fields|undefined> {
    const client=this.client, epoch=this.epoch, id=Wire.text(entry,'id');
    if (!client || this.pickerBusy || !this.live(epoch) || kind!==this.pickerKind || !this.entries.some((row:Fields) => Wire.text(row,'id')===id)) return undefined;
    const pick=++this.picks; this.pickerBusy=true; this.pickerError='';
    try {
      const result=await client.repo().call(this.path('/api/v1/kb/'+(kind==='chat' ? 'chats' : 'docs')+'/'+encodeURIComponent(id)));
      if (!this.live(epoch) || pick!==this.picks) return undefined;
      const meta=Wire.object(Wire.object(result,'detail'),'meta'), source=Wire.text(meta,'source'), stable=Wire.text(meta,kind==='chat' ? 'source_id' : 'id');
      if (!source || !stable) { this.pickerError='ks_no_id'; return undefined; }
      if (kind==='chat' && !['dm','group','bot','official','system'].includes(Wire.text(meta,'kind'))) { this.pickerError='ks_not_selectable'; return undefined; }
      const row=Wire.payload(['source','kind','id','read','store'],[source,kind,stable,true,false]);
      if (Wire.text(entry,'name')) row['name']=Wire.text(entry,'name');
      return copy(selectionItem(this.data,selectionItemKey(row)) || row);
    } catch (_) { if (this.live(epoch) && pick===this.picks) this.pickerError='ks_load_error'; return undefined; }
    finally { if (pick===this.picks) this.pickerBusy=false; }
  }

  private async loadTopics():Promise<void> {
    const client=this.client, epoch=this.epoch, read=++this.topicReads;
    if (!client || !this.live(epoch)) return;
    try { const result=await client.repo().call(this.path('/api/v1/assistant'));
      if (this.live(epoch) && read===this.topicReads) this.topicsOn=Wire.bool(result,'kb_topics',true);
    } catch (_) { if (this.live(epoch) && read===this.topicReads) this.topicProblem='ks_load_error'; }
  }
  async setTopics(on:boolean):Promise<void> {
    const client=this.client, epoch=this.epoch, previous=this.topicsOn;
    if (!client || !this.live(epoch) || previous===undefined || previous===on || this.topicSaving) return;
    this.topicSaving=true; this.topicsOn=on; this.topicProblem='';
    try {
      const path=this.path('/api/v1/assistant');
      if (client.operationBlocked(path)) throw new Error('result_unknown');
      const response=await client.repo().call(path,'PUT',Wire.payload(['kb_topics'],[on]),'',() => this.live(epoch));
      if (this.live(epoch)) this.topicsOn=Wire.bool(response,'kb_topics',on);
    } catch (error) { if (this.live(epoch)) { this.topicsOn=previous; this.topicProblem=this.selectionFailure(error as Error,false); } }
    finally { if (this.epoch===epoch) this.topicSaving=false; }
  }
  /** Load the list and its real child details as a single ownership-checked outline snapshot. */
  async readOutline(mode:string):Promise<KnowledgeMapData|undefined> {
    const client=this.client, epoch=this.epoch, read=++this.mapReads;
    if (!client || !this.live(epoch) || (mode!=='topics' && mode!=='projects')) return undefined;
    const current=():boolean=>this.live(epoch) && read===this.mapReads;
    const path='/api/v1/kb/'+mode;
    const response=await client.repo().call(this.path(path));
    if (!current()) return undefined;
    const result=new KnowledgeMapData();result.items=Wire.rows(response,'items');
    let next=0;
    const fetchNext=async():Promise<void>=>{
      while (current() && next<result.items.length) {
        const id=Wire.text(result.items[next++],'id');
        if (!id) continue;
        try {
          const full=await client.repo().call(this.path(path+'/'+encodeURIComponent(id)));
          if (current()) {
            if (record(full['detail'])) result.details.set(id,Wire.object(full,'detail'));
            else result.failed++;
          }
        } catch (_) { if (current()) result.failed++; }
      }
    };
    const workers:Promise<void>[]=[];
    for (let i=0;i<Math.min(6,result.items.length);i++) workers.push(fetchNext());
    await Promise.all(workers);
    if (mode==='topics' && current()) {
      try {
        const settings=await client.repo().call(this.path('/api/v1/assistant'));
        if (current() && typeof settings['kb_topics']==='boolean') result.curating=Wire.bool(settings,'kb_topics');
      } catch (_) {}
    }
    return current() ? result : undefined;
  }

  async map(mode:string,entry:string=''):Promise<void> {
    if (!['topics','projects'].includes(mode)) return;
    const client=this.client,epoch=this.epoch,read=++this.mapReads;
    if (!client || !this.live(epoch)) return;
    if (this.mapMode!==mode || !entry) { this.mapRows=[]; this.mapDetails={}; }
    this.mapMode=mode; this.mapBusy=true; this.mapError='';
    try {
      const response=await client.repo().call(this.path('/api/v1/kb/'+mode+(entry ? '/'+encodeURIComponent(entry) : '')));
      if (!this.live(epoch) || read!==this.mapReads) return;
      if (!entry) this.mapRows=Wire.rows(response,'items');
      else {
        const d=copy(this.mapDetails); d[entry]=Wire.object(response,'detail'); this.mapDetails=d;
      }
    } catch (_) { if (this.live(epoch) && read===this.mapReads) this.mapError='ks_load_error'; }
    finally { if (read===this.mapReads) this.mapBusy=false; }
  }
}
