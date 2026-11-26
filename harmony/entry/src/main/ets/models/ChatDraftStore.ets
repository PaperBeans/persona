import type { LocalStore } from '../platform/LocalStore';
import { ResourceRow, Wire } from './Wire';

export class ChatDraft {
  text: string = '';
  context: string = '';
  revision: string = '';
}
/** A prepared forwarded image is the same MIME + base64 pair as a normal picked image. */
export class DraftForwardImage {
  id:string='';
  mime:string='';
  data:string='';
  item:string='';
}
const FORWARD_CHUNK=128*1024;
function draftRows(raw:string):Record<string,Object>[] {
  if (!raw) return [];
  const doc=Wire.parse(raw), rows=Wire.rows(doc,'contexts');
  return rows.length ? rows : Wire.text(doc,'kind') ? [doc] : [];
}
export function draftForwardRows(raw:string):Record<string,Object>[] {
  return draftRows(raw).filter((r:Record<string,Object>)=>Wire.text(r,'kind')==='forward');
}
function savedForwardImages(raw:string):Record<string,Object>[] {
  const result:Record<string,Object>[]=[];
  for(const row of draftForwardRows(raw)) for(const file of Wire.rows(row,'local_images')) {
    if (/^[0-9a-f-]{36}$/i.test(Wire.text(file,'id')) && ['image/jpeg','image/png'].includes(Wire.text(file,'mime')) &&
      Number.isInteger(Wire.number(file,'chunks')) && Wire.number(file,'chunks')>0 && Wire.number(file,'chunks')<=32) result.push(file);
  }
  return result;
}
function imageKey(target:string,id:string,index:number):string { return 'draftmedia:'+target+':'+id+':'+index; }
function removedMedia(target:string,before:string,after:string):string[] {
  const remaining=new Set<string>(savedForwardImages(after).map((f:Record<string,Object>)=>Wire.text(f,'id')));
  const keys:string[]=[];
  for(const f of savedForwardImages(before)) if(!remaining.has(Wire.text(f,'id'))) {
    for(let i=0;i<Wire.number(f,'chunks');i++) keys.push(imageKey(target,Wire.text(f,'id'),i));
  }
  return keys;
}
export class ChatOutboxEntry {
  id: string = '';
  target: string = '';
  revision: string = '';
  text: string = '';
  context: string = '';
  imageCount: number = 0;
  attachmentSnapshot: string = '';
  // Rebound from the local store on read; never trust a persisted process epoch.
  localEpoch: number = 0;
  at: number = 0;
  acceptedAt: number = 0;
  afterSequence: number = 0;
  state: string = 'pending';
  confirmedPending: boolean = false;
  draft: string = '';
}

