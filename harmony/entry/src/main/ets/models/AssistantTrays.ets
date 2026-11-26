import { ResourceRow, Wire } from './Wire';
import { TodoWorkspaceItem, TodoWorkspaceAgent, relatedTodoMembers } from './TodoWorkspace';

type Fields = Record<string,Object>;
const hour = 60*60*1000;
function date(value:string):number { return Date.parse(value) || 0; }
function pair(machine:string, session:string):string { return JSON.stringify([machine,session]); }

export class TrayRequest {
  machine:string;
  session:string;
  id:string;
  title:string;
  at:number;
  direct:boolean;
  delegating:boolean;
  constructor(machine:string,session:string,id:string,title:string,at:number,direct:boolean,delegating:boolean=false) {
    this.machine=machine; this.session=session; this.id=id; this.title=title; this.at=at; this.direct=direct; this.delegating=delegating;
  }
}

export class TaskTrayRow {
  id:string;
  kind:string;
  bucket:string;
  status:string;
  title:string;
  note:string;
  data:Fields;
  resource:ResourceRow|undefined;
  at:number=0;
  rank:number=0;
  order:number=0;
  groupHeading:string='';
  agentCount:number=0;
  waitingForOwner:boolean=false;
  constructor(kind:string,id:string,bucket:string,status:string,title:string,note:string,data:Fields,resource?:ResourceRow) {
    this.kind=kind; this.id=id; this.bucket=bucket; this.status=status; this.title=title; this.note=note; this.data=data; this.resource=resource;
  }
  key():string { return this.kind+':'+this.id; }
}

function statusOf(todo:TodoWorkspaceItem, agents:TodoWorkspaceAgent[], lookup:Map<string,ResourceRow>):string {
  if (todo.startError) return 'failed';
  if (todo.isDone()) return 'done';
  if (!agents.length) return 'not_started';
  const states=new Set<string>();
  for (const a of agents) {
    const row=lookup.get(pair(a.machineId,a.sessionId));
    if (!row || a.startError) continue;
    const data=row.data(), state=row.state, work=Wire.object(data,'work');
    if (state==='needs_you') return 'needs_you';
    if (['unread','idle'].includes(state) && Wire.text(data,'turn_error'))
      states.add(Wire.text(data,'turn_error_kind')==='usage_limit' ? 'usage_limit' : 'interrupted');
    else if (state==='working' || Wire.bool(data,'starting')) states.add('working');
    else if (state==='waiting' || Wire.bool(data,'background_working') || Wire.text(work,'phase')==='blocked' &&
      ['agent','external','transient'].includes(Wire.text(work,'on'))) states.add('waiting');
    else states.add(state);
  }
  if (states.has('interrupted') && !states.has('working') && !states.has('waiting') && Wire.text(todo.supervision,'state')!=='parked') return 'interrupted';
  return ['usage_limit','unread','working','waiting'].find((key:string) => states.has(key)) || 'followup';
}

function lastMoved(todo:TodoWorkspaceItem, lookup:Map<string,ResourceRow>):number {
  const reviews=Wire.rows(todo.supervision,'reviews'), latest=reviews[reviews.length-1] || {};
  let at=Math.max(...[todo.createdAt,todo.updatedAt,todo.doneAt,todo.reopenedAt,Wire.text(todo.supervision,'updated_at'),
    Wire.text(Wire.object(todo.supervision,'progress'),'at'),Wire.text(latest,'at'),Wire.text(todo.delivery,'at'),Wire.text(todo.ask,'asked_at')].map(date));
  for (const a of todo.agents) {
    const row=lookup.get(pair(a.machineId,a.sessionId));
    if (!row || a.releasedAt) continue;
    const raw=row.data(); at=Math.max(at,date(Wire.text(raw,'updated_at')),date(Wire.text(raw,'state_since')));
  }
  return at;
}

