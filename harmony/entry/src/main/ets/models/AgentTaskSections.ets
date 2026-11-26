import { ResourceRow, Wire } from './Wire';
import { TodoWorkspaceAgent, TodoWorkspaceItem } from './TodoWorkspace';

export class AgentTaskLine {
  kind: string;
  row: ResourceRow;
  depth: number;
  state: string;
  constructor(kind: string, row: ResourceRow, depth: number = 0, state: string = '') {
    this.kind = kind; this.row = row; this.depth = depth; this.state = state;
  }
}

export class AgentTaskSection {
  key: string;
  title: string = '';
  titleKey: string = '';
  hint: string = '';
  state: string = '';
  folded: boolean = false;
  todo: ResourceRow | undefined;
  direction: boolean = false;
  needs: number = 0;
  running: boolean = false;
  lines: AgentTaskLine[] = [];
  constructor(key: string) { this.key = key; }
}

function stateOf(data: Record<string, Object>): string {
  const raw = Wire.text(data, 'state') || Wire.text(data, 'status');
  if (raw === 'blocked') return 'needs_you';
  if (raw === 'done') return 'unread';
  return ['needs_you','working','unread','idle','offline'].includes(raw) ? raw : 'unknown';
}

function sessionID(machine: string, session: string): string { return JSON.stringify([machine,session]); }
function belongs(row: ResourceRow, group: string, manager: string): boolean {
  return (!row.group || row.group === group) && (!row.manager || row.manager === manager);
}

function waitingForWork(data: Record<string, Object>): boolean {
  const progress = Wire.object(data, 'workflow_progress');
  const active = (item: Record<string, Object>): boolean => Wire.bool(item, 'running', Wire.text(item, 'status') === 'running');
  const workflows = Wire.has(data, 'waiting') ? active(progress) : Wire.rows(data, 'workflows').some(active);
  return !!data['loop'] || Wire.rows(data, 'background').length > 0 || Wire.rows(data, 'subagents').some((agent: Record<string,Object>) => Wire.bool(agent,'running')) || workflows;
}

class Member {
  row: ResourceRow;
  key: string;
  state: string;
  manual: boolean;
  todo: string;
  rank: number;
  at: number;
  wakes: boolean;
  starting: boolean;
  constructor(row: ResourceRow, muted: boolean) {
    this.row = row;
    this.key = sessionID(row.machine, row.session || row.id);
    const data = row.data(), work = Wire.object(data,'work');
    this.state = stateOf(data);
    this.todo = Wire.text(Wire.object(data,'todo'),'id');
    this.manual = Wire.text(data,'space_kind') === 'herdr' && !Wire.bool(data,'started_by_persona',Wire.bool(data,Wire.legacyStartedBy));
    const waiting = ['idle','unknown'].includes(this.state) && waitingForWork(data);
    this.wakes = this.state === 'working' ? Wire.bool(data,'background_working') || Wire.bool(data,'background_waiting') :
      !['needs_you','unread'].includes(this.state) && Wire.text(work,'phase') === 'blocked' && ['agent','external','transient'].includes(Wire.text(work,'on'));
    this.starting = Wire.bool(data,'starting');
    this.rank = this.state === 'needs_you' ? 0 : muted && (waiting || ['unread','working'].includes(this.state)) ? 4 :
      this.state === 'unread' ? 1 : this.state === 'working' ? 2 : waiting ? 3 : 5;
    const since = this.state === 'working' ? Date.parse(Wire.text(data,'state_since')) : Number.NaN;
    this.at = Number.isFinite(since) ? since : Date.parse(Wire.text(data,'updated_at')) || 0;
  }
}

function compareMember(a: Member, b: Member): number {
  return a.rank - b.rank || b.at - a.at || (a.row.session || a.row.id).localeCompare(b.row.session || b.row.id) || a.row.machine.localeCompare(b.row.machine);
}