/** Serialize complete read/modify/write transactions across all detail contexts sharing a database handle. */
export class ChatDraftStore {
  private static queues: Map<LocalStore, Map<string, Promise<void>>> = new Map();
  private local: LocalStore;
  constructor(local: LocalStore) { this.local = local; }
  private async transaction<T>(key: string, action: (epoch: number) => Promise<T>): Promise<T> {
    let queues = ChatDraftStore.queues.get(this.local);
    if (!queues) { queues = new Map(); ChatDraftStore.queues.set(this.local, queues); }
    const epoch = this.local.revision(key), previous = queues.get(key) || Promise.resolve();
    const work = previous.catch(() => {}).then(async (): Promise<T> => {
      if (!this.local.accepts(key, epoch)) throw new Error('scope_retired');
      const result = await action(epoch); if (!this.local.accepts(key, epoch)) throw new Error('scope_retired'); return result;
    }), tail = work.then(() => {}, () => {});
    queues.set(key, tail);
    try { return await work; } finally { if (queues.get(key) === tail) queues.delete(key); if (!queues.size) ChatDraftStore.queues.delete(this.local); }
  }
  private decodeDraft(raw: string): ChatDraft {
    const data = raw ? Wire.record(JSON.parse(raw) as Object) : Wire.payload([], []), draft = new ChatDraft();
    draft.text = Wire.text(data, 'text'); draft.context = Wire.text(data, 'context'); draft.revision = Wire.text(data, 'revision'); return draft;
  }
  async readSessionSnapshot(scope: string): Promise<string> {
    const key = 'snapshot:' + scope; return this.transaction(key, (): Promise<string> => this.local.get(key));
  }
  async saveSessionSnapshot(scope: string, sessions: ResourceRow[], conversations: ResourceRow[], heldActions: Record<string, Object>[], current: () => boolean, sidebarConfig?: string): Promise<void> {
    const key = 'snapshot:' + scope, value = JSON.stringify({ sessions: sessions, conversations: conversations, heldActions: heldActions, sidebarConfig: sidebarConfig });
    await this.transaction(key, async (epoch: number): Promise<void> => {
      if (current()) await this.local.put(key, value, epoch);
    });
  }
  // Commit a definite held receipt before retiring its outbox copy, even if the owner switched chats or managers.
  async cacheHeldMessage(scope: string, target: string, owner: ResourceRow, id: string, held: Record<string, Object> | undefined, paired: () => boolean,
    action?: Record<string, Object>): Promise<void> {
    const key = 'snapshot:' + scope, original = Wire.record(JSON.parse(JSON.stringify(owner)) as Object);
    await this.transaction(key, async (epoch: number): Promise<void> => {
      const raw = await this.local.get(key), snapshot = raw ? Wire.record(JSON.parse(raw) as Object) : Wire.payload([], []);
      if (!paired()) return;
      const actions = Wire.rows(snapshot, 'heldActions');
      const same = (item: Record<string, Object>): boolean => Wire.text(item, 'target') === target && Wire.text(item, 'id') === id;
      if (action) snapshot['heldActions'] = actions.filter((item: Record<string, Object>) => !same(item)).concat([action]);
      else if (actions.some((item: Record<string, Object>) => same(item) && Wire.bool(item, 'deleted'))) return;
      const sessions = Wire.rows(snapshot, 'sessions');
      let saved = sessions.find((row: Record<string, Object>) => Wire.text(row, 'machine') === owner.machine &&
        (Wire.text(row, 'session') || Wire.text(row, 'id')) === (owner.session || owner.id));
      if (!saved && (held || (action && !Wire.bool(action, 'deleted')))) { saved = original; sessions.push(saved); }
      if (saved) {
        const data = Wire.parse(Wire.text(saved, 'raw', '{}')), entries = Wire.rows(data, 'undelivered');
        if (action && Wire.bool(action, 'deleted')) data['undelivered'] = entries.filter((item: Record<string, Object>) => Wire.text(item, 'id') !== id);
        else if (held && !entries.some((item: Record<string, Object>) => Wire.text(item, 'id') === id)) data['undelivered'] = entries.concat([held]);
        saved['raw'] = JSON.stringify(data);
      }
      snapshot['sessions'] = sessions;
      await this.local.put(key, JSON.stringify(snapshot), epoch);
    });
  }
  async read(target: string): Promise<ChatDraft> {
    const key = 'draft:' + target; return this.transaction(key, async (): Promise<ChatDraft> => this.decodeDraft(await this.local.get(key)));
  }
  async save(target: string, draft: ChatDraft): Promise<void> {
    const key = 'draft:' + target, raw = JSON.stringify(draft); return this.transaction(key, async (epoch: number): Promise<void> => this.local.put(key, raw, epoch));
  }
  async appendForwards(target:string,references:Record<string,Object>[],images:DraftForwardImage[],revision:string,
    current:()=>boolean):Promise<ChatDraft> {
    const key='draft:'+target;
    return this.transaction(key,async(epoch:number):Promise<ChatDraft>=>{
      if (!current() || references.length<1 || references.length>30) throw new Error('request_updated');
      const result=this.decodeDraft(await this.local.get(key));
      const refs=draftRows(result.context);
      if(refs.some((r:Record<string,Object>)=>Wire.text(r,'local_transfer')===revision))return result;
      const values=new Map<string,string>();
      let available=Math.max(0,4-savedForwardImages(result.context).length);
      for (const original of references) {
        const item=Wire.text(original,'msg'),origin=Wire.text(original,'url');
        if(item && refs.some((ref:Record<string,Object>)=>Wire.text(ref,'kind')==='forward' && Wire.text(ref,'msg')===item && Wire.text(ref,'url')===origin))continue;
        const entry=Wire.parse(JSON.stringify(original));entry['local_transfer']=revision;
        const photos:Record<string,Object>[]=[];
        for(const accepted of images) {
          if(!available || accepted.item!==Wire.text(entry,'msg') || !accepted.data) continue;
          if(!/^[0-9a-f-]{36}$/i.test(accepted.id) || !['image/jpeg','image/png'].includes(accepted.mime) || accepted.data.length>4*1024*1024) throw new Error('chat_attachment_request_too_large');
          const chunks=Math.ceil(accepted.data.length/FORWARD_CHUNK);
          for(let i=0;i<chunks;i++) values.set(imageKey(target,accepted.id,i),accepted.data.slice(i*FORWARD_CHUNK,(i+1)*FORWARD_CHUNK));
          photos.push(Wire.payload(['id','mime','chunks'],[accepted.id,accepted.mime,chunks]));available--;
        }
        if(photos.length)entry['local_images']=photos;
        refs.push(entry);
      }
      result.context=JSON.stringify(Wire.payload(['contexts'],[refs]));result.revision=revision;
      values.set(key,JSON.stringify(result));
      if(!current())throw new Error('request_updated');
      await this.local.updateRecords(key,epoch,values,[],current);
      return result;
    });
  }
  async forwardedImages(target:string,context:string,available:number,current:()=>boolean):Promise<Object[]> {
    const result:Object[]=[];
    for(const file of savedForwardImages(context).slice(0,Math.max(0,available))) {
      let data='';
      for(let i=0;i<Wire.number(file,'chunks');i++){
        if(!current())throw new Error('request_updated');
        const part=await this.local.get(imageKey(target,Wire.text(file,'id'),i));if(!part)throw new Error('chat_attachment_storage_unavailable');
        data+=part;
      }
      if(!current())throw new Error('request_updated');
      result.push(Wire.payload(['mime','data'],[Wire.text(file,'mime'),data]));
    }
    return result;
  }
  async discardForwardMedia(target:string,before:string,after:string):Promise<void> {
    const keys=removedMedia(target,before,after);if(!keys.length)return;
    const main='draft:'+target;
    await this.transaction(main,async(epoch:number):Promise<void>=>{
      const current=this.decodeDraft(await this.local.get(main));
      const safe=removedMedia(target,before,current.context).filter((k:string)=>keys.includes(k));
      if(safe.length) await this.local.updateRecords(main,epoch,new Map<string,string>(),safe,()=>true);
    });
  }

