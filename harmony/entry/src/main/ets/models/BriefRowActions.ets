import { Wire } from './Wire';
import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';

export type BriefAction = 'accept' | 'complete' | 'handled';

export function briefTake(row: Record<string,Object>): string {
  const outcome = Wire.text(row,'outcome').trim();
  if (outcome === 'decide') return Wire.text(row,'recommend').trim() ? 'recommendation' : '';
  return outcome ? 'got_it' : 'accept';
}

export function briefWritable(value: Record<string,Object>): boolean {
  return !Wire.bool(value,'read_only',Wire.bool(value,'readonly')) &&
    (!Wire.has(value,'can_write') || Wire.bool(value,'can_write'));
}

function rowKey(scope: string, section: string, row: Record<string,Object>): string {
  return JSON.stringify([scope,section,Wire.text(row,'id'),section==='accept' ? Wire.text(row,'asked_at') : '']);
}

export interface BriefRoute {
  scope: string;
  manager: string;
  loadRepository: () => Promise<HubRepository>;
  current: () => boolean;
  blocked: (path: string) => boolean;
  reloadTodos: () => Promise<void>;
}

export function briefRowPath(manager: string, id: string, action: BriefAction): string {
  const base = action === 'handled' ? '/api/v1/later/' : '/api/v1/todos/';
  const suffix = action === 'handled' ? '/handled' : action === 'accept' ? '/accept' : '';
  return base + encodeURIComponent(id) + suffix + (manager ? '?manager=' + encodeURIComponent(manager) : '');
}

function replacing(entries: Record<string,string>, key: string, value: string): Record<string,string> {
  const next: Record<string,string> = {};
  Object.keys(entries).forEach((other: string) => { if (other!==key) next[other]=entries[other]; });
  if (value) next[key]=value;
  return next;
}

@ObservedV2
export class BriefRowLedger {
  @Trace private outcomes: Record<string,string> = {};
  @Trace private errors: Record<string,string> = {};
  @Trace private details: Record<string,string> = {};

  outcome(scope: string, section: string, row: Record<string,Object>): string {
    return this.outcomes[rowKey(scope,section,row)] || '';
  }
  error(scope: string, section: string, row: Record<string,Object>): string {
    return this.errors[rowKey(scope,section,row)] || '';
  }

  detail(scope: string, section: string, row: Record<string,Object>): string {
    return this.details[rowKey(scope,section,row)] || '';
  }

  async perform(route: BriefRoute, section: string, row: Record<string,Object>, action: BriefAction): Promise<boolean> {
    const id = Wire.text(row,'id'), round = Wire.text(row,'asked_at'), take = briefTake(row);
    const offered = action === 'handled' ? section === 'later' : section === 'accept' && !!round.trim() &&
      (action === 'accept' ? !!take : Wire.text(row,'outcome').trim() === 'decide');
    if (!id || !offered || !route.current()) return false;
    const key = rowKey(route.scope,section,row), path = briefRowPath(route.manager,id,action);
    if (this.outcomes[key] || route.blocked(path)) return false;
    this.outcomes=replacing(this.outcomes,key,'busy');
    this.errors=replacing(this.errors,key,'');
    this.details=replacing(this.details,key,'');
    let outcome = '', error = '', detail = '';
    try {
      const repository=await route.loadRepository();
      if (!route.current() || route.blocked(path)) return false;
      if (action === 'complete') {
        // The completion PATCH has no round parameter. Re-read the actual decision before ending it.
        const live = Wire.object(await repository.call(path,'GET',{},'',route.current),'todo');
        if (!route.current()) return false;
        if (Wire.text(live,'id')!==id || Wire.text(live,'manager',route.manager)!==route.manager) {
          error='request_updated'; return false;
        }
        if (!briefWritable(live)) { error='permission_denied'; return false; }
        const ask=Wire.object(live,'ask');
        if (Wire.text(live,'done_at') || Wire.bool(live,'done') || Wire.text(ask,'asked_at')!==round || Wire.text(ask,'outcome').trim()!=='decide') {
          outcome='already'; return false;
        }
      }
      const accepted = (result: Record<string,Object>): boolean => {
        if (action!=='complete') return Wire.bool(result,'ok');
        const todo=Wire.object(result,'todo');
        return Wire.text(todo,'id')===id && Wire.text(todo,'manager',route.manager)===route.manager && !!Wire.text(todo,'done_at');
      };
      const body=action==='complete' ? Wire.payload(['done'],[true]) :
        action==='accept' ? Wire.payload(['asked_at'],[round]) : Wire.payload([],[]);
      const response=await repository.call(path,action==='complete'?'PATCH':'POST',body,'',route.current,false,accepted,action==='handled');
      if (!accepted(response)) { outcome='unknown'; return false; }
      outcome=action==='handled'?'handled':action==='complete'?'completed':take==='accept'?'accepted':take==='got_it'?'noted':'recommended';
      if (action!=='handled' && route.current()) {
        try { await route.reloadTodos(); } catch (_) { /* The Hub already acknowledged the write. */ }
      }
      return true;
    } catch (cause) {
      const failure=cause as Object as Record<string,Object>, status=Wire.number(failure,'status');
      if (Wire.bool(failure,'resultPending')) outcome='pending';
      else if (Wire.bool(failure,'resultUnknown') || Wire.text(failure,'code')==='send_in_doubt') outcome='unknown';
      else if (action!=='complete' && status===409) outcome='already';
      else if (action!=='complete' && status===404) outcome='unsupported';
      else if (status===401 || status===403) error='permission_denied';
      else if (Wire.text(failure,'code')==='request_updated' || Wire.text(failure,'message')==='request_updated') error='request_updated';
      else error=action==='handled'?'brief_row_handle_failed':action==='complete'?'brief_row_complete_failed':'brief_row_accept_failed';
      if (error || outcome==='unknown') detail=Wire.text(failure,'userMessage').trim().slice(0,512);
      return false;
    } finally {
      // This key names the original account and round even if navigation changed while the Hub was responding.
      this.outcomes=replacing(this.outcomes,key,outcome);
      this.errors=replacing(this.errors,key,error);
      this.details=replacing(this.details,key,detail);
    }
  }
}

const ledgers: WeakMap<ClientModel,BriefRowLedger> = new WeakMap();
export function briefLedgerFor(client: ClientModel): BriefRowLedger {
  const existing=ledgers.get(client);
  if (existing) return existing;
  const next=new BriefRowLedger(); ledgers.set(client,next); return next;
}
