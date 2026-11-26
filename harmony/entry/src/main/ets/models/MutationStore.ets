import { util } from '@kit.ArkTS';
import { LocalStore } from '../platform/LocalStore';
import type { UniqueLocalKey } from '../platform/LocalStore';
import { isHubStateKey } from '../core/StorageOwnership';
import { HttpFailure } from '../platform/HubTransport';
import { Wire } from './Wire';

export class MutationRecord {
  id: string = '';
  path: string = '';
  method: string = '';
  payload: string = '';
  state: string = 'pending';
  confirmedPending: boolean = false;
  requiresReceipt: boolean = true;
  createdAt: string = '';
}

// Persist before admission. A restart or lost reply never silently creates a replacement write.
export class MutationStore {
  private static stores: Map<LocalStore, Map<string, MutationStore>> = new Map();
  static forgetHub(local: LocalStore, hub: string): void {
    const scopes = MutationStore.stores.get(local); if (!scopes) return;
    for (const scope of scopes.keys()) if (isHubStateKey('mutations:' + scope, hub)) {
      scopes.get(scope)?.retire(); scopes.delete(scope);
    }
  }
  static shared(local: LocalStore, scope: string): MutationStore {
    let scopes = MutationStore.stores.get(local);
    if (!scopes) { scopes = new Map(); MutationStore.stores.set(local, scopes); }
    let store = scopes.get(scope); if (!store) { store = new MutationStore(local, scope); scopes.set(scope, store); }
    return store;
  }
  private records: MutationRecord[] = [];
  private retired: boolean = false;
  get scopeRetired(): boolean { return this.retired; }
  private retire(): void { this.retired = true; this.records.forEach((record: MutationRecord) => record.payload = ''); this.records = []; }
  private assertActive(): void { if (this.retired) throw new HttpFailure(0, 'scope_retired', true); }
  private loaded: boolean = false;
  private loading: Promise<void> | undefined;
  private writing: Promise<void> = Promise.resolve();
  private transaction<T>(action: () => Promise<T>): Promise<T> {
    const result = this.writing.then(() => { this.assertActive(); return action(); }); this.writing = result.then(() => {}, () => {}); return result;
  }
  private key: string;
  private local: LocalStore;
  constructor(local: LocalStore, scope: string) { this.local = local; this.key = 'mutations:' + scope; }
  async load(): Promise<void> {
    this.assertActive();
    if (this.loaded) return;
    if (this.loading) return this.loading;
    this.loading = this.restore();
    try { await this.loading; } finally { this.loading = undefined; }
  }
  private decode(value: Record<string, Object>): MutationRecord {
    const record = new MutationRecord(); record.id = Wire.text(value, 'id'); record.path = Wire.text(value, 'path');
    record.method = Wire.text(value, 'method'); record.payload = Wire.text(value, 'payload'); record.createdAt = Wire.text(value, 'createdAt');
    record.state = Wire.text(value, 'state', 'unknown'); record.confirmedPending = Wire.bool(value, 'confirmedPending');
    record.requiresReceipt = Wire.bool(value, 'requiresReceipt', record.state === 'pending'); return record;
  }
  private async restore(): Promise<void> {
    const raw = await this.local.get(this.key);
    this.assertActive();
    // Local payloads are individually bounded at admission, rather than as one growing network response.
    const saved: Record<string, Object> = raw ? Wire.record(JSON.parse(raw) as Object) : {};
    this.records = [];
    for (const id of Wire.strings(saved, 'ids')) {
      const value = await this.local.get(this.key + ':' + id);
      this.assertActive();
      if (!value) throw new Error('storage_unavailable');
      const record = this.decode(Wire.record(JSON.parse(value) as Object));
      if (record.state === 'pending' && !record.confirmedPending) record.state = 'unknown'; this.records.push(record);
    }
    // Migrate the original single-row development format without discarding an uncertain operation.
    for (const value of Wire.rows(saved, 'records')) {
      const record = this.decode(value); if (record.state === 'pending' && !record.confirmedPending) record.state = 'unknown';
      if (['pending', 'unknown'].includes(record.state)) { this.records.push(record); await this.persist(record); }
    }
    await this.saveIndex(); this.loaded = true;
  }
  private async persist(record: MutationRecord): Promise<void> { this.assertActive(); await this.local.put(this.key + ':' + record.id, JSON.stringify(record)); }
  private async saveIndex(recent?: MutationRecord): Promise<void> {
    this.assertActive();
    const ordered = recent ? this.records.filter((item: MutationRecord) => item !== recent).concat([recent]) : this.records;
    const terminal = ordered.filter((item: MutationRecord) => !['pending', 'unknown'].includes(item.state));
    const removed = terminal.slice(0, Math.max(0, terminal.length - 100));
    const retained = ordered.filter((item: MutationRecord) => !removed.includes(item));
    await this.local.put(this.key, JSON.stringify(Wire.payload(['ids'], [retained.map((item: MutationRecord) => item.id)])));
    this.records = retained;
    for (const record of removed) { try { await this.local.remove(this.key + ':' + record.id); } catch (_) { /* Orphaned terminal rows are safe to clean on a later maintenance pass. */ } }
  }
  blocked(): MutationRecord[] { return this.records.filter((item: MutationRecord) => item.state === 'pending' || item.state === 'unknown'); }
  record(id: string): MutationRecord | undefined { return this.records.find((item: MutationRecord) => item.id === id); }
  async begin(path: string, method: string, payload: string, id: string = ''): Promise<MutationRecord> {
    await this.load();
    return this.transaction(() => this.admit(path, method, payload, id));
  }
  private async admit(path: string, method: string, payload: string, id: string): Promise<MutationRecord> {
    if (this.blocked().length >= 128 || this.blocked().some((item: MutationRecord) => item.path === path && item.method === method))
      throw new HttpFailure(0, 'pending_action', true);
    const record = new MutationRecord(); record.id = id || util.generateRandomUUID(); record.path = path; record.method = method;
    // This journal never replays a body. Draft stores own user content; photo Base64 cannot fit a readable RDB row.
    record.payload = '';
    record.createdAt = new Date().toISOString(); this.records.push(record);
    try { await this.persist(record); await this.saveIndex(); return record; }
    catch (_) {
      // Admission failed locally, before the caller could dispatch any network request.
      record.state = 'failed'; record.payload = '';
      try { await this.persist(record); await this.saveIndex(); } catch (_) {}
      throw new HttpFailure(0, 'storage_unavailable', false);
    }
  }
  async finish(record: MutationRecord, state: string, admit?: () => boolean): Promise<void> {
    return this.transaction(async () => {
      if (!admit || admit()) await this.settle(record, state);
    });
  }
  private async settle(record: MutationRecord, state: string): Promise<void> {
    if (record.state === 'done') return;
    if (state === 'checked' && (record.state !== 'unknown' || record.requiresReceipt)) return;
    const updated = this.decode(Wire.record(JSON.parse(JSON.stringify(record)) as Object)); updated.state = state; updated.confirmedPending = state === 'pending';
    updated.requiresReceipt = state === 'pending';
    if (!['pending', 'unknown'].includes(state)) updated.payload = '';
    if (state === 'done') { record.state = 'done'; record.confirmedPending = false; record.requiresReceipt = true; record.payload = ''; }
    try {
      await this.persist(updated);
      record.state = updated.state; record.confirmedPending = updated.confirmedPending; record.requiresReceipt = updated.requiresReceipt; record.payload = updated.payload;
      // The existing index already references this durable row. Index pruning may be retried later.
      try { await this.saveIndex(); } catch (_) {}
    } catch (_) {
      if (state === 'done') {
        // Delivery is already proved. Keep that fact even if its first canonical write failed.
        record.state = 'done'; record.confirmedPending = false; record.requiresReceipt = true; record.payload = '';
        try { await this.persist(record); } catch (_) {}
        return;
      }
      // Preserve the original operation as a blocker when completion cannot be durably recorded.
      const pending = state === 'pending' || record.confirmedPending;
      record.state = pending ? 'pending' : 'unknown'; record.confirmedPending = pending;
      if (pending) record.requiresReceipt = true;
      try { await this.persist(record); } catch (_) {}
      throw new HttpFailure(0, 'receipt_storage_failed', true);
    }
  }
  /** Record the local review without erasing its request, its saved form, or any terminal history. */
  async acknowledgeRemovedRename(record:MutationRecord, viewScope:string, oldFormKey:string, snapshot:string, currentFormKey:string,
    admit:()=>boolean):Promise<boolean> {
    return this.transaction(async ():Promise<boolean>=>{
      const prefix='view:'+viewScope+':worktree-request:';
      const allowed=():boolean=>this.record(record.id)===record && record.state==='unknown' && record.method==='POST' &&
        record.path.endsWith('/worktree/rename') && record.confirmedPending===false && record.requiresReceipt===false &&
        this.blocked().filter((other:MutationRecord)=>other.method===record.method && other.path===record.path).length===1 && admit();
      const form=Wire.parse(snapshot);
      if(!allowed() || oldFormKey===currentFormKey || !currentFormKey.startsWith(prefix) ||
        oldFormKey!==prefix+Wire.text(form,'target') || Wire.text(form,'id')!==record.id ||
        Wire.text(form,'action')!=='rename' || Wire.text(form,'path')!==record.path) return false;
      const key=this.key+':'+record.id,index=await this.local.get(this.key),raw=await this.local.get(key);
      const ids=Wire.strings(Wire.parse(index),'ids'), data=Wire.parse(raw), saved=this.decode(data);
      if(!allowed() || ids.filter((id:string)=>id===record.id).length!==1 || JSON.stringify(ids)!==JSON.stringify(this.records.map((r:MutationRecord)=>r.id)) ||
        saved.id!==record.id || saved.path!==record.path || saved.method!==record.method || saved.createdAt!==record.createdAt ||
        saved.payload!==record.payload || saved.state!=='unknown' || saved.confirmedPending || saved.requiresReceipt) return false;
      const target=JSON.parse(Wire.text(form,'target')) as Object;
      if(!Array.isArray(target) || target.length!==8 || target[0]!==viewScope || target[7]!=='rename')return false;
      const routePrefix=prefix+JSON.stringify((target as string[]).slice(0,5)).slice(0,-1)+',';
      const unique:UniqueLocalKey={prefix:routePrefix,suffix:',"rename"]',valueContains:record.id,key:oldFormKey};
      data['state']='checked';
      const ordered=this.records.filter((r:MutationRecord)=>r!==record).concat([record]);
      const values=new Map<string,string>();
      values.set(key,JSON.stringify(data));
      values.set(this.key,JSON.stringify(Wire.payload(['ids'],[ordered.map((r:MutationRecord)=>r.id)])));
      const expected=new Map<string,string>();expected.set(key,raw);expected.set(this.key,index);
      expected.set(oldFormKey,snapshot);expected.set(currentFormKey,'');
      await this.local.updateRecords(key,this.local.revision(key),values,[],allowed,expected,unique);
      record.state='checked';this.records=ordered;return true;
    });
  }
  async acknowledge(id: string, admit?: () => boolean): Promise<void> {
    const record = this.record(id);
    if (!record || record.state !== 'unknown' || record.requiresReceipt || record.confirmedPending) return;
    await this.transaction(async () => {
      const allowed = (): boolean => this.record(id) === record && record.state === 'unknown' &&
        !record.requiresReceipt && !record.confirmedPending && (!admit || admit());
      if (!allowed()) return;
      if (admit && this.records[this.records.length-1] !== record) {
        // Persist the review order while it is still unknown. Restoring a checked row
        // can then finish an interrupted view cleanup even with a full terminal history.
        await this.saveIndex(record);
        if (!allowed()) return;
      }
      await this.settle(record, 'checked');
    });
  }
}
