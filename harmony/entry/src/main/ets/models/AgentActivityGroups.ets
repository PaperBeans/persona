import type { AgentListInput, AgentListRow, CoreAgentListGroup, CoreAgentListProjection } from '../core/AgentListProjection';
import { agentRepositoryPlace } from './AgentRepositoryPlace';
import { ResourceRow, Wire } from './Wire';
import { MachineAgent } from './MachineDashboard';

const ENGINES: string[] = ['herdr','herdr-core','orca','tern','rex'];
const ENGINE_NAMES: string[] = ['herdr','Herdr App','Orca','Tern','Rex'];

class ActivityRow {
  row: AgentListRow;
  time: number;
  constructor(row: AgentListRow) {
    this.row = row;
    const date = Date.parse(Wire.text(row.raw,'updated_at'));
    this.time = Number.isFinite(date) ? date : Number.NEGATIVE_INFINITY;
  }
}

class Bucket {
  id: string;
  title: string;
  titleKey: string;
  rows: ActivityRow[] = [];
  order: number = Number.MAX_SAFE_INTEGER;
  machine: string = '';
  constructor(id: string, title: string, titleKey: string = '') {
    this.id = id; this.title = title; this.titleKey = titleKey;
  }
}

function compareId(a: string, b: string): number { return a < b ? -1 : a > b ? 1 : 0; }

function desktopName(raw: Record<string,Object>): string {
  const agent = Wire.text(raw,'agent').toLowerCase(), source = Wire.text(raw,'source_kind');
  if (agent === 'codex' || source === 'codex_app_server' || source === 'codex_history') return 'Codex';
  if (!agent || agent === 'claude') return 'Claude Code';
  if (agent === 'trae') return 'TRAE';
  if (['traex','traecli','trae-cli'].includes(agent)) return 'TRAE CLI';
  const hint = Wire.text(raw,'agent_label'), launcher = Wire.text(raw,'launcher');
  if (agent === 'paseo' && (!hint || hint.toLowerCase() === agent) && launcher) return launcher;
  return hint || Wire.text(raw,'agent');
}

function appFor(raw: Record<string,Object>): Bucket {
  if (Wire.text(raw,'agent_surface') === 'desktop') {
    const name = desktopName(raw);
    const key = name === 'Claude Code' ? 'agent_app_claude' : name === 'Codex' ? 'agent_app_codex' : 'agent_app_desktop';
    const app = new Bucket('app/desktop/'+name.toLowerCase(), name, key);
    app.order = name === 'Claude Code' ? 0 : name === 'Codex' ? 1 : 2;
    return app;
  }
  const engine = Wire.text(raw,'space_kind'), index = ENGINES.indexOf(engine);
  if (index < 0) return new Bucket('app/other','','other');
  const app = new Bucket('app/engine/'+engine,ENGINE_NAMES[index]);
  app.order = 3+index;
  return app;
}

function homeDirectory(path: string): boolean {
  const parts = path.split(/[\\/]/).filter((part: string) => !!part);
  if (!parts.length || path === '~') return true;
  if (parts.length === 1) return ['root','Users','home'].includes(parts[0]) || parts[0].endsWith(':');
  if (parts.length === 2 && (parts[0] === 'Users' || parts[0] === 'home')) return true;
  return parts.length <= 3 && parts[0].endsWith(':') && parts[1] === 'Users';
}

function projectFor(app: Bucket, row: AgentListRow): Bucket {
  const raw = row.raw, folder = Wire.text(raw,'cwd'), machine = [Wire.text(raw,'app_hub_group'),Wire.text(raw,'manager'),Wire.text(raw,'machine_id')].filter((value:string)=>!!value).join('|');
  const other = new Bucket(app.id+'/project/other#','','other');
  let key: string, name: string;
  if (Wire.text(raw,'space_kind') === 'codex_app') {
    key = Wire.text(raw,'space_id'); name = Wire.text(raw,'space_name').trim();
    if (!key || !name || key.startsWith('codex-chats:') || key === 'codex-app:chats') return other;
    if (key.startsWith('codex-project:') && /(?:^|[\\/])Documents[\\/]Codex[\\/]\d{4}-\d{2}-\d{2}[\\/][^\\/]+[\\/]*$/.test(key.slice(14))) return other;
  } else {
    key = agentRepositoryPlace(folder,Wire.text(raw,'worktree'),Wire.text(raw,'repo_root')).repo;
    if (homeDirectory(key)) key = folder;
    key = key.replace(/[\\/]+$/,'');
    if (homeDirectory(key)) return other;
    const parts = key.split(/[\\/]/);
    name = parts[parts.length-1];
  }
  const project = new Bucket(app.id+'/project/'+machine+'/'+key+'#',name);
  project.machine=machine;
  const index = Wire.number(raw,'space_index');
  if (Wire.text(raw,'space_kind') === 'codex_app' && index > 0) project.order = index;
  return project;
}

