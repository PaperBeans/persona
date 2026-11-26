import { util } from '@kit.ArkTS';
import type { MutationRecord } from './MutationStore';
import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';
import { ResourceRow, Wire } from './Wire';
import { SessionHeaderPermissions } from './SessionHeaderPermissions';
import { modelIDProblem } from './ModelID';

type Fields = Record<string,Object>;
const claudeEfforts:string[]=['auto','low','medium','high','xhigh','max'];

export function composerModelName(id:string):string {
  const match=/^claude-([a-z]+)((?:-[0-9]{1,2})*)(?:-[0-9]{8})?(\[1m\])?$/i.exec(id);
  if (!match) return id;
  const name=match[1]; return name.charAt(0).toUpperCase()+name.slice(1)+
    (match[2] ? ' '+match[2].slice(1).replace(/-/g,'.') : '')+(match[3] || '');
}
// PersonaWire.ModelID: ASCII IDs, including a gateway's tilde-prefixed latest alias.
export function validComposerModelID(id:string):boolean {
  return !modelIDProblem(id, (key:string):string => key);
}
function liveRow(client:ClientModel):ResourceRow|undefined {
  const selected=client.selected, manager=client.profile.manager, group=client.profile.id;
  if (!client.connected || !client.canCompose() || !['session','conversation'].includes(selected.kind) ||
    selected.manager && selected.manager!==manager || selected.group && selected.group!==group) return undefined;
  return client.sessions.find((s:ResourceRow) => s.manager===manager && (!s.group || s.group===group) && s.machine===selected.machine &&
    (s.session || s.id)===(selected.session || selected.id));
}
function claude(data:Fields):boolean { const agent=Wire.text(data,'agent');return (!agent || agent==='claude') && Wire.text(data,'launcher')!=='codex'; }
function modelCapable(data:Fields):boolean {
  const caps=Wire.object(data,'capabilities');
  if (Wire.bool(data,'assistant')) return false;
  if (Wire.has(caps,'model')) return Wire.bool(caps,'model');
  const terminal=Wire.has(data,'capabilities') ? Wire.bool(caps,'terminal') : !Wire.text(data,'source_kind');
  return terminal && claude(data) && !!Wire.text(data,'pane_id');
}
function modes(data:Fields):boolean {
  const caps=Wire.object(data,'capabilities'), assistant=Wire.bool(data,'assistant');
  return (assistant || Wire.text(data,'status')!=='offline') && Wire.bool(caps,'modes',assistant || claude(data));
}
function effortValues(data:Fields):string[] {
  const values=Wire.strings(data,'efforts');
  if (modes(data)) return values.length ? values : claudeEfforts;
  return !Wire.bool(data,'assistant') && !claude(data) && Wire.text(data,'status')!=='offline' ? values : [];
}
function modePath(row:ResourceRow):string {
  return '/api/v1/sessions/'+encodeURIComponent(row.machine)+'/'+encodeURIComponent(row.session || row.id)+'/modes';
}
function launcher(row:ResourceRow):string { const data=row.data(); return Wire.text(data,'launcher') || (claude(data) ? 'claude' : Wire.text(data,'agent')); }

@ObservedV2
export class SessionComposerModes {
  @Trace catalog:Fields[]=[];
  @Trace locked:boolean=false;
  @Trace loading:boolean=false;
  @Trace saving:boolean=false;
  @Trace error:string='';
  @Trace pending:string='';
  private sequence:number=0;
  private key:string='';
  private owner:ClientModel|undefined;
  private repo:HubRepository|undefined;