class GroupTodo {
  row: ResourceRow;
  item: TodoWorkspaceItem;
  state: string;
  rank: number;
  due: number;
  created: number;
  constructor(row: ResourceRow, byID: Map<string, Member>) {
    this.row = row;
    this.item = new TodoWorkspaceItem(row.data(), row.manager);
    this.due = Number.isFinite(this.item.dueBy) ? this.item.dueBy : Number.POSITIVE_INFINITY;
    this.created = Date.parse(this.item.createdAt) || 0;
    const states = this.item.agents.filter((agent: TodoWorkspaceAgent) => !agent.releasedAt).map((agent: TodoWorkspaceAgent): string => {
      const member = byID.get(sessionID(agent.machineId, agent.sessionId));
      if (agent.startError || !member) return 'offline';
      if (['needs_you','unread'].includes(member.state)) return member.state;
      if (member.starting) return 'working';
      return member.wakes ? 'waiting' : member.state;
    });
    const ranks = ['needs_you','unread','working','waiting','followup','not_started','done','failed'];
    this.state = this.item.startError ? 'failed' : Wire.text(this.item.ask,'asked_at') ? 'needs_you' :
      ranks.slice(0,4).find((key: string) => states.includes(key)) || (states.length ? 'followup' : 'not_started');
    this.rank = ranks.indexOf(this.state);
  }
}

function extras(members: Member[], keepEmpty: boolean, belowTodos: boolean, belowDirections: boolean): AgentTaskSection[] {
  const result: AgentTaskSection[] = [];
  for (const manual of [true,false]) {
    const rows = members.filter((member: Member) => member.manual === manual).sort(compareMember);
    if (!rows.length && !keepEmpty) continue;
    const section = new AgentTaskSection(manual ? 'todo:manual' : 'todo:none');
    section.titleKey = manual ? 'agent_task_manual' : 'agent_task_unassigned';
    section.hint = manual ? 'agent_task_manual_hint' : 'agent_task_unassigned_hint';
    section.folded = (belowTodos || (!manual && belowDirections)) && !rows.some((member: Member) => member.state === 'needs_you');
    section.state = ['needs_you','unread','working','idle'].find((state: string) => rows.some((member: Member) => member.state === state)) || 'idle';
    section.lines = rows.map((member: Member) => new AgentTaskLine('session',member.row));
    result.push(section);
  }
  return result;
}

function liveMembers(shown: ResourceRow[], muted: (row: ResourceRow) => boolean): Member[] {
  return shown.filter((row: ResourceRow) => row.kind === 'session').map((row: ResourceRow) => new Member(row,muted(row)))
    .filter((member: Member) => member.state !== 'offline');
}

export function todoAgentSections(todos: ResourceRow[], shown: ResourceRow[], group: string, manager: string,
  filtering: boolean, muted: (row: ResourceRow) => boolean): AgentTaskSection[] {
  const live = liveMembers(shown,muted), byID = new Map<string, Member>(), byTodo = new Map<string, Member[]>();
  for (const member of live) {
    if (!belongs(member.row,group,manager)) continue;
    byID.set(member.key,member);
    if (member.todo) {
      const rows = byTodo.get(member.todo);
      if (rows) rows.push(member); else byTodo.set(member.todo,[member]);
    }
  }
  const open = todos.filter((row: ResourceRow) => !!row.id && belongs(row,group,manager) && !row.isDone())
    .map((row: ResourceRow) => new GroupTodo(row,byID)).sort((a: GroupTodo,b: GroupTodo) => {
      if ((a.rank <= 1 || b.rank <= 1) && a.rank !== b.rank) return a.rank-b.rank;
      return (a.due === b.due ? 0 : a.due-b.due) || a.rank-b.rank || b.created-a.created || a.row.id.localeCompare(b.row.id);
    });
  const groups: AgentTaskSection[] = [], placed = new Set<Member>();
  for (const task of open) {
    const members: Member[] = [];
    const take = (member: Member | undefined): void => { if (member && !placed.has(member)) { placed.add(member); members.push(member); } };
    for (const agent of task.item.agents) if (!agent.releasedAt) take(byID.get(sessionID(agent.machineId,agent.sessionId)));
    (byTodo.get(task.row.id) || []).forEach(take);
    if (!members.length) continue;
    const section = new AgentTaskSection('todo:' + task.row.id);
    const card = ResourceRow.from(task.row.data(),'todo');
    card.group = group; card.manager = manager;
    section.title = card.title; section.todo = card; section.state = task.state;
    section.lines = members.sort(compareMember).map((member: Member) => new AgentTaskLine('session',member.row));
    groups.push(section);
  }
  return groups.concat(extras(live.filter((member: Member) => !placed.has(member)), !filtering && live.length > 0, !!groups.length, false));
}

