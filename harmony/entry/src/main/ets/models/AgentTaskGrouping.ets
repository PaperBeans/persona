import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';
import type { AgentWorkspace } from './AgentWorkspace';
import { AgentTaskSection, directionAgentSections, todoAgentSections } from './AgentTaskSections';
import { HttpFailure } from '../platform/HubTransport';
import { ResourceRow, Wire } from './Wire';

// The menu follows the two reads the Hub actually supports. Only a 404 withdraws a capability.
@ObservedV2
export class AgentTaskGrouping {
  @Trace supportsTodos: boolean = true;
  @Trace supportsDirections: boolean = false;
  @Trace todos: ResourceRow[] = [];
  @Trace directions: Record<string, Object>[] = [];
  private initialRows: ResourceRow[] | undefined;
  private client: ClientModel | undefined;
  private owner: string = '';
  private revision: number = 0;
  private generation: number = -1;
  private pending: Promise<void> | undefined;

  clear(): void {
    this.revision++;
    this.pending = undefined;
    this.initialRows = undefined;
    this.client = undefined;
    this.owner = '';
    this.generation = -1;
    this.supportsTodos = true;
    this.supportsDirections = false;
    this.todos = [];
    this.directions = [];
  }

  async refresh(client: ClientModel): Promise<void> {
    const owner = client.scope(), generation = client.connectionGeneration();
    if (this.client !== client || this.owner !== owner) {
      this.clear();
      this.owner = owner;
      this.client = client;
    }
    if (this.generation !== generation) {
      this.generation = generation;
      this.pending = undefined;
      this.revision++;
    }
    if (!client.connected) return;
    if (this.pending) return this.pending;
    const repo = client.repo(), revision = this.revision;
    const current = (): boolean => this.revision === revision && this.client === client && client.connected && client.owns(generation, owner);
    const load = Promise.all([this.readTodos(repo, client, current), this.readDirections(repo, current)]).then(() => {});
    this.pending = load;
    try { await load; } finally { if (this.revision === revision) this.pending = undefined; }
  }

  currentTodos(client: ClientModel): ResourceRow[] {
    if (this.client !== client || this.owner !== client.scope()) return [];
    return this.initialRows && client.todos !== this.initialRows ? client.todos : this.todos;
  }

  private async readTodos(repo: HubRepository, client: ClientModel, current: () => boolean): Promise<void> {
    const initial = client.todos;
    try {
      const rows = await repo.todos();
      if (!current()) return;
      this.initialRows = initial;
      const group = client.profile.id, manager = client.profile.manager;
      this.todos = rows.filter((row: ResourceRow) => (!row.group || row.group === group) && (!row.manager || row.manager === manager))
        .map((row: ResourceRow) => { row.group = group; row.manager = manager; return row; });
      this.supportsTodos = true;
    } catch (error) {
      if (current() && error instanceof HttpFailure && error.status === 404) { this.supportsTodos = false; this.todos = []; }
    }
  }

  private async readDirections(repo: HubRepository, current: () => boolean): Promise<void> {
    if (!repo.isDefaultManager()) { this.supportsDirections = false; this.directions = []; return; }
    try {
      const tree = await repo.call('/api/v1/assistant/directions/tree');
      if (!current()) return;
      this.directions = Wire.rows(tree, 'directions');
      this.supportsDirections = true;
    } catch (error) {
      if (current() && error instanceof HttpFailure && error.status === 404) { this.supportsDirections = false; this.directions = []; }
    }
  }
}


// The parent List must emit one native child per section so scrolling and virtualization see every group.
export function agentTaskSectionsFor(model: ClientModel, workspace: AgentWorkspace, source: AgentTaskGrouping, mode: string): AgentTaskSection[] {
  const filtering = !!workspace.search.trim() || workspace.quickFilter !== 'all';
  const group = model.profile.id, manager = model.profile.manager;
  const muted = (row: ResourceRow): boolean => workspace.isMuted(row);
  if (mode === 'direction' && source.supportsDirections) {
    return directionAgentSections(source.directions, workspace.unpinnedRows, model.directorySessions.concat(model.sessions),
      model.conversations, group, manager, filtering, muted);
  }
  return todoAgentSections(source.currentTodos(model), workspace.unpinnedRows, group, manager, filtering, muted);
}
