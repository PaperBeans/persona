import type { TodoWorkspaceItem } from './TodoWorkspace';
import { ResourceRow, Wire } from './Wire';

/** The iPhone's ordered list sections. The task detail uses its independent, six-state status. */
export const TODO_PHONE_SECTIONS:string[]=['needs_you','working','pr','other','just_done','shelved','done'];
export interface TodoPhonePlacement { section:string; last:number; superseded:boolean; awaitingReview:boolean; }

export function phoneTodoSeenCanFile(todo:TodoWorkspaceItem):boolean {
  return (todo.kind==='concern' || Wire.text(todo.delivery,'state')!=='live') &&
    Wire.text(todo.supervision,'state')!=='pending_review' && !Wire.text(todo.supervision,'supervisor');
}

export class TodoPhoneRowContext {
  canFileSeen:boolean = false;
  showAgentMetadata:boolean = true;
  lead: ResourceRow | undefined = undefined;
  activeAgents: number = 0;
  urgentState: string = '';
  taskState: string = 'not_started';
  shownState: string = 'not_started';
  statusKey: string = 'todo_state_not_started';
  reviewKey: string = '';
  reviewTone: string = '';
  reviewRound: number = 0;
  reviewCount: string = '';
  summary: string = '';
  summaryKind: string = 'empty';
}

function time(value:string):number {
  const date=Date.parse(value); return Number.isFinite(date) ? date : Number.NEGATIVE_INFINITY;
}

// A task can be waiting while its Agent has a working dot. A question or an unread turn takes priority.
function taskAgentState(agentFailed: boolean, session: ResourceRow | undefined, data: Record<string,Object> | undefined): string {
  if (agentFailed || !session || !data) return 'offline';
  const state=session.state || Wire.text(data,'state');
  if (state==='needs_you' || state==='unread') return state;
  if (Wire.bool(data,'starting')) return 'working';
  const work=Wire.object(data,'work');
  if (state==='working') return Wire.bool(data,'background_working') || Wire.bool(data,'background_waiting') ? 'waiting' : 'working';
  return Wire.text(work,'phase')==='blocked' && ['agent','external','transient'].includes(Wire.text(work,'on')) ? 'waiting' : state;
}

function reviewCount(supervision:Record<string,Object>, reviews:Record<string,Object>[], accepted:boolean):string {
  const latest=new Map<string,boolean>();
  for (const review of reviews) for (const check of Wire.rows(review,'checks')) {
    latest.set(Wire.text(check,'criterion'),Wire.bool(check,'met'));
  }
  const criteria=new Set<string>(latest.keys());
  if (!accepted) for (const criterion of Wire.strings(supervision,'done_when')) criteria.add(criterion);
  let met=0;
  for (const criterion of criteria) if (latest.get(criterion)) met++;
  return criteria.size ? String(met)+'/'+String(criteria.size) : '';
}