export function directionAgentSections(directions: Record<string,Object>[], shown: ResourceRow[], known: ResourceRow[],
  conversations: ResourceRow[], group: string, manager: string, filtering: boolean, muted: (row: ResourceRow) => boolean): AgentTaskSection[] {
  const live = liveMembers(shown,muted), byID = new Map<string, Member>(), inventory = new Set<string>(), used = new Set<string>();
  for (const row of known) if (belongs(row,group,manager)) inventory.add(sessionID(row.machine,row.session || row.id));
  for (const member of live) if (belongs(member.row,group,manager)) byID.set(member.key,member);
  const result: AgentTaskSection[] = [];
  for (const direction of directions) {
    const id = Wire.text(direction,'id');
    if (!id) continue;
    const section = new AgentTaskSection('direction:' + id);
    section.direction = true;
    const conversation = conversations.find((row: ResourceRow) => (row.session || row.id) === id && belongs(row,group,manager));
    section.title = Wire.text(direction,'name') || (conversation && conversation.title !== 'Claude Code' ? conversation.title : '');
    if (Wire.bool(direction,'secretary')) section.titleKey = 'agent_task_secretary';
    else if (!section.title) section.titleKey = 'agent_task_new_chat';
    const counts = Wire.object(direction,'counts');
    section.needs = Wire.number(counts,'needs_you');
    section.running = Wire.bool(direction,'running',Wire.number(counts,'working') > 0);
    for (const task of Wire.rows(direction,'tasks')) {
      if (!Wire.text(task,'id')) continue;
      const lines: AgentTaskLine[] = [], visited = new Set<string>();
      const walk = (nodes: Record<string,Object>[], depth: number): void => {
        for (const node of nodes) {
          const machine = Wire.text(node,'machine_id'), sid = Wire.text(node,'session_id'), key = sessionID(machine,sid);
          if (!machine || !sid || visited.has(key)) continue;
          visited.add(key);
          const member = byID.get(key);
          if (!used.has(key) && (member || (!inventory.has(key) && !filtering))) {
            used.add(key);
            if (member) lines.push(new AgentTaskLine('session',member.row,depth));
            else {
              const row = ResourceRow.from(node,'session');
              row.group = group; row.manager = manager;
              lines.push(new AgentTaskLine('tree',row,depth,stateOf(node)));
            }
          }
          walk(Wire.rows(node,'children'),depth+1);
        }
      };
      walk(Wire.rows(task,'agents'),1);
      if (!lines.length && filtering) continue;
      const row = ResourceRow.from(task,'todo');
      row.group = group; row.manager = manager;
      const state = Wire.text(task,'state');
      section.lines.push(new AgentTaskLine('task',row,0,['needs_you','review','stuck','planned','working'].includes(state) ? state : 'working'));
      section.lines = section.lines.concat(lines);
    }
    if (section.lines.length) result.push(section);
  }
  return result.concat(extras(live.filter((member: Member) => !belongs(member.row,group,manager) || !used.has(member.key)), false, false, !!result.length));
}
