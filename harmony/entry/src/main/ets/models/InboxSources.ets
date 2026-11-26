import { ClientModel } from './ClientModel';
import { Wire } from './Wire';
import { segment } from './HubRepository';

export class InboxSourcesFrame {
  serial:number;
  graph:boolean;
  state:string;
  title:string;
  constructor(serial:number,graph:boolean,state:string,title:string) { this.serial=serial; this.graph=graph; this.state=state; this.title=title; }
}

/** The settings toolbar and system Back use the same stack as the manager-drawn pages. */
@ObservedV2
export class InboxSourcesNavigation {
  @Trace frames: InboxSourcesFrame[] = [];
  @Trace title: string = '';
  private serial: number = 0;

  reset(): void { this.frames = []; this.title = ''; this.push('', ''); }
  push(state:string, title:string, graph:boolean = false): void {
    if (this.frames.length >= 24) return;
    this.frames = this.frames.concat(new InboxSourcesFrame(++this.serial, graph, state, title));
    this.title = title;
  }
  current(): InboxSourcesFrame | undefined { return this.frames[this.frames.length-1]; }
  setTitle(serial:number,title:string):void {
    const top=this.current();
    if (!top || top.serial!==serial || !title || this.title===title) return;
    this.frames=this.frames.slice(0,-1).concat(new InboxSourcesFrame(serial,top.graph,top.state,title)); this.title=title;
  }
  back():boolean {
    if (this.frames.length < 2) return false;
    this.frames=this.frames.slice(0,-1); this.title=this.current()!.title; return true;
  }
}

export class InboxSourceGroup {
  entry:Record<string,Object>;
  sources:Record<string,Object>[];
  constructor(entry:Record<string,Object>,sources:Record<string,Object>[]) { this.entry=entry; this.sources=sources; }
}

@ObservedV2
export class InboxSourcesGraph {
  @Trace data: Record<string,Object> = {};
  @Trace filter: string = 'all';
  @Trace selected: string = '';
  @Trace loading: boolean = false;
  @Trace error: string = '';
  private revision: number = 0;

  clear():void { this.revision++; this.data={}; this.selected=''; this.filter='all'; this.loading=false; this.error=''; }
  sources():Record<string,Object>[] { return Wire.rows(this.data,'sources'); }
  item(kind:string,id:string):Record<string,Object> {
    return Wire.rows(this.data,kind).find((entry:Record<string,Object>)=>Wire.text(entry,'id')===id) || {};
  }
  destinations(row:Record<string,Object>):string[] { return Wire.strings(row,'to'); }
  matches(row:Record<string,Object>,filter:string):boolean {
    return filter==='all' || (filter==='idle' ? !Wire.bool(row,'reading') : this.destinations(row).includes(filter));
  }
  count(filter:string):number { return this.sources().filter((row:Record<string,Object>)=>this.matches(row,filter)).length; }
  groups():InboxSourceGroup[] {
    const owners=new Map<string,string>();
    for (const read of Wire.rows(this.data,'reads')) {
      const key=Wire.text(read,'id'); if (!owners.has(key)) owners.set(key,Wire.text(read,'extension'));
    }
    const groups:InboxSourceGroup[]=[], byOwner=new Map<string,InboxSourceGroup>();
    for (const entry of Wire.rows(this.data,'extensions')) {
      const group=new InboxSourceGroup(entry,[]); groups.push(group); byOwner.set(Wire.text(entry,'id'),group);
    }
    for (const source of this.sources()) {
      if (this.matches(source,this.filter)) byOwner.get(owners.get(Wire.text(source,'read')) || '')?.sources.push(source);
    }
    return groups.filter((group:InboxSourceGroup)=>group.sources.length>0);
  }
  async load(model:ClientModel):Promise<void> {
    const revision=++this.revision, generation=model.connectionGeneration(),scope=model.scope();
    const current=():boolean=>revision===this.revision && model.owns(generation,scope);
    this.error='';
    if (!model.connected || !model.features.includes('inbox_sources_graph')) { this.error='extension_ui_offline';return; }
    this.loading=true;
    try {
      const manager=model.profile.manager, suffix=manager ? '&manager='+segment(manager) : '';
      const next=await model.repo().call('/api/v1/subscriptions/-/graph?ui_version=6'+suffix);
      if (!current()) return;
      this.data=next;
      if (this.selected && !this.sources().some((source:Record<string,Object>)=>Wire.text(source,'id')===this.selected)) this.selected='';
    } catch (failure) {
      if (current() && !Object.keys(this.data).length) this.error='failed';
    } finally { if (current()) this.loading=false; }
  }
}
