import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

export class ChatActionTarget {
  client: ClientModel;
  generation: number;
  scope: string;
  machine: string;
  session: string;
  key: string;
  constructor(client: ClientModel,row: ResourceRow) {
    this.client=client; this.generation=client.connectionGeneration(); this.scope=client.scope();
    this.machine=row.machine; this.session=row.session || row.id;
    this.key=JSON.stringify([this.scope,this.generation,this.machine,this.session]);
  }
}

export class ChatDirection {
  id: string;
  name: string;
  constructor(id: string,name: string) { this.id=id; this.name=name; }
}

class FilingUndo {
  target: ChatActionTarget;
  previous: string | null;
  constructor(target: ChatActionTarget,previous: string | null) {this.target=target; this.previous=previous;}
}

// The Hub owns each write. A held gesture or late response cannot act on another manager's rows.
@ObservedV2
export class AllChatActions {
  @Trace directions: ChatDirection[] = [];
  @Trace hidden: string[] = [];
  @Trace pending: string[] = [];
  @Trace notice: string = '';
  @Trace detail: string = '';
  @Trace failure: boolean = false;
  @Trace undo: FilingUndo | undefined = undefined;
  private client: ClientModel | undefined = undefined;
  private scope: string = '';
  private generation: number = -1;
  private treeInput: string = '';
  private missingRoutes: boolean = false;
  private readRevision: number = 0;

  bind(client: ClientModel): void {
    if (this.client!==client || this.scope!==client.scope() || this.generation!==client.connectionGeneration()) {
      this.close(); this.client=client; this.scope=client.scope(); this.generation=client.connectionGeneration();
    }
    const input=JSON.stringify([client.connected,client.features.includes('direction_sessions'),Wire.strings(Wire.parse(client.sidebarConfig),'leaders')]);
    if (input!==this.treeInput) { this.treeInput=input; this.readDirections().catch(() => {}); }
  }

  close(): void {
    this.client=undefined; this.readRevision++; this.treeInput=''; this.missingRoutes=false; this.treeLoaded=false;
    this.directions=[]; this.hidden=[]; this.pending=[]; this.clearNotice();
  }
  clearNotice(): void { this.notice=''; this.detail=''; this.failure=false; this.undo=undefined; }

  private current(target: ChatActionTarget): boolean {
    return this.client===target.client && this.scope===target.scope && this.generation===target.generation &&
      target.client.connected && !target.client.demo && target.client.owns(target.generation,target.scope);
  }

  private same(target: ChatActionTarget,row: ResourceRow): boolean {
    return (row.session || row.id)===target.session && row.machine===target.machine &&
      (!row.group || row.group===target.client.profile.id) && (!row.manager || row.manager===target.client.profile.manager);
  }

  row(target: ChatActionTarget): ResourceRow | undefined {
    if (!this.current(target) || !target.session) return undefined;
    const client=target.client, config=Wire.parse(client.sidebarConfig);
    if (target.session===Wire.text(config,'background_supervisor') ||
        target.session===client.mainId && (!client.mainMachineId || client.mainMachineId===target.machine)) return undefined;
    return client.conversations.find((row: ResourceRow) => this.same(target,row) && !Wire.bool(row.data(),'archived'));
  }

  // Pin and archive are keyed only by session in this API. A duplicate on another computer is ambiguous.
  canChangeChat(target: ChatActionTarget): boolean {
    return !!this.row(target) && !target.client.conversations.some((row: ResourceRow) =>
      (row.session || row.id)===target.session && row.machine!==target.machine &&
      (!row.group || row.group===target.client.profile.id) && (!row.manager || row.manager===target.client.profile.manager));
  }
  pinned(target: ChatActionTarget): boolean {
    return Wire.strings(Wire.parse(target.client.sidebarConfig),'leaders').includes(target.session);
  }
  private permitted(target: ChatActionTarget,path: string): boolean {
    return this.current(target) && !this.pending.includes(target.key) && !target.client.operationBlocked(path);
  }