  async acknowledge(target: string, revision: string): Promise<boolean> {
    if (!revision) return false;
    const key = 'draft:' + target;
    return this.transaction(key, async (epoch: number): Promise<boolean> => {
      const current = this.decodeDraft(await this.local.get(key));
      if (current.revision !== revision) return false;
      const empty=JSON.stringify(new ChatDraft()), remove=removedMedia(target,current.context,'');
      if(remove.length) {
        const values=new Map<string,string>();values.set(key,empty);
        await this.local.updateRecords(key,epoch,values,remove,()=>true);
      } else await this.local.put(key,empty,epoch);
      return true;
    });
  }
  private decodeEntry(data: Record<string, Object>): ChatOutboxEntry {
    const entry = new ChatOutboxEntry(); entry.id = Wire.text(data, 'id'); entry.target = Wire.text(data, 'target');
    entry.revision = Wire.text(data, 'revision'); entry.text = Wire.text(data, 'text'); entry.context = Wire.text(data, 'context');
    entry.draft = Wire.text(data, 'draft', entry.text); entry.confirmedPending = Wire.bool(data, 'confirmedPending');
    entry.attachmentSnapshot = Wire.text(data, 'attachmentSnapshot'); entry.imageCount = Wire.number(data, 'imageCount'); entry.at = Wire.number(data, 'at'); entry.state = Wire.text(data, 'state', 'unknown');
    entry.acceptedAt = Wire.number(data, 'acceptedAt'); entry.afterSequence = Wire.number(data, 'afterSequence'); return entry;
  }
  private async entries(key: string): Promise<ChatOutboxEntry[]> {
    const raw = await this.local.get(key), data = raw ? Wire.record(JSON.parse(raw) as Object) : Wire.payload([], []), entries: ChatOutboxEntry[] = [];
    for (const id of Wire.strings(data, 'ids')) {
      const value = await this.local.get(key + ':' + id);
      if (!value) throw new Error('storage_unavailable');
      entries.push(this.decodeEntry(Wire.record(JSON.parse(value) as Object)));
    }
    return entries;
  }
  private async index(key: string, entries: ChatOutboxEntry[], epoch: number): Promise<void> {
    await this.local.put(key, JSON.stringify(Wire.payload(['ids'], [entries.map((entry: ChatOutboxEntry) => entry.id)])), epoch);
  }
  async list(scope: string): Promise<ChatOutboxEntry[]> {
    const key = 'outbox:' + scope; return this.transaction(key, async (epoch: number): Promise<ChatOutboxEntry[]> => { const entries = await this.entries(key); entries.forEach((entry: ChatOutboxEntry) => entry.localEpoch = epoch); return entries; });
  }
  async stage(scope: string, entry: ChatOutboxEntry): Promise<void> {
    const key = 'outbox:' + scope, captured = this.decodeEntry(Wire.record(JSON.parse(JSON.stringify(entry)) as Object));
    return this.transaction(key, async (epoch: number): Promise<void> => {
      let entries = await this.entries(key);
      const existing = entries.find((old: ChatOutboxEntry) => old.id === captured.id);
      if (existing) { if (existing.target !== captured.target || existing.revision !== captured.revision || existing.text !== captured.text || existing.context !== captured.context || existing.imageCount !== captured.imageCount || existing.attachmentSnapshot !== captured.attachmentSnapshot) throw new Error('request_updated'); return; }
      if (entries.some((old: ChatOutboxEntry) => old.target === captured.target && ['pending', 'unknown'].includes(old.state))) throw new Error('pending_action');
      const active = entries.filter((old: ChatOutboxEntry) => ['pending', 'unknown'].includes(old.state));
      if (active.length >= 128) throw new Error('pending_action');
      const repairs = entries.filter((old: ChatOutboxEntry) => old.state === 'done' && !!old.attachmentSnapshot), terminal = entries.filter((old: ChatOutboxEntry) => !['pending', 'unknown'].includes(old.state) && !(old.state === 'done' && !!old.attachmentSnapshot)), removed = terminal.slice(0, Math.max(0, terminal.length - 32));
      entries = active.concat(repairs, terminal.slice(-32)); entries.push(captured);
      await this.local.put(key + ':' + captured.id, JSON.stringify(captured), epoch); await this.index(key, entries, epoch);
      for (const old of removed) { try { await this.local.remove(key + ':' + old.id, epoch); } catch (_) {} }
    });
  }
  async settle(scope: string, id: string, state: string, acceptedAt: number = 0): Promise<void> {
    if (!['pending', 'unknown', 'failed', 'done', 'checked'].includes(state)) throw new Error('unsupported');
    const key = 'outbox:' + scope;
    return this.transaction(key, async (epoch: number): Promise<void> => {
      const entries = await this.entries(key), entry = entries.find((value: ChatOutboxEntry) => value.id === id);
      if (!entry) return;
      // An acknowledged message cannot become retryable because a later read or cleanup failed.
      if (entry.state === 'done' && state !== 'done') return;
      entry.state = state === 'checked' ? 'failed' : state; entry.confirmedPending = state === 'pending';
      if (state === 'done' && acceptedAt > 0 && !entry.acceptedAt) entry.acceptedAt = acceptedAt;
      await this.local.put(key + ':' + entry.id, JSON.stringify(entry), epoch);
    });
  }
  async finishFeedback(scope: string, id: string): Promise<void> {
    const key = 'outbox:' + scope;
    return this.transaction(key, async (epoch: number): Promise<void> => {
      const raw = await this.local.get(key + ':' + id); if (!raw) return;
      const entry = this.decodeEntry(Wire.parse(raw)); entry.acceptedAt = -1;
      await this.local.put(key + ':' + id, JSON.stringify(entry), epoch);
    });
  }
  async attachmentsCommitted(scope: string, id: string): Promise<void> {
    const key = 'outbox:' + scope;
    return this.transaction(key, async (epoch: number): Promise<void> => {
      const entries = await this.entries(key), entry = entries.find((value: ChatOutboxEntry) => value.id === id);
      if (!entry || entry.state !== 'done') return;
      entry.attachmentSnapshot = ''; await this.local.put(key + ':' + id, JSON.stringify(entry), epoch);
    });
  }
  async discardEchoed(scope: string, ids: string[]): Promise<void> {
    if (!ids.length) return; const key = 'outbox:' + scope;
    return this.transaction(key, async (epoch: number): Promise<void> => {
      const entries = await this.entries(key), retained = entries.filter((entry: ChatOutboxEntry) => !ids.includes(entry.id));
      if (retained.length !== entries.length) { await this.index(key, retained, epoch); for (const entry of entries) if (ids.includes(entry.id)) { try { await this.local.remove(key + ':' + entry.id, epoch); } catch (_) {} } }
    });
  }
}
