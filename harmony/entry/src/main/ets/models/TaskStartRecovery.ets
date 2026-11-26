import type { AssistantFeedModel } from './AssistantFeed';
import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';
import { ResourceRow, Wire } from './Wire';
import { TaskStartupQuestion } from './TaskStartupQuestion';

type Fields = Record<string,Object>;

function matchingTask(plan:Fields, id:string):Fields | undefined {
  return Wire.rows(plan,'tasks').find((task:Fields) => Wire.text(task,'todo_id')===id);
}

function ownedManager(data:Fields, owner:string):boolean {
  return !Wire.has(data,'manager') || typeof data['manager']==='string' && data['manager']===owner;
}

function cannotWrite(row:Fields):boolean {
  return Wire.bool(row,'read_only',Wire.bool(row,'readonly')) || Wire.has(row,'can_write') && !Wire.bool(row,'can_write');
}

/** A review ticket is scoped to the failed attempt. Read markers and rendered timestamps are independent. */
function orderedFields(record:Fields):Object[] {
  return Object.keys(record).sort().map((key:string):Object => [key,record[key]]);
}

function attemptSignature(todo:Fields, plan:Fields, task:Fields):string {
  return JSON.stringify([Wire.text(todo,'id'), Wire.text(todo,'origin'), Wire.text(todo,'plan_id'), Wire.text(todo,'title'),
    Wire.text(todo,'start_error'), Wire.text(todo,'done_at'), Wire.text(todo,'reopened_at'), Wire.text(plan,'id'), Wire.text(plan,'conversation'),
    Wire.text(task,'machine'), Wire.text(task,'cwd'), Wire.text(task,'launcher'), Wire.text(task,'error'), Wire.text(task,'error_code'),
    orderedFields(Wire.object(task,'error_args')), Wire.strings(task,'retry_with'), Wire.text(task,'pane'), orderedFields(Wire.object(task,'start')),
    Wire.rows(todo,'agents').map((a:Fields)=>[Wire.text(a,'machine_id'),Wire.text(a,'session_id'),Wire.text(a,'linked_at'),Wire.text(a,'released_at'),Wire.text(a,'dismissed_at')])]);
}

@ObservedV2
export class TaskStartRecovery {
  @Trace id:string='';
  @Trace title:string='';
  @Trace failure:string='';
  @Trace state:string='';
  @Trace error:string='';
  @Trace busy:boolean=false;
  @Trace readOnly:boolean=false;
  @Trace confirmRemoval:boolean=false;
  @Trace alternative:string='';
  @Trace reasonCode:string='';
  @Trace reasonLauncher:string='';
  @Trace question:TaskStartupQuestion | undefined;
  private selectedTodo:(() => Fields) | undefined;
  private refreshTodo:(() => Promise<void>) | undefined;
  private editableTodo:(() => boolean) | undefined;
  private client:ClientModel | undefined;
  get language():string { return this.client?.language || 'default'; }
  private feed:AssistantFeedModel | undefined;
  private repository:HubRepository | undefined;
  private generation:number=0;
  private clientGeneration:number=0;
  private group:string='';
  private conversation:string='';
  private manager:string='';
  private scope:string='';
  private planID:string='';
  private version:string='';
  private completedAttempt:string='';
  private active:()=>boolean=():boolean=>false;
  private uncertain:boolean=false;
  private originalTodoAttempt:string='';
  private visibleAttempt(todo:Fields):string {
    return JSON.stringify([Wire.text(todo,'id'),Wire.text(todo,'origin'),Wire.text(todo,'plan_id'),Wire.text(todo,'done_at'),
      Wire.text(todo,'start_error'),Wire.text(todo,'reopened_at'),Wire.rows(todo,'agents')]);
  }

  close():void {
    this.generation++; this.id=''; this.title=''; this.failure=''; this.state=''; this.error=''; this.busy=false;
    this.alternative=''; this.reasonCode=''; this.reasonLauncher=''; this.version=''; this.completedAttempt=''; this.uncertain=false;this.readOnly=false;this.confirmRemoval=false;
    this.question=undefined;this.selectedTodo=undefined;this.refreshTodo=undefined;this.editableTodo=undefined;
    this.client=undefined; this.feed=undefined; this.repository=undefined; this.active=():boolean=>false;
  }