  hasFiling(): boolean { return !!this.client && this.client.features.includes('direction_sessions') && !this.missingRoutes && this.treeLoaded; }
  private treeLoaded: boolean = false;
  private async readDirections(): Promise<void> {
    const client=this.client, scope=this.scope, generation=this.generation, rev=++this.readRevision;
    if (!client || !client.connected || client.demo || this.missingRoutes || !client.features.includes('direction_sessions')) {
      this.directions=[]; this.treeLoaded=false; return;
    }
    const valid=(): boolean => this.client===client && this.readRevision===rev && client.owns(generation,scope);
    try {
      const tree=await client.repo().call('/api/v1/assistant/directions/tree');
      if (!valid()) return;
      this.treeLoaded=Array.isArray(tree['directions']);
      this.directions=Wire.rows(tree,'directions').filter((r:Record<string,Object>) =>
        !!Wire.text(r,'id') && !Wire.bool(r,'secretary')).map((r:Record<string,Object>) => {
          const id=Wire.text(r,'id'), chat=client.conversations.find((s:ResourceRow) => (s.session || s.id)===id);
          return new ChatDirection(id,Wire.text(r,'name') || chat?.title || '');
        });
    } catch (_) { if (valid()) { this.directions=[]; this.treeLoaded=false; } }
  }

  choices(target: ChatActionTarget,ownWrite: boolean=false): ChatDirection[] {
    const current=this.row(target), client=target.client;
    if (!current || !target.machine || !client.features.includes('direction_sessions') || this.missingRoutes ||
        (!ownWrite && this.pending.includes(target.key)) || this.pinned(target) || this.directions.some((d:ChatDirection) => d.id===target.session)) return [];
    const followers=new Set<string>();
    for (const todo of client.todos) {
      const data=todo.data();
      if (Wire.text(data,'done_at') || !Wire.text(Wire.object(data,'supervision'),'supervisor')) continue;
      for (const agent of Wire.rows(data,'agents')) {
        if (!Wire.text(agent,'released_at') && !Wire.text(agent,'dismissed_at')) followers.add(Wire.text(agent,'machine_id')+'/'+Wire.text(agent,'session_id'));
      }
    }
    let session=target.session;
    for (let depth=0;depth<8 && session;depth++) {
      if (followers.has(target.machine+'/'+session)) return [];
      const row=client.sessions.find((r:ResourceRow) => r.machine===target.machine && (r.session || r.id)===session);
      const parent=Wire.text(row ? row.data() : session===target.session ? current.data() : {},'parent_session_id') || (session.includes('@') ? session.split('@')[0] : '');
      if (!parent || parent===session) break;
      session=parent;
    }
    return this.directions;
  }

  private async write(target: ChatActionTarget,path: string,method: string,body: Record<string,Object>,admit: () => boolean,
    acknowledge: (response: Record<string,Object>) => void): Promise<void> {
    if (!this.permitted(target,path) || !admit()) return;
    this.pending=[...this.pending,target.key]; this.clearNotice();
    try {
      const result=await target.client.repo().call(path,method,body,'',() => this.current(target) && admit());
      if (this.current(target)) acknowledge(result);
      else if (target.client.connected && target.client.owns(target.generation,target.scope)) target.client.refresh().catch(() => {});
    } catch (problem) {
      if (this.current(target)) {
        const error=problem as Error, data=error as Object as Record<string,Object>;
        const code=typeof data['code']==='string' ? String(data['code']) : error.message;
        const status=typeof data['status']==='number' ? Number(data['status']) : 0;
        if (path.endsWith('/direction') && [404,405].includes(status) && !['session_not_found','machine_not_found','direction_not_found'].includes(code)) {
          this.missingRoutes=true; this.directions=[]; this.notice='phone_chat_direction_unavailable';
        } else if (path.endsWith('/direction') && typeof data['userMessage']==='string' && String(data['userMessage']).length) {
          this.detail=String(data['userMessage']);this.notice='from_hub';
        } else if (['full_access_required','forbidden'].includes(code) || status===403) this.notice='permission_denied';
        else if (code==='direction_full') this.notice='phone_chat_direction_full';
        else if (code==='direction_supervised_task') this.notice='phone_chat_direction_supervised';
        else if (data['resultUnknown']===true || data['resultPending']===true) this.notice='result_unknown';
        else this.notice=['offline','pending_action','request_updated'].includes(code) ? code : 'failed';
        this.failure=true;
      }
    } finally { if (this.current(target)) this.pending=this.pending.filter((key:string) => key!==target.key); }
  }