function closingTime(todo:TodoWorkspaceItem, last:number):number {
  if (todo.doneAt) return date(todo.doneAt);
  if (todo.kind==='concern') return 0;
  const reviews=Wire.rows(todo.supervision,'reviews'), review=reviews[reviews.length-1] || {};
  if (Wire.text(todo.supervision,'state')==='accepted' && Wire.text(review,'verdict')==='accepted') return date(Wire.text(review,'at')) || last;
  if (Wire.text(todo.delivery,'state')==='live') return date(Wire.text(todo.delivery,'at')) || last;
  return todo.hubView?.section==='done' ? last : 0;
}

function taskNote(todo:TodoWorkspaceItem, sessions:ResourceRow[]):string {
  if (todo.startError) return todo.startError;
  if (todo.doneReason) return todo.doneReason;
  if (todo.hubView?.line) return todo.hubView.line;
  const gate=Wire.text(todo.delivery,'gate');
  if (gate) return gate;
  for (const row of sessions) {
    const data=row.data(), note=Wire.text(data,'last_text') || Wire.text(Wire.object(data,'activity'),'text');
    if (note) return note;
  }
  return todo.notes;
}

function independentAgentRows(todos: TodoWorkspaceItem[], lookup: Map<string,ResourceRow>, folded: Set<string>,
  asking?: Map<string,number>): TaskTrayRow[] {
  const rows: TaskTrayRow[] = [];
  const manual=new Set<string>(todos.filter((t:TodoWorkspaceItem) => !t.origin).map((t:TodoWorkspaceItem) => t.id));
  for (const key of lookup.keys()) {
    const s=lookup.get(key); if (!s) continue;
    const d=s.data(), tagged=Wire.text(Wire.object(d,'todo'),'id');
    if (Wire.bool(d,'assistant') || !['needs_you','working'].includes(s.state) || folded.has(key) || (tagged && !manual.has(tagged))) continue;
    if (Wire.bool(d,'archived') || Wire.bool(d,'muted') || Wire.bool(d,'deleted')) continue;
    const parent=Wire.text(d,'parent_session_id') || Wire.text(Wire.object(d,'parent'),'session_id');
    if (parent && lookup.has(pair(s.machine,parent)) && s.state!=='needs_you' && ['needs_you','working'].includes(lookup.get(pair(s.machine,parent))?.state || '')) continue;
    const waits=s.state==='needs_you';
    if (waits && asking && !asking.has(key)) continue;
    const row=new TaskTrayRow('agent',key,waits ? 'needs':'progress',s.state,s.title,'',d,s);
    row.order=waits ? 0 : 5; row.at=waits ? asking?.get(key) || 0 : date(Wire.text(d,'state_since')); rows.push(row);
  }
  return rows;
}

export interface BoardDetails {
  requests: TrayRequest[];
  plans: Fields;
  machineName: (id: string) => string;
  orphanedLine: (reason: string) => string;
}

function boardAgentName(session: ResourceRow): string {
  const data=session.data(), agent=Wire.text(data,'agent'), source=Wire.text(data,'source_kind');
  if (agent==='codex' || source==='codex_history' || source==='codex_app_server') return 'Codex';
  if (!agent || agent==='claude') return 'Claude';
  if (['traex','traecli','trae-cli'].includes(agent)) return 'TRAE CLI';
  const label=Wire.text(data,'agent_label'), launcher=Wire.text(data,'launcher');
  if (agent.toLowerCase()==='paseo' && launcher && (!label || agent.toLowerCase()===label.toLowerCase())) return launcher;
  return label || agent;
}

function boardFolder(path: string): string {
  const parts=path.split('/').filter((part:string) => !!part);
  return parts[parts.length-1] || '';
}

function boardQuestion(sessions: ResourceRow[], requests: TrayRequest[]): string {
  const keys=new Set<string>(sessions.map((s:ResourceRow) => pair(s.machine,s.session || s.id)));
  const pending=requests.filter((request:TrayRequest) => keys.has(pair(request.machine,request.session)) && !!request.title.trim());
  pending.sort((a:TrayRequest,b:TrayRequest) => a.at-b.at || a.id.localeCompare(b.id));
  return pending[0]?.title.trim().split(/\s*\n\s*/).join(' ') || '';
}