  open(client:ClientModel,feed:AssistantFeedModel,row:ResourceRow,active:()=>boolean):void {
    this.close();
    const item=feed.todo(row.id), origin=Wire.text(item,'origin');
    if (!row.id || !origin || origin!==feed.conversationId || row.group!==client.profile.id || row.manager!==client.profile.manager ||
      feed.owner!==client.profile.manager || !ownedManager(item,feed.owner)) return;
    this.client=client; this.feed=feed; this.repository=client.repo(); this.clientGeneration=client.connectionGeneration(); this.scope=client.scope();
    this.group=row.group; this.conversation=origin; this.manager=row.manager; this.id=row.id; this.title=Wire.text(item,'title'); this.active=active;
    let planID=Wire.text(item,'plan_id'), plan=Wire.record(feed.plans[planID]);
    if (!matchingTask(plan,this.id)) {
      const matches=Object.keys(feed.plans).filter((key:string)=>{
        const p=Wire.record(feed.plans[key]); return Wire.text(p,'conversation')===origin && Wire.text(p,'state')!=='dismissed' && !!matchingTask(p,this.id);
      });
      if(matches.length===1) { planID=matches[0]; plan=Wire.record(feed.plans[planID]); }
    }
    this.planID=planID; this.readOnly=cannotWrite(item);
    const task=matchingTask(plan,this.id) || {};
    this.failure=Wire.text(item,'start_error') || Wire.text(task,'error');
    this.version=this.failure && Wire.text(plan,'id')===planID && Wire.text(plan,'conversation')===origin ? attemptSignature(item,plan,task) : '';
    this.state='loading'; this.refresh();
  }

  openTodo(client:ClientModel, selected:()=>Fields, active:()=>boolean, canWrite:()=>boolean, refresh:()=>Promise<void>):void {
    this.close();
    const todo=selected(), origin=Wire.text(todo,'origin'), id=Wire.text(todo,'id'), manager=client.profile.manager;
    if (!origin || !id || !ownedManager(todo,manager)) return;
    this.client=client; this.repository=client.repo(); this.clientGeneration=client.connectionGeneration(); this.scope=client.scope();
    this.id=id; this.group=client.profile.id; this.manager=manager; this.conversation=origin;
    this.title=Wire.text(todo,'title'); this.failure=Wire.text(todo,'start_error'); this.planID=Wire.text(todo,'plan_id');
    this.originalTodoAttempt=this.visibleAttempt(todo); this.selectedTodo=selected; this.active=active; this.editableTodo=canWrite; this.refreshTodo=refresh;
    this.readOnly=cannotWrite(todo); this.state='loading'; this.refresh();
  }