function phoneRowStatus(todo:TodoWorkspaceItem,row:TodoPhoneRowContext,states:Set<string>):void {
  const done=todo.isDone(), openAsk=!done && !!Wire.text(todo.ask,'asked_at');
  let state='not_started';
  if (todo.startError) state='failed';
  else if (done) state='done';
  else if (openAsk || states.has('needs_you')) state='needs_you';
  else if (states.has('unread')) state='unread';
  else if (states.has('working')) state='working';
  else if (states.has('waiting')) state='waiting';
  else if (row.activeAgents) state='followup';
  row.taskState=state;
  row.shownState=state;
  switch (state) {
    case 'needs_you': row.statusKey='phone_todo_section_needs'; break;
    case 'working': row.statusKey='phone_todo_section_working'; break;
    case 'unread': row.statusKey='agent_task_state_unread'; break;
    case 'waiting': row.statusKey='waiting'; break;
    case 'followup': row.statusKey='assistant_feed_state_followup'; break;
    case 'failed': row.statusKey='phone_todo_did_not_start'; break;
    case 'done': row.statusKey='todo_state_done'; break;
  }

  const data=todo.data(), sup=todo.supervision, reviews=Wire.rows(sup,'reviews');
  const last=reviews[reviews.length-1], rawReview=Wire.text(sup,'state');
  const accepted=rawReview==='accepted' && !!last && Wire.text(last,'verdict')==='accepted';
  let review=rawReview;
  if (done && review==='user_check') review='accepted';
  else if (!done && (review==='cancelled' || review==='accepted' && !accepted)) review='';
  const decision=Wire.object(data,'decision_review'), round=Wire.text(decision,'review_at');
  const mostRecent=last ? time(Wire.text(last,'at')) : Number.NEGATIVE_INFINITY;
  const dismissed=!!Wire.text(decision,'dismissal_id') && !!round && (!Number.isFinite(mostRecent) || time(round)===mostRecent);
  const ownerWait=!done && !dismissed && (review==='needs_user' || openAsk && ['decide','cannot'].includes(Wire.text(todo.ask,'outcome')));
  const resting=state==='followup' || state==='unread' || state==='not_started';
  const acceptedAtRest=!done && todo.kind!=='concern' && !ownerWait && accepted && resting;
  const delivered=!done && !ownerWait && resting && (review==='pending_review' || openAsk || todo.kind!=='concern' && Wire.text(todo.delivery,'state')==='live');
  if (acceptedAtRest) row.shownState='done';
  else if (ownerWait) row.shownState='needs_you';
  else if (delivered) row.shownState='unread';

  if (Wire.text(data,'cancelled_at') || todo.doneBy==='cancelled' || todo.doneBy==='review' && review==='cancelled') row.statusKey='phone_todo_row_cancelled';
  else if (acceptedAtRest || state==='done' && todo.doneBy==='review') row.statusKey='phone_todo_row_accepted';
  else if (state==='done' && todo.doneBy==='auto') row.statusKey='phone_todo_row_auto_done';
  else if (ownerWait) row.statusKey='phone_todo_section_needs';
  else if (delivered) row.statusKey='phone_todo_section_delivered';

  if (done || !data['supervision'] || typeof data['supervision']!=='object' || Array.isArray(data['supervision']) ||
    todo.kind==='concern' && review==='accepted') return;
  switch (review) {
    case 'pending_review': case 'user_check': case 'rework': row.reviewTone='caution'; break;
    case 'needs_user': row.reviewTone='danger'; break;
    case 'accepted': row.reviewTone='positive'; break;
    default: review='ongoing'; row.reviewTone='neutral'; break;
  }
  row.reviewKey=review==='accepted' ? 'phone_todo_row_accepted' : review==='pending_review' ? 'agent_task_review' : 'phone_todo_review_'+review;
  row.reviewRound=Math.max(1, Wire.number(sup,'round'));
  row.reviewCount=reviewCount(sup,reviews,accepted);
}

export function phoneTodoTitle(section:string):string {
  switch (section) {
    case 'needs_you': return 'phone_todo_section_needs';
    case 'working': return 'phone_todo_section_working';
    case 'pr': return 'phone_todo_section_pr';
    case 'other': return 'phone_todo_section_open';
    case 'just_done': return 'phone_todo_section_recent';
    case 'shelved': return 'phone_todo_section_shelved';
    default: return 'phone_todo_section_done';
  }
}

export class TodoPhoneSections {
  private sessions:Map<string,ResourceRow>=new Map<string,ResourceRow>();
  private now:number;
  constructor(rows:ResourceRow[],now:number) {
    this.now=now;
    rows.forEach((row:ResourceRow)=>{
      if (row.kind==='session') this.sessions.set(JSON.stringify([row.manager,row.machine,row.session || row.id]),row);
    });
  }

  private session(manager: string, machine: string, id: string): ResourceRow | undefined {
    return this.sessions.get(JSON.stringify([manager, machine, id])) || this.sessions.get(JSON.stringify(['', machine, id]));
  }

  rowContext(todo: TodoWorkspaceItem, placement?: TodoPhonePlacement): TodoPhoneRowContext {
    const row = new TodoPhoneRowContext();
    row.canFileSeen=placement?.section==='just_done' && phoneTodoSeenCanFile(todo);
    row.showAgentMetadata=placement?.section!=='pr';
    let best = -1, leadMoved = Number.NEGATIVE_INFINITY;
    const taskStates=new Set<string>();
    for (const agent of todo.agents) {
      if (agent.releasedAt) continue;
      row.activeAgents++;
      const candidate = this.session(todo.manager, agent.machineId, agent.sessionId);
      const data = candidate?.data();
      taskStates.add(taskAgentState(!!agent.startError,candidate,data));
      if (candidate && data) {
        const moved = Math.max(time(Wire.text(data, 'updated_at')), time(Wire.text(data, 'state_since')));
        const working = candidate.state === 'working', previousWorking = row.lead?.state === 'working';
        if (!row.lead || (working !== previousWorking ? working : moved > leadMoved)) {
          row.lead = candidate;
          leadMoved = moved;
        }
      }
      const state = agent.startError ? 'offline' : candidate?.state || 'offline';
      const urgency = agent.startError ? 1 : state === 'needs_you' ? 4 : state === 'unread' ? 3 : state === 'working' ? 2 : 0;
      if (urgency <= best) continue;
      best = urgency;
      row.urgentState = state;
      row.summary = agent.startError || (candidate && data ? Wire.text(data, 'last_text') || candidate.title : agent.title);
      row.summaryKind = agent.startError ? 'failure' : 'session';
    }
    phoneRowStatus(todo,row,taskStates);
    const raw = todo.data(), closedBy = Wire.text(raw, 'done_by');
    const cancelled = !!Wire.text(raw, 'cancelled_at') || closedBy === 'cancelled' ||
      closedBy === 'review' && Wire.text(todo.supervision, 'state') === 'cancelled';
    const reason = cancelled ? (Wire.text(raw, 'cancelled_reason') || todo.doneReason).trim() :
      todo.isDone() && ['auto', 'review'].includes(closedBy) ? todo.doneReason.trim() : '';
    if (todo.startError && (!todo.isDone() || !placement || placement.section === 'needs_you')) {
      row.summaryKind = 'failure';
      row.summary = todo.startError;
    } else if (reason) {
      row.summaryKind = 'closed';
      row.summary = reason;
    } else if (!row.summary) {
      row.summaryKind = todo.notes ? 'notes' : 'empty';
      row.summary = todo.notes;
    }
    return row;
  }