  async archive(target: ChatActionTarget): Promise<void> {
    const path='/api/v1/assistant/conversations/'+encodeURIComponent(target.session)+'/archive';
    if (!this.canChangeChat(target) || !this.permitted(target,path)) return;
    this.hidden=[...this.hidden,target.key];
    try {
      await this.write(target,path,'POST',{},() => this.canChangeChat(target),(result:Record<string,Object>) => {
        target.client.conversations=target.client.conversations.filter((r:ResourceRow) => !this.same(target,r));
        this.notice='phone_chat_archived'; target.client.refresh().catch(() => {});
      });
    } finally { if (this.current(target)) this.hidden=this.hidden.filter((key:string) => key!==target.key); }
  }

  async pin(target: ChatActionTarget,value: boolean): Promise<void> {
    const path='/api/v1/assistant/directions/'+encodeURIComponent(target.session)+(value ? '/make' : '/end');
    await this.write(target,path,'POST',{},() => this.canChangeChat(target) && this.pinned(target)!==value,(result:Record<string,Object>) => {
      const config=Wire.parse(target.client.sidebarConfig), leaders=Wire.strings(config,'leaders').filter((id:string) => id!==target.session);
      if (value) leaders.push(target.session);
      config['leaders']=leaders; target.client.sidebarConfig=JSON.stringify(config);
      this.notice=value ? 'phone_chat_pinned' : 'phone_chat_unpinned'; target.client.refresh().catch(() => {});
    });
  }

  async file(target: ChatActionTarget,choice: ChatDirection | undefined,undo: boolean=false): Promise<void> {
    const prior=this.undo, direction=undo && prior ? prior.previous : choice?.id || '';
    const path='/api/v1/assistant/sessions/'+encodeURIComponent(target.machine)+'/'+encodeURIComponent(target.session)+'/direction';
    // JSON null restores the absence of a filing. The empty string explicitly keeps a chat on its own.
    const body=direction===null ? Wire.parse('{"direction":null}') : Wire.payload(['direction'],[direction]);
    const choices=this.choices(target);
    if (!choices.length || (undo ? !prior || prior.target.key!==target.key : !!choice && !choices.some((c:ChatDirection) => c.id===choice.id))) return;
    await this.write(target,path,'PUT',body,() => this.choices(target,true).length>0 &&
      (undo || !choice || this.directions.some((c:ChatDirection) => c.id===choice.id)),(result:Record<string,Object>) => {
      if (undo) { this.notice='phone_chat_move_undone'; return; }
      const changed=Wire.bool(result,'changed');
      this.detail=choice?.name || '';
      this.notice=choice ? (changed ? 'phone_chat_moved' : 'phone_chat_already_moved') : (changed ? 'phone_chat_own_set' : 'phone_chat_own_already');
      if (changed && Wire.has(result,'previous')) this.undo=new FilingUndo(target,typeof result['previous']==='string' ? Wire.text(result,'previous') : null);
    });
  }

  async undoMove(): Promise<void> { const entry=this.undo; if (entry) await this.file(entry.target,undefined,true); }
}
