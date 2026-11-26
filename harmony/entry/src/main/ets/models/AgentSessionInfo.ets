import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

export function sessionSlot(data: Record<string,Object>): string {
  const name = Wire.text(data,'slot_name');
  const index = data['slot_index'];
  if (typeof index === 'number' && Number.isInteger(index) && (!name || name === String(index))) return '#' + String(index);
  return name;
}

export function sessionInfoPath(path: string, home: string): string {
  if (home && (path === home || path.startsWith(home+'/'))) return '~'+path.slice(home.length);
  return path.replace(/^\/(?:Users|home|data00\/home)\/[^/]+(?=\/|$)/,'~');
}

export function agentTitleState(row: ResourceRow): string {
  const state = row.state;
  if (state === 'blocked') return 'needs_you';
  if (state === 'done') return 'unread';
  return ['needs_you','working','unread','idle','offline'].includes(state) ? state : 'unknown';
}

const stateKeys: Record<string,string> = {'needs_you':'phone_todo_section_needs','working':'phone_todo_section_working',
  'unread':'phone_sidebar_unread','idle':'agent_group_idle','offline':'phone_session_not_running','unknown':'unknown'};

export function agentInfoState(row: ResourceRow, word: (key: string) => string): string {
  const state = agentTitleState(row);
  if (state === 'working' && Wire.bool(row.data(),'background_working')) return word('phone_session_background_work');
  return word(stateKeys[state]);
}

export class AgentHeaderWords {
  state: string = '';
  status: string = '';
  full: string = '';
  short: string = '';
  location: string = '';
}

export function agentHeaderWords(row: ResourceRow, machine: string, space: string, hasTabStrip: boolean,
  word: (key: string) => string): AgentHeaderWords {
  const words = new AgentHeaderWords(), data = row.data();
  words.state = agentTitleState(row); words.status = agentInfoState(row,word);
  const slot = sessionSlot(data);
  const place = [space,hasTabStrip ? '' : slot].filter((part:string) => !!part).join(' › ');
  const active = ['needs_you','working','unread'].includes(words.state) ? words.status : '';
  words.full = [active,machine,place].filter((part:string) => !!part).join(' · ') || words.status;
  words.short = [active,place || machine].filter((part:string) => !!part).join(' · ') || words.status;
  words.location = [machine,space,slot].filter((part:string) => !!part).join(' › ');
  return words;
}

@ObservedV2
export class AgentSessionInfo {
  @Trace shown: boolean = false;
  @Trace loading: boolean = false;
  @Trace error: string = '';
  @Trace detail: Record<string,Object> = {};
  private serial: number = 0;
  private owner: ClientModel | undefined;
  private identity: string = '';
  private generation: number = -1;

  owns(model: ClientModel): boolean {
    return this.shown && this.owner === model && model.target() === this.identity && model.connectionGeneration() === this.generation;
  }
  close(): void { this.serial++; this.shown = false; this.loading = false; this.error = ''; this.detail = {}; this.owner = undefined; }
  async open(model: ClientModel): Promise<void> {
    this.close();
    const row = model.selected, id = row.session || row.id, scope = model.scope();
    if (row.kind !== 'session' || !id || !row.machine || row.group && row.group !== model.profile.id || row.manager && row.manager !== model.profile.manager) return;
    this.owner = model; this.identity = model.target(); this.generation = model.connectionGeneration(); this.shown = true;
    if (!model.connected) { this.error = 'offline'; return; }
    const ticket = this.serial, generation = this.generation;
    const current = (): boolean => ticket === this.serial && this.owns(model) && model.owns(generation,scope);
    this.loading = true;
    try {
      const reply = await model.repo().call('/api/v1/sessions/' + encodeURIComponent(row.machine) + '/' + encodeURIComponent(id) + '/info',
        'GET',{},'',current);
      if (!current()) return;
      if (Wire.text(reply,'session_id') !== id || Wire.text(reply,'machine_id',row.machine) !== row.machine) { this.error = 'request_updated'; return; }
      this.detail = reply;
    } catch (failure) { if (current()) this.error = (failure as Error).message || 'failed'; }
    finally { if (ticket === this.serial) this.loading = false; }
  }
}