function boardSubtitle(todo: TodoWorkspaceItem, sessions: ResourceRow[], planned: Fields, state: string, details: BoardDetails): string {
  const priorities=['needs_you','unread','working','idle','unknown','offline'];
  const priority=(s:ResourceRow):number => { const ix=priorities.indexOf(s.state); return ix<0 ? 4 : ix; };
  const lead=sessions.slice().sort((a:ResourceRow,b:ResourceRow) => priority(a)-priority(b) ||
    date(Wire.text(b.data(),'updated_at'))-date(Wire.text(a.data(),'updated_at')))[0];
  const first=todo.activeAgents()[0] || todo.agents[0];
  const machine=first?.machineId || Wire.text(planned,'machine'), folder=boardFolder(first ? first.cwd : Wire.text(planned,'cwd'));
  const place=[machine ? details.machineName(machine) : '',folder,lead ? boardAgentName(lead) : ''].filter((s:string) => !!s).join(' · ');
  const data=lead?.data() || {}, words=Wire.text(data,'last_text').trim(), step=Wire.text(data,'activity').trim();
  const failure=todo.startError || (!todo.agents.length ? Wire.text(planned,'error') : '');
  const question=state==='needs_you' ? boardQuestion(sessions,details.requests) : '';
  const raw=todo.data(), orphan=Wire.object(raw,'orphaned');
  if (!failure && !question && state!=='needs_you' && !todo.isDone() && Wire.has(raw,'orphaned') && raw['orphaned'] &&
      typeof raw['orphaned']==='object' && !Array.isArray(raw['orphaned'])) {
    return [details.orphanedLine(Wire.text(orphan,'reason')),place].filter((s:string) => !!s).join(' · ');
  }
  const progress=step && (state==='working' || !words) ? step : words;
  return [place,failure || question || progress].filter((s:string) => !!s).join(' · ');
}

/** The board keeps every task handed out by this conversation, including older finished work. */
export function assistantBoardRows(directory: Fields[], sessions: ResourceRow[], manager: string, group: string,
  conversation: string, main: string, details?: BoardDetails): TaskTrayRow[] {
  if (!conversation) return [];
  const lookup = new Map<string,ResourceRow>();
  for (const row of sessions) {
    if (row.manager === manager && (!row.group || row.group === group)) lookup.set(pair(row.machine,row.session || row.id),row);
  }
  const todos = directory.filter((raw: Fields) => !!Wire.text(raw,'id') && Wire.text(raw,'manager',manager) === manager)
    .map((raw: Fields) => new TodoWorkspaceItem(raw,manager));
  const folded = new Set<string>(), rows: TaskTrayRow[] = [];
  const plans = details?.plans || {};
  const planTasks = new Map<string,Fields>();
  for (const key of Object.keys(plans)) {
    const plan=Wire.record(plans[key]);
    if (Wire.text(plan,'state')!=='dismissed') for (const task of Wire.rows(plan,'tasks')) {
      const id=Wire.text(task,'todo_id'); if (id && !planTasks.has(id)) planTasks.set(id,task);
    }
  }
  for (const todo of todos) {
    const active=todo.activeAgents();
    if (todo.origin && !todo.isDone()) for (const agent of active) folded.add(pair(agent.machineId,agent.sessionId));
    if (todo.origin !== conversation) continue;
    const ownPlan=Wire.record(plans[todo.planId]);
    const planned=Wire.rows(ownPlan,'tasks').find((task:Fields) => Wire.text(task,'todo_id')===todo.id) || planTasks.get(todo.id) || {};
    let status=statusOf(todo,active,lookup);
    if (status==='not_started' && !todo.agents.length && Wire.text(planned,'error')) status='failed';
    else if (!todo.isDone() && status!=='failed' && Wire.text(todo.ask,'asked_at')) status='needs_you';
    const linked:ResourceRow[]=[];
    for (const agent of active) { const session=lookup.get(pair(agent.machineId,agent.sessionId)); if (session) linked.push(session); }
    const data=todo.data(), resource=ResourceRow.from(data,'todo'); resource.manager=manager; resource.group=group;
    const note=details ? boardSubtitle(todo,linked,planned,status,details) : '';
    const row = new TaskTrayRow('todo',todo.id,'',status,todo.title,note,data,resource);
    row.at=date(todo.createdAt); rows.push(row);
  }
  rows.sort((a:TaskTrayRow,b:TaskTrayRow) => b.at-a.at || a.id.localeCompare(b.id));
  if (conversation === main) {
    const agents=independentAgentRows(todos,lookup,folded);
    agents.sort((a:TaskTrayRow,b:TaskTrayRow) => a.order-b.order || b.at-a.at || a.id.localeCompare(b.id));
    for (const agent of agents) {
      const row=agent.resource;
      if (details && row) {
        const data=row.data();
        const question=row.state==='needs_you' ? boardQuestion([row],details.requests) : '';
        agent.note=[details.machineName(row.machine),boardFolder(Wire.text(data,'cwd')),boardAgentName(row),
          question || Wire.text(data,'last_text').trim()].filter((v:string) => !!v).join(' · ');
      }
      rows.push(agent);
    }
  }
  for (const row of rows) {
    if (row.status === 'needs_you') row.bucket='needs';
    else if (['done','failed','unread'].includes(row.status)) row.bucket=row.status;
    else if (['followup','interrupted','usage_limit'].includes(row.status)) row.bucket='followup';
    else row.bucket='working';
  }
  return rows;
}