  private identity(client:ClientModel,row:ResourceRow):string {
    return JSON.stringify([client.scope(),client.connectionGeneration(),client.target(),row.machine,row.session || row.id,launcher(row)]);
  }
  private current(client:ClientModel):ResourceRow|undefined {
    const row=liveRow(client);
    return this.owner===client && row && this.repo===client.repo() && this.key===this.identity(client,row) ? row : undefined;
  }
  clear():void { this.sequence++; this.key=''; this.owner=undefined; this.repo=undefined; this.catalog=[]; this.loading=false; this.saving=false; this.error=''; this.pending=''; this.locked=false; }
  visible(client:ClientModel):boolean {
    const row=liveRow(client); if (!row) return false;
    const d=row.data(); return !!(Wire.text(d,'model') || Wire.text(d,'effort')) && !Wire.bool(d,'deleted');
  }
  data(client:ClientModel):Fields { return liveRow(client)?.data() || {}; }
  currentModel(client:ClientModel):string { return Wire.text(this.data(client),'model'); }
  modelName(client:ClientModel):string {
    const id=this.currentModel(client), match=this.catalog.find((c:Fields) => Wire.text(c,'id')===id && !Wire.bool(c,'default'));
    return match && Wire.text(match,'name') || composerModelName(id);
  }
  currentEffort(client:ClientModel):string { return Wire.text(this.data(client),'effort'); }
  pendingEffort(client:ClientModel):string { return Wire.text(this.data(client),'pending_effort') || this.pending; }
  efforts(client:ClientModel):string[] { return effortValues(this.data(client)); }
  modeValues(client:ClientModel):string[] {
    const d=this.data(client);
    const listed=Wire.strings(d,'modes');
    if (listed.length) return listed;
    if (!modes(d)) return [];
    const allowed=['default','acceptEdits','plan','auto'];
    if (Wire.bool(d,'assistant') || Wire.bool(d,'bypass_available')) allowed.push('bypassPermissions');
    return allowed;
  }
  acceptsModel(client:ClientModel):boolean {
    const row=this.current(client);
    return !!row && modelCapable(row.data()) && !this.locked && !Wire.bool(row.data(),'starting') &&
      !['working','needs_you','offline'].includes(row.state) && Wire.text(row.data(),'status')!=='offline';
  }
  allowsTypedModel(client:ClientModel):boolean {
    const row=this.current(client); if (!row || !this.acceptsModel(client)) return false;
    return !Wire.has(Wire.object(row.data(),'capabilities'),'model') || this.catalog.length>0;
  }
  chosenModel(client:ClientModel):string {
    const id=this.currentModel(client);
    const exact=this.catalog.find((c:Fields) => Wire.text(c,'id')===id);
    const resolved=this.catalog.find((c:Fields) => !Wire.bool(c,'default') && Wire.text(c,'resolved')===id);
    return Wire.text(exact || resolved || {},'id') || id;
  }
  async load(client:ClientModel,retry:boolean=false,models:boolean=true):Promise<void> {
    const row=liveRow(client);
    if (!row) { this.clear(); return; }
    const key=this.identity(client,row);
    if (!retry && this.key===key && this.owner===client) return;
    if (this.saving && this.key===key) return;
    this.clear(); this.owner=client; this.key=key; this.repo=client.repo();
    if (!models || !modelCapable(row.data()) || !launcher(row)) return;
    const seq=this.sequence; this.loading=true;
    try {
      const path='/api/v1/machines/'+encodeURIComponent(row.machine)+'/launchers/'+encodeURIComponent(launcher(row))+'/models';
      const result=await this.repo.call(path);
      if (seq!==this.sequence || !this.current(client)) return;
      this.locked=Wire.bool(result,'locked');
      this.catalog=Wire.rows(result,'models').filter((m:Fields) => !!Wire.text(m,'id') && !Wire.bool(m,'disabled'));
    } catch (_) { if (this.current(client) && seq===this.sequence) this.error='flow_models_offline'; }
    finally { if (this.current(client) && seq===this.sequence) this.loading=false; }
  }
  private allowed(client:ClientModel,field:string,value:string,ownOp:string=''):boolean {
    const actual=this.current(client);
    if (!actual || !value || Wire.bool(actual.data(),'deleted') || client.decision) return false;
    const path=modePath(actual);
    if (!ownOp && client.operationBlocked(path) || this.repo?.mutations?.blocked().some((r:MutationRecord) => r.path===path && r.id!==ownOp)) return false;
    if (field==='effort') return this.efforts(client).includes(value) && (value!==this.currentEffort(client) || !!this.pendingEffort(client));
    if (field==='header_permission') return new SessionHeaderPermissions(actual).permits(value);
    if (field==='permission_mode') return this.modeValues(client).includes(value) && Wire.text(actual.data(),'permission_mode')!==value;
    if (field==='custom_model') return this.allowsTypedModel(client) && validComposerModelID(value) && this.currentModel(client)!==value && this.chosenModel(client)!==value;
    return field==='model' && this.acceptsModel(client) && this.chosenModel(client)!==value && this.catalog.some((entry:Fields) => Wire.text(entry,'id')===value);
  }
  async change(client:ClientModel,field:string,value:string,showing:()=>boolean):Promise<boolean> {
    const row=this.current(client), repo=this.repo;
    if (!row || !repo || this.saving || !showing() || !this.allowed(client,field,value)) return false;
    const seq=this.sequence, op=util.generateRandomUUID(), path=modePath(row), body=Wire.payload([field==='custom_model' ? 'model' : field==='header_permission' ? 'permission_mode' : field],[value]);
    if (field==='effort') body['queue_effort']=true;
    this.saving=true; this.error='';
    const admit=():boolean => this.sequence===seq && showing() && this.allowed(client,field,value,op);
    try {
      const result=await repo.call(path,'POST',body,op,admit);
      if (!this.current(client) || this.sequence!==seq) return false;
      this.pending=Wire.text(result,'pending_effort'); await client.refresh();
      return !!this.current(client) && this.sequence===seq;
    } catch (e) {
      if (this.current(client) && this.sequence===seq) {
        const err=e as Object as Record<string,Object>; this.error=Wire.bool(err,'resultUnknown') ? 'result_unknown' : Wire.number(err,'status')===403 ? 'permission_denied' : 'failed';
      }
      return false;
    } finally { if (this.current(client) && seq===this.sequence) this.saving=false; }
  }
}