  place(todo:TodoWorkspaceItem,superseded:boolean=false,seenOverride?:string):TodoPhonePlacement {
    const sup=todo.supervision,reviews=Wire.rows(sup,'reviews'),review:Record<string,Object>=reviews[reviews.length-1] || Wire.payload([],[]);
    const reviewAt=time(Wire.text(review,'at')),delivery=Wire.text(todo.delivery,'state'),deliveredAt=time(Wire.text(todo.delivery,'at'));
    const askTime=time(Wire.text(todo.ask,'asked_at')),doneAt=time(todo.doneAt),seenAt=time(seenOverride ?? Wire.text(todo.data(),'seen_at'));
    let last=Math.max(time(todo.updatedAt),time(todo.createdAt),time(todo.reopenedAt),doneAt,time(Wire.text(sup,'updated_at')),
      time(Wire.text(Wire.object(sup,'progress'),'at')),reviewAt,deliveredAt,askTime);
    let asks=false,interrupted=false,limit=false,unread=false,working=false;
    for (const a of todo.activeAgents()) {
      const row=this.session(todo.manager,a.machineId,a.sessionId);
      if (!row) continue;
      const data=row.data(),state=row.state || Wire.text(data,'state');
      last=Math.max(last,time(Wire.text(data,'updated_at')),time(Wire.text(data,'state_since')));
      if (a.startError) continue;
      const stopped=state==='idle' || state==='unread';
      if (state==='needs_you') asks=true;
      else if (stopped && Wire.text(data,'turn_error')) {
        if (Wire.text(data,'turn_error_kind')==='usage_limit') limit=true;
        else interrupted=true;
      } else if (state==='unread') unread=true;
      else if (state==='working' || Wire.bool(data,'starting')) working=true;
    }
    const current=(when:number):boolean=>this.now-when<86400000 && !(Number.isFinite(seenAt) && seenAt>=when);
    const concern=todo.kind==='concern' && !todo.isDone(),reviewState=Wire.text(sup,'state');
    const openAsk=!!Wire.text(todo.ask,'verdict') && Number.isFinite(askTime),outcome=Wire.text(todo.ask,'outcome');
    const need=reviewState==='needs_user' || reviewState==='user_check' || openAsk && (outcome==='decide' || outcome==='cannot') ||
      Wire.text(todo.delivery,'unblock')==='user' && ['pr','merged','off'].includes(delivery);
    const finished=!todo.isDone() && !limit && unread || reviewState==='pending_review' || openAsk && (!outcome || outcome==='done');
    let section='other';
    if (todo.startError) section=superseded ? 'done' :
      this.now-last<172800000 ? 'needs_you' : concern ? 'other' : 'done';
    else if (!todo.isDone() && (asks || interrupted || need)) section='needs_you';
    else if (!todo.isDone() && !limit && !unread && working) section='working';
    else if (todo.isDone()) section=todo.kind==='concern' && current(doneAt) ? 'just_done' : 'done';
    else if (!concern && reviewState==='accepted' && Wire.text(review,'verdict')==='accepted') section='done';
    else if (!concern && delivery==='live') section='just_done';
    else if (finished && reviewState!=='parked' && (!(Number.isFinite(seenAt) && seenAt>=last) || !phoneTodoSeenCanFile(todo))) section='just_done';
    else if (delivery==='pr' || !!todo.prUrl && (!delivery || delivery==='none')) section='pr';
    else if (finished && !concern) section='done';
    else if (!concern && (todo.agents.length>0 || !!todo.origin) && !(todo.dueBy>=this.now) && this.now-last>=172800000) section='shelved';
    return {section:section,last:last,superseded:superseded,awaitingReview:section==='just_done' && !todo.isDone()};
  }
}