  private current(run:number):boolean {
    const c=this.client, f=this.feed;
    return this.generation===run && !!c && c.owns(this.clientGeneration,this.scope) && c.repo()===this.repository &&
      c.connected && c.profile.id===this.group && c.profile.manager===this.manager && this.active() &&
      (f ? f.owner===this.manager && f.conversationId===this.conversation : !!this.selectedTodo && this.belongs(this.selectedTodo()));
  }
  private path(action:string):string {
    let tail='/api/v1/todos/'+encodeURIComponent(this.id);
    if (action==='plan') tail='/api/v1/task-plans/'+encodeURIComponent(this.planID);
    else if (action==='feed') tail='/api/v1/assistant/conversations/'+encodeURIComponent(this.conversation)+'/feed';
    else if (action==='retry') tail+='/retry';
    else if (action==='answer') tail+='/start/answer';
    return this.manager ? tail+'?manager='+encodeURIComponent(this.manager) : tail;
  }
  private belongs(todo:Fields):boolean {
    return Wire.text(todo,'id')===this.id && Wire.text(todo,'origin')===this.conversation && ownedManager(todo,this.manager);
  }
  private async readAttempt(run:number):Promise<string> {
    const repository=this.repository!;
    if (!this.current(run)) return '';
    const result=await repository.call(this.path('todo'));
    if (!this.current(run)) return '';
    const todo=Wire.object(result,'todo');
    if (!this.belongs(todo)) return '';
    this.readOnly=cannotWrite(todo); this.confirmRemoval=Wire.rows(todo,'agents').length>0;
    if (!this.planID && this.selectedTodo) {
      const response=await repository.call(this.path('feed'));
      if (!this.current(run)) return '';
      const plans=Wire.object(response,'plans');
      const matches=Object.keys(plans).filter((id:string)=>{
        const plan=Wire.record(plans[id]);
        return Wire.text(plan,'id')===id && Wire.text(plan,'conversation')===this.conversation && ownedManager(plan,this.manager) &&
          Wire.text(plan,'state')!=='dismissed' && !!matchingTask(plan,this.id);
      });
      if (matches.length===1) this.planID=matches[0];
    }
    if (!this.planID || (Wire.text(todo,'plan_id') && Wire.text(todo,'plan_id')!==this.planID)) return '';
    const response=await repository.call(this.path('plan'));
    if (!this.current(run)) return '';
    const plan=Wire.object(response,'plan'), task=matchingTask(plan,this.id);
    if (!task || Wire.text(plan,'id')!==this.planID || Wire.text(plan,'conversation')!==this.conversation ||
      !ownedManager(plan,this.manager) || Wire.text(plan,'state')==='dismissed') return '';
    const error=Wire.text(todo,'start_error') || (!Wire.rows(todo,'agents').length ? Wire.text(task,'error') : '');
    if (!error) return '';
    this.title=Wire.text(todo,'title'); this.failure=error;
    this.reasonCode=Wire.text(task,'error_code');
    this.reasonLauncher=Wire.text(Wire.object(task,'error_args'),'where')==='terminal' ? Wire.text(Wire.object(task,'error_args'),'launcher') : '';
    this.alternative=this.client!.features.includes('task_retry_launcher') ? Wire.strings(task,'retry_with')[0] || '' : '';
    const signature=attemptSignature(todo,plan,task);
    const start=Wire.text(Wire.object(task,'start'),'state');
    if (this.reasonCode==='shell_waiting' && this.client!.features.includes('todo_start_answer') &&
      !['remedying','starting','started'].includes(start) && !!Wire.text(todo,'done_at') && !!Wire.text(todo,'start_error')) {
      const choice=new TaskStartupQuestion(Wire.object(task,'error_args'),signature);
      this.question=choice.open() ? choice : undefined;
    } else this.question=undefined;
    return signature;
  }
  private fail(error:Error):void {
    const data=error as Object as Fields, status=Wire.number(data,'status');
    this.error=data['resultUnknown']===true || data['resultPending']===true ? 'result_unknown' :
      status===403 || status===401 ? 'permission_denied' : status===409 || status===410 ? 'request_updated' : status===404 ? 'task_recovery_gone' : 'failed';
    if (this.error==='result_unknown') this.uncertain=true;
    this.state=this.uncertain ? 'unknown' : this.error==='task_recovery_gone' ? 'gone' : 'stale';
  }
  async refresh():Promise<void> {
    if (this.busy || !this.id) return;
    const run=this.generation; this.busy=true;
    try {
      const result=await this.readAttempt(run);
      if (!this.current(run)) return;
      if (!result || (this.version && result!==this.version)) { this.state='stale'; this.error='request_updated'; return; }
      this.version=result; this.state=this.uncertain ? 'unknown':'ready'; this.error=this.uncertain?'result_unknown':'';
    } catch(e) { if (this.current(run)) this.fail(e as Error); }
    finally { if(this.generation===run) this.busy=false; }
  }
  observeNextAttempt():void {
    const feed=this.feed;
    if (this.busy || this.state!=='starting' || !this.completedAttempt || !feed || !this.current(this.generation)) return;
    const todo=feed.todo(this.id), plan=Wire.record(feed.plans[this.planID]), task=matchingTask(plan,this.id);
    if (!this.belongs(todo) || !Wire.text(todo,'done_at') || !Wire.text(todo,'start_error') || !task ||
      Wire.text(plan,'conversation')!==this.conversation || !ownedManager(plan,this.manager) || Wire.text(plan,'state')==='dismissed') return;
    const next=attemptSignature(todo,plan,task);
    if (next===this.completedAttempt) return;
    this.version=next; this.state='loading'; this.refresh();
  }
  statusMessage():string {
    if (this.error) return this.error;
    if (this.busy || this.state!=='ready' || !this.current(this.generation)) return '';
    if (!this.writable()) return 'permission_denied';
    if (this.pendingWrite()) return 'result_unknown';
    return '';
  }
  private writable():boolean { return !this.readOnly && (this.editableTodo ? this.editableTodo() : !!this.client?.canApprove()); }
  private pendingWrite():boolean {
    const c=this.client;
    return !c || ['retry','remove','answer'].some((action:string)=>c.operationBlocked(this.path(action)));
  }
  canAct(action:string):boolean {
    return ['retry','remove','answer'].includes(action) && this.current(this.generation) && this.state==='ready' &&
      this.writable() && !this.uncertain && !this.busy && !!this.version && !this.pendingWrite() &&
      (action!=='answer' || !!this.question?.open() && !!this.client?.features.includes('todo_start_answer'));
  }
  private offered(choice:string, attempt:string):boolean {
    return !!this.question && this.question.attempt===attempt && !!this.client?.features.includes('todo_start_answer') &&
      this.question.open() && this.question.choices.includes(choice);
  }
  canAnswer(choice:string, attempt:string):boolean { return this.canAct('answer') && this.offered(choice,attempt) && this.version===attempt; }

