import { supervisorHoldsCard } from './InteractionDecision';
import type { CoreAgentListProjection } from '../core/AgentListProjection';
import type { AgentDecisionSnapshot } from './AgentDecisionQueue';
import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

export const projectQuickFilters: string[] = ['all','needsYou','unread','working','backgroundWaiting','feishu','terminal'];
export const projectQuickLabels: Record<string,string> = { 'all':'all', 'needsYou':'needs_you', 'unread':'agent_group_unread',
  'working':'working', 'backgroundWaiting':'agent_group_background', 'feishu':'agent_source_lark_group', 'terminal':'terminal' };

export class ProjectSearchResult {
  counts: Record<string,Object> = {};
  rows: Set<string> = new Set();
}

function identity(group:string, manager:string, machine:string, session:string):string {
  return JSON.stringify([group,manager,machine,session]);
}

/** The repository outline admits only real rows. A pending card outside the text search still contributes one
 * Needs You entry per waiting session, or one for a sessionless card, as in AgentsNeedsYou.rows on iPhone. */
export function projectSearchResult(root:ClientModel, sessions:ResourceRow[], projection:CoreAgentListProjection,
  pending:AgentDecisionSnapshot[], machineFilter:string, quick:string, coreID:(row:ResourceRow) => string):ProjectSearchResult {
  const result = new ProjectSearchResult();
  const counts = projection.filter_counts;
  if (counts) for (const key of Object.keys(counts)) result.counts[key] = counts[key];
  const matches = new Set(projection.queryMatches || []);
  const selected = new Set(projection.filtered || []);
  const waiting = new Set<string>(), attention = new Set<string>(), held = new Set<string>();
  const bySession = new Map<string,ResourceRow>(), hidden = new Set<string>();
  for (const row of sessions) {
    const data = row.data(), key = identity(row.group,row.manager,row.machine,row.session || row.id);
    if (Wire.bool(data,'assistant') || Wire.bool(data,'hidden_by_directory') || Wire.bool(data,'archived')) {
      hidden.add(key); continue;
    }
    bySession.set(key,row);
    const id = coreID(row);
    if (selected.has(id)) result.rows.add(row.key());
    if (matches.has(id) && (row.state === 'needs_you' || row.state === 'blocked')) waiting.add(key);
  }
  const visibleOwners = new Set(root.directoryOwners());
  for (const snapshot of pending) {
    const owner = snapshot.owner;
    if (!visibleOwners.has(owner) || !owner.owns(snapshot.generation,snapshot.scope)) continue;
    for (const outer of snapshot.records) {
      const inner = Wire.object(outer,'interaction');
      const field = (key:string):string => Wire.text(outer,key) || Wire.text(inner,key);
      const manager = field('manager') || field('manager_id') || owner.profile.manager, group = owner.profile.id;
      const machine = field('machine_id'), session = field('session_id'), id = field('id') || field('interaction_id');
      if (!id || manager !== owner.profile.manager || (machineFilter && machineFilter !== JSON.stringify([group,manager,machine]))) continue;
      const status = field('status');
      if (status && status !== 'open' && status !== 'pending') continue;
      const supervisorHeld = supervisorHoldsCard(field('kind'),Wire.bool(Object.keys(inner).length ? inner : outer,'delegating'));
      if (!session) {
        if (!supervisorHeld) attention.add(JSON.stringify([group,manager,'card',id,'']));
        continue;
      }
      const key = identity(group,manager,machine,session);
      if (!machine || hidden.has(key)) continue;
      const home = identity(group,manager,machine,field('folded_under'));
      const target = waiting.has(home) ? home : key;
      if (supervisorHeld) { held.add(target); continue; }
      attention.add(target);
      const row = bySession.get(target);
      if (quick === 'needsYou' && row) result.rows.add(row.key());
    }
  }
  for (const key of waiting) {
    if (!held.has(key)) attention.add(key);
    else if (quick === 'needsYou' && !attention.has(key)) {
      const row = bySession.get(key);
      if (row) result.rows.delete(row.key());
    }
  }
  result.counts['needsYou'] = attention.size;
  return result;
}