/** Projects the main supervisor's directory or an ordinary conversation's own slots using the same links as iOS. */
export function assistantTrayRows(directory:Fields[], sessions:ResourceRow[], items:Fields[], plans:Fields, requests:TrayRequest[],
  manager:string,group:string,conversation:string,main:string,now:number):TaskTrayRow[] {
  if (!conversation) return [];
  const isMain=!!main && main===conversation, lookup=new Map<string,ResourceRow>(), rows:TaskTrayRow[]=[];
  for (const row of sessions) {
    if (row.manager===manager && (!row.group || row.group===group)) lookup.set(pair(row.machine,row.session || row.id),row);
  }
  const cards=new Map<string,string>(), slots=new Map<string,string>(), dates=new Map<string,number>(), todoPlans=new Map<string,Fields>();
  const todos=directory.filter((t:Fields) => !!Wire.text(t,'id') && Wire.text(t,'manager',manager)===manager).map((t:Fields) => new TodoWorkspaceItem(t,manager));
  for (const id of Object.keys(plans).sort()) {
    const plan=Wire.record(plans[id]);
    if (Wire.text(plan,'conversation')!==conversation) continue;
    Wire.rows(plan,'tasks').forEach((task:Fields,index:number) => {
      const id=Wire.text(task,'todo_id'); if (!id || slots.has(id)) return;
      slots.set(id,'plan:'+Wire.text(plan,'id')+':'+String(index)); dates.set(id,date(Wire.text(plan,'created_at'))); todoPlans.set(id,task);
    });
  }
  for (const item of items) if (['task','receipt','report','review'].includes(Wire.text(item,'kind'))) cards.set(Wire.text(item,'ref'),Wire.text(item,'at'));
  const included=todos.filter((t:TodoWorkspaceItem) => isMain || t.origin===conversation || slots.has(t.id) || cards.has(t.id));
  const when=(t:TodoWorkspaceItem):number => dates.get(t.id) ?? (date(t.createdAt) || date(cards.get(t.id) || ''));
  const slot=(t:TodoWorkspaceItem):string => slots.get(t.id) || 'task:'+t.id;
  const home=new Map<string,string>(), folded=new Set<string>();
  for (const t of included.slice().sort((a:TodoWorkspaceItem,b:TodoWorkspaceItem) => when(a)-when(b) || slot(a).localeCompare(slot(b)))) {
    if (t.isDone()) continue;
    for (const agent of t.activeAgents()) {
      const key=pair(agent.machineId,agent.sessionId), linked=lookup.get(key);
      if (!home.has(key) || (linked && Wire.text(Wire.object(linked.data(),'todo'),'id')===t.id)) home.set(key,t.id);
    }
  }
  const asking=new Map<string,number>(), routed=new Set<string>(), userCards=new Set<string>();
  for (const req of requests) {
    const key=pair(req.machine,req.session); routed.add(key);
    if (req.delegating) continue;
    userCards.add(key);
    if (!isMain || req.direct) asking.set(key,Math.min(asking.get(key) ?? req.at,req.at));
  }
  for (const todo of included) {
    const active=todo.isDone() ? [] : todo.activeAgents(), own=active.filter((a:TodoWorkspaceAgent) => home.get(pair(a.machineId,a.sessionId))===todo.id);
    let status=statusOf(todo,active,lookup);
    if (status==='not_started' && !todo.agents.length && Wire.text(todoPlans.get(todo.id) || {},'error')) status='failed';
    if (status==='needs_you' && own.length<active.length) {
      const ownStatus=statusOf(todo,own,lookup); status=ownStatus==='not_started' ? 'working' : ownStatus;
    }
    const nowAsks=own.map((a:TodoWorkspaceAgent) => asking.get(pair(a.machineId,a.sessionId))).filter((at:number|undefined):boolean => at!==undefined);
    const last=lastMoved(todo,lookup), closed=closingTime(todo,last), sup=todo.supervision;
    const review=Wire.text(sup,'state'), reviewAt=date(Wire.text(sup,'pending_since'));
    const reviewing=!todo.isDone() && review==='pending_review' && reviewAt>0 && now-reviewAt<hour;
    const ask=!todo.isDone() && !!Wire.text(todo.ask,'verdict') && !!Wire.text(todo.ask,'asked_at');
    const outcome=Wire.text(todo.ask,'outcome');
    const directDecision=!todo.isDone() && (['user_check','needs_user'].includes(review) ||
      ask && (!!Wire.text(todo.ask,'question') || (Array.isArray(todo.ask['options']) && (todo.ask['options'] as Object[]).length>0) ||
        !outcome || outcome==='decide' || !!Wire.text(todo.ask,'why_owner')));
    const decide=ask || !todo.isDone() && (['user_check','needs_user'].includes(review) || review==='pending_review' && !reviewing);
    const held=!todo.isDone() && Wire.text(todo.delivery,'unblock')==='user' && ['pr','merged','off'].includes(Wire.text(todo.delivery,'state'));
    const failed=status==='failed', supervised=!!Wire.text(sup,'supervisor').trim();
    const sessionKeys=own.map((a:TodoWorkspaceAgent)=>pair(a.machineId,a.sessionId));
    const supervisorHoldsAll=supervised && sessionKeys.some((key:string)=>routed.has(key)) &&
      !sessionKeys.some((key:string)=>userCards.has(key));
    const legacyWaits=failed ? !supervised : ['needs_you','interrupted'].includes(status) && !supervisorHoldsAll;
    const replaced=failed && relatedTodoMembers(todos,todo).some((other:TodoWorkspaceItem) => other.id!==todo.id && !!other.doneAt && !other.startError);
    const stale=todo.hubView?.section==='shelved' || (!todo.hubView && todo.kind!=='concern' && (todo.origin || todo.agents.length) &&
      !(todo.dueBy>=now) && ['not_started','followup','waiting'].includes(status) && !todo.prUrl && !review && now-last>=48*hour);
    let bucket='progress', order=4;
    if (nowAsks.length && (!isMain || !failed)) { bucket='needs'; order=0; }
    else if (legacyWaits || held || decide) {
      // These entries belong to Needs You in a regular conversation. Its main supervisor only shows direct decisions.
      if (isMain && (failed || !held && !directDecision)) {
        for (const agent of active) folded.add(pair(agent.machineId,agent.sessionId));
        continue;
      }
      if (failed && (replaced || now-last>=48*hour)) continue;
      bucket='needs'; order=2;
    }
    else if (closed && !reviewing) { if (!todo.origin || now-closed>=24*hour) continue; bucket='done'; }
    else if (stale && !reviewing) { bucket='shelved'; }
    for (const a of active) folded.add(pair(a.machineId,a.sessionId));
    const raw=Wire.parse(todo.raw), resource=ResourceRow.from(raw,'todo'); resource.manager=manager; resource.group=group;
    const linked:ResourceRow[]=[]; for(const a of active) { const row=lookup.get(pair(a.machineId,a.sessionId)); if(row) linked.push(row); }
    const row=new TaskTrayRow('todo',todo.id,bucket,held ? 'needs_you' : status,todo.title,taskNote(todo,linked),raw,resource);
    row.agentCount=active.length;
    if (row.status === 'needs_you') {
      const request = requests.filter((req:TrayRequest) => !req.delegating && active.some((agent:TodoWorkspaceAgent) =>
        agent.machineId===req.machine && agent.sessionId===req.session)).sort((a:TrayRequest,b:TrayRequest) => a.at-b.at || a.id.localeCompare(b.id))
        .find((req:TrayRequest) => !!req.title.trim());
      row.note=request?.title.trim().split(/\s*\n\s*/).join(' ') || '';
      row.waitingForOwner=!row.note;
    }
    row.order=order; row.at=nowAsks.length ? Math.min(...nowAsks as number[]) : bucket==='done' ? closed : when(todo);
    const ranking=['needs_you','interrupted','usage_limit','owner_review','failed','working','waiting','not_started','unread','followup','done'];
    row.rank=ranking.indexOf(['not_started','followup','unread'].includes(status) && (review==='pending_review' || ask) ? 'owner_review' : status); rows.push(row);
  }
  if (isMain) rows.push(...independentAgentRows(todos,lookup,folded,asking));
  for (const id of Object.keys(plans).sort()) {
    const plan=Wire.record(plans[id]), origin=Wire.text(plan,'conversation');
    if (!origin || Wire.text(plan,'id')!==id || Wire.text(plan,'state')!=='open' ||
        Wire.text(plan,'manager',manager)!==manager || Wire.text(plan,'group',group)!==group ||
        origin!==conversation && (!isMain || Wire.text(plan,'space')==='health')) continue;
    const first=Wire.rows(plan,'tasks')[0] || {};
    const row=new TaskTrayRow('plan',id,'needs','plan',Wire.text(first,'title'),Wire.text(first,'prompt'),plan);
    row.order=1; row.at=date(Wire.text(plan,'created_at')); rows.push(row);
  }
  for (const item of items) {
    const kind=Wire.text(item,'kind'), ref=Wire.text(item,'ref'), id=Wire.text(item,'id'), body=Wire.object(item,'body');
    if (!id || !ref) continue;
    if(kind==='message' && !Wire.text(item,'sent_at') && !Wire.text(item,'filed_at') && !Wire.text(body,'sent_at') && !Wire.text(body,'filed_at') && !Wire.text(body,'dismissal_id') && !Wire.text(body,'dismissed_at')) {
      const row=new TaskTrayRow('message',id,'needs','message',[Wire.text(body,'sender'),Wire.text(body,'chat')].filter((s:string) => !!s).join(' · '),(Wire.text(body,'text') || Wire.text(body,'quote')).split('\n')[0],item);
      row.order=3; row.at=date(Wire.text(item,'at')); rows.push(row);
    }
  }
  const sorted=rows.sort((a:TaskTrayRow,b:TaskTrayRow) => a.order-b.order || (a.order===0 ? a.at-b.at : a.rank-b.rank) || (a.order<=1 ? a.at-b.at : b.at-a.at));
  if (!isMain) return sorted;
  const groups=new Map<string,TaskTrayRow[]>();
  groups.set('',[]);
  for (const row of sorted.filter((r:TaskTrayRow) => r.bucket==='needs')) {
    const origin=row.kind==='todo' ? Wire.text(row.data,'origin') : '', group=origin && origin!==conversation ? origin : '';
    const members=groups.get(group) || [];
    members.push(row); groups.set(group,members);
  }
  const needs:TaskTrayRow[]=[];
  const grouped=Array.from(groups.values()).filter((m:TaskTrayRow[]) => m.length>0).length>1;
  for (const id of groups.keys()) {
    const members=groups.get(id) || []; if (grouped && id && members.length) members[0].groupHeading=id;
    needs.push(...members);
  }
  return needs.concat(sorted.filter((r:TaskTrayRow) => r.bucket!=='needs'));

}
