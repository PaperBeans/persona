import { ResourceRow, Wire } from './Wire';

/** Only routing and visibility take part in the badge, so a summary need never retain the user's answer or prompt. */
export class AgentAttentionCard {
  readonly id: string;
  readonly machine: string;
  readonly session: string;
  readonly parent: string;
  readonly held: boolean;
  readonly hidden: boolean;

  constructor(id: string, machine: string, session: string, parent: string, held: boolean, hidden: boolean) {
    this.id = id;
    this.machine = machine;
    this.session = session;
    this.parent = parent;
    this.held = held;
    this.hidden = hidden;
  }
}

export function agentAttentionCards(input: Record<string, Object>[], manager: string): AgentAttentionCard[] {
  const result: AgentAttentionCard[] = [];
  const seen = new Set<string>();
  for (const envelope of input) {
    const nested = Wire.object(envelope, 'interaction');
    const card = Object.keys(nested).length ? nested : envelope;
    const owner = Wire.text(envelope, 'manager') || Wire.text(envelope, 'manager_id') ||
      Wire.text(card, 'manager') || Wire.text(card, 'manager_id') || manager;
    const id = Wire.text(card, 'id');
    const machine = Wire.text(card, 'machine_id') || Wire.text(envelope, 'machine_id');
    const session = Wire.text(card, 'session_id') || Wire.text(envelope, 'session_id');
    const key = JSON.stringify([machine, session, id]);
    const status = Wire.text(envelope, 'status') || Wire.text(card, 'status');
    if (owner !== manager || !id || !machine || seen.has(key) ||
      ['resolved', 'closed', 'expired', 'dismissed', 'cancelled'].includes(status)) continue;

    seen.add(key);
    const parent = Wire.text(card, 'folded_under') || Wire.text(envelope, 'folded_under');
    // Neither a supervised card nor a question the assistant is answering should ring the Agents badge.
    const held = Wire.bool(card, 'delegating') || Wire.bool(envelope, 'delegating');
    const hidden = Wire.bool(card, 'hidden_by_directory') || Wire.bool(envelope, 'hidden_by_directory') ||
      Wire.bool(card, 'muted') || Wire.bool(envelope, 'muted');
    result.push(new AgentAttentionCard(id, machine, session, parent, held, hidden));
  }
  return result;
}

function sessionKey(machine: string, session: string): string {
  return JSON.stringify([machine, session]);
}

/** Match the shared Needs You corpus for a single connected Hub and assistant, independent of active list filters. */
export function waitingAgentBadge(rows: ResourceRow[], todos: ResourceRow[], cards: AgentAttentionCard[],
  group: string, manager: string, muted: (row: ResourceRow, space: string) => boolean, authoritative: boolean): number {
  const quietTodos = new Set<string>();
  const quietAgents = new Set<string>();
  for (const todo of todos) {
    if (todo.group && todo.group !== group || todo.manager && todo.manager !== manager) continue;
    const data = todo.data();
    if (!Wire.bool(data, 'quiet')) continue;
    quietTodos.add(todo.id);
    for (const agent of Wire.rows(data, 'agents')) {
      quietAgents.add(sessionKey(Wire.text(agent, 'machine_id'), Wire.text(agent, 'session_id')));
    }
  }

  const parents = new Map<string, string>();
  const waiting = new Set<string>();
  const excluded = new Set<string>();
  for (const row of rows) {
    if (row.kind !== 'session' || row.group && row.group !== group || row.manager && row.manager !== manager) continue;
    const session = row.session || row.id, key = sessionKey(row.machine, session);
    const fields = row.badgeFields();
    parents.set(key, fields.parent && fields.parent !== session ? sessionKey(row.machine, fields.parent) : '');
    const needsUser = ['needs_you', 'blocked', 'waiting_for_user'].includes(row.state) ||
      fields.childrenWaiting || !authoritative && fields.pending;
    if (needsUser) waiting.add(key);
    if (fields.hidden || quietTodos.has(fields.quietTodo) || quietAgents.has(key) || muted(row, fields.space)) {
      excluded.add(key);
    }
  }
  parents.forEach((parent: string, key: string): void => {
    if (parent && parents.has(parent)) waiting.delete(key);
  });

  const active = new Set<string>();
  const held = new Set<string>();
  for (const card of cards) {
    const own = sessionKey(card.machine, card.session);
    if (card.hidden || excluded.has(own) || quietAgents.has(own)) continue;
    const parent = sessionKey(card.machine, card.parent);
    let key = own;
    if (!card.session) key = JSON.stringify([card.machine, '', card.id]);
    else if (card.parent && waiting.has(parent)) key = parent;
    if (excluded.has(key)) continue;
    if (card.held) held.add(key);
    else active.add(key);
  }
  for (const key of waiting) {
    if (!excluded.has(key) && !held.has(key)) active.add(key);
  }
  return active.size;
}