function collect(buckets: Map<string,Bucket>, next: Bucket, row: ActivityRow): void {
  const stored = buckets.get(next.id);
  if (!stored) { next.rows.push(row); buckets.set(next.id,next); return; }
  stored.rows.push(row);
  if (stored.order === Number.MAX_SAFE_INTEGER) stored.order = next.order;
}

function section(bucket: Bucket, type: string, input: AgentListInput, project: boolean = false): CoreAgentListGroup {
  const rows = bucket.rows.map((item:ActivityRow)=>item.row.id);
  const folded = type !== 'recent' && Wire.strings(input.coreSettings,'folded').includes(bucket.id);
  const cap = project ? 8 : 20;
  const limited = !input.query && input.quick === 'all' && rows.length > cap+5;
  const expanded = Wire.strings(input.coreSettings,'expanded_idle').includes(bucket.id);
  const visible = folded ? [] : limited && !expanded ? rows.slice(0,cap) : rows;
  return {id:bucket.id,title:bucket.title,titleKey:bucket.titleKey,presentation:type,rows,visible_rows:visible,folded,capped:limited};
}

/** The same unpinned, filtered native rows drive activity, app, task and repository views. */
export function phoneAgentGroups(input: AgentListInput, projection: CoreAgentListProjection): CoreAgentListGroup[] {
  if (!['repo','todo','direction','app','recent'].includes(input.grouping)) return projection.groups;
  if (!projection.unpinned) throw new Error('missing_core_unpinned_inventory');
  const groups: CoreAgentListGroup[] = [];
  if (!input.query && (input.quick === 'all' || input.quick === 'needsYou')) {
    const needs = projection.groups.find((group:CoreAgentListGroup)=>group.id === 'state/needs');
    if (needs) groups.push(needs);
  }
  const pinned = projection.groups.find((group:CoreAgentListGroup)=>group.id === 'pinned');
  if (pinned) groups.push(pinned);
  if (input.grouping !== 'recent' && input.grouping !== 'app') return groups;
  const included = new Set<string>(projection.unpinned);
  const rows = input.rows.filter((row:AgentListRow)=>included.has(row.id)).map((row:AgentListRow)=>new ActivityRow(row));
  rows.sort((a:ActivityRow,b:ActivityRow)=>a.time !== b.time ? b.time-a.time : compareId(a.row.id,b.row.id));
  if (input.grouping === 'recent') {
    if (rows.length) {
      const recent = new Bucket('recent','','agent_group_recent');
      recent.rows=rows; groups.push(section(recent,'recent',input));
    }
    return groups;
  }
  const apps = new Map<string,Bucket>();
  for (const row of rows) collect(apps,appFor(row.row.raw),row);
  const ordered = Array.from(apps.values()).sort((a:Bucket,b:Bucket)=>a.order-b.order || compareId(a.id,b.id));
  for (const app of ordered) {
    const header = section(app,'app',input); groups.push(header);
    const counts:Record<string,Object> = {};
    for (const row of app.rows) {
      if (row.row.raw['nested_subtask'] === true) continue;
      const agent = new MachineAgent(row.row.source as ResourceRow);
      let status = agent.counted;
      if (status === 'offline') continue;
      if (agent.state === 'working' && status === 'idle') status='backgroundWaiting';
      if (status === 'unknown') status = 'idle';
      counts[status]=Wire.number(counts,status)+1;
    }
    header.state_counts=counts;
    if (header.folded || app.id === 'app/other') continue;
    const projects = new Map<string,Bucket>();
    for (const row of app.rows) collect(projects,projectFor(app,row.row),row);
    const children = Array.from(projects.values()).sort((a:Bucket,b:Bucket)=>Number(!a.title)-Number(!b.title) || a.order-b.order);
    if (children.length === 1 && !children[0].title) continue;
    header.presentation = 'app-parent'; header.visible_rows = []; header.capped = false;
    const machines = new Set(children.map((child:Bucket)=>child.machine).filter((name:string)=>!!name));
    for (const child of children) {
      const item=section(child,'app-project',input,true); item.parent=app.id;
      item.firstProject=child===children[0]; item.lastProject=child===children[children.length-1];
      if (child.machine && machines.size>1) item.subtitle=input.machineName(child.rows[0].row);
      groups.push(item);
    }
  }
  return groups;
}