  private localUnchanged():boolean {
    if (this.selectedTodo) {
      const todo=this.selectedTodo();
      return this.belongs(todo) && !cannotWrite(todo) && this.visibleAttempt(todo)===this.originalTodoAttempt;
    }
    const f=this.feed;
    if (!f || !f.todosReady) return false;
    const todo=f.todo(this.id), plan=Wire.record(f.plans[this.planID]), task=matchingTask(plan,this.id);
    return this.belongs(todo) && !cannotWrite(todo) && !!task && attemptSignature(todo,plan,task)===this.version;
  }
  async act(action:string, value:string='', attempt:string=''):Promise<boolean> {
    const launcher=action==='retry' ? value : '';
    if (!this.canAct(action) || (action==='answer' && !this.canAnswer(value,attempt)) ||
      (launcher && (launcher!==this.alternative || !this.client?.features.includes('task_retry_launcher')))) return false;
    const run=this.generation, version=this.version, owner=this.manager, id=this.id, origin=this.conversation, repo=this.repository!;
    this.busy=true; this.error='';
    try {
      const live=await this.readAttempt(run);
      if (!this.current(run)) return false;
      if (version!==live || !this.localUnchanged()) { this.state='stale'; this.error='request_updated'; return false; }
      if (!this.writable() || this.pendingWrite() || (action==='answer' && !this.offered(value,attempt))) return false;
      const admit=():boolean=>this.current(run) && this.writable() && this.version===version && this.localUnchanged() &&
        (action!=='answer' || this.offered(value,attempt)) &&
        (!launcher || this.client!.features.includes('task_retry_launcher') && this.alternative===launcher);
      const acknowledged=(data:Fields):boolean=>{
        if(action==='remove') return Wire.bool(data,'ok');
        const t=Wire.object(data,'todo'); return Wire.text(t,'id')===id && Wire.text(t,'origin')===origin && ownedManager(t,owner);
      };
      const body=action==='answer' ? Wire.payload(['choice'],[value]) : launcher ? Wire.payload(['launcher'],[launcher]) : Wire.payload([],[]);
      const reply=await repo.call(this.path(action),action==='remove'?'DELETE':'POST',body,'',admit,false,acknowledged,action!=='answer' && !launcher);
      if (!this.current(run)) return false;
      if (!acknowledged(reply)) { this.uncertain=true;this.state='unknown';this.error='result_unknown';return false; }
      this.completedAttempt=version; this.state=action==='remove' ? 'gone' : 'starting'; this.failure=''; this.alternative=''; this.version=''; this.question=undefined;
      if (this.feed) await this.feed.refresh(); else if (this.refreshTodo) await this.refreshTodo();
      return this.current(run);
    } catch(e) { if(this.current(run)) this.fail(e as Error); return false; }
    finally { if(this.generation===run) this.busy=false; }
  }
}
