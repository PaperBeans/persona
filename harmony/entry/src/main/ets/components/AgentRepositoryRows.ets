import { AgentWorkspace } from '../models/AgentWorkspace';
import { ClientModel } from '../models/ClientModel';
import { PersonaProject, ProjectCatalog, ProjectWorktree } from '../models/ProjectCatalog';
import { ResourceRow } from '../models/Wire';
import { WorktreeChange } from '../models/WorktreeActions';
import { ProjectsPage } from './ProjectsPage';

@ComponentV2
export struct AgentRepositoryRows {
  @Require @Param model: ClientModel;
  @Require @Param workspace: AgentWorkspace;
  @Require @Param catalog: ProjectCatalog;
  @Require @Param rows: ResourceRow[];
  @BuilderParam rowActions:(row:ResourceRow,spaceName:string)=>void;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onRoute: (kind: string, key: string) => void = () => {};
  @Event onChanged: (tree: ProjectWorktree, change: WorktreeChange) => void = () => {};

  aboutToAppear(): void {
    if (!this.catalog.loading) this.catalog.refresh(this.model).catch(() => {});
    this.updateSearch();
  }

  @Monitor('workspace.search', 'workspace.quickFilter', 'workspace.machineFilter', 'workspace.currentHubOnly')
  updateSearch(): void {
    this.catalog.agentBrowser.setSearch(this.workspace.search,JSON.stringify([this.workspace.quickFilter,this.workspace.machineFilter,this.workspace.currentHubOnly]));
  }

  private admits(group: string, manager: string, machine: string): boolean {
    if (!this.workspace.includesHub(group)) return false;
    const selected = this.workspace.machineFilter;
    return !selected || selected === JSON.stringify([group,manager,machine]) || selected === machine ||
      selected === [group,manager,machine].join('|') || selected === [manager,machine].join('|') || selected === [group,machine].join('|');
  }

  @Computed get visible(): PersonaProject[] {
    if (!this.workspace.isBoundTo(this.model)) return [];
    return this.catalog.agentDirectory(this.model,this.rows,this.workspace.quickFilter === 'all',this.workspace.search,
      (group:string,manager:string,machine:string):boolean => this.admits(group,manager,machine));
  }

  // ArkUI keeps a virtual ListItemGroup's children when their scoped keys overlap across filters.
  // Replace that group when the user's search scope changes; the controls and saved folds live above it.
  @Computed get filterIdentity(): string {
    return JSON.stringify([this.workspace.search,this.workspace.quickFilter,this.workspace.machineFilter,this.workspace.currentHubOnly]);
  }

  @Builder repositoryList() {
    ProjectsPage({ model:this.model, catalog:this.catalog, page:'agent_repositories', agentRepositories:this.visible,
      agentFilterActive:this.workspace.quickFilter !== 'all' || !!this.workspace.search.trim(),
      agentActions:(row:ResourceRow,spaceName:string)=>{if(this.rows.some((r:ResourceRow)=>r.key()===row.key()))this.rowActions(row,spaceName);},
      isMuted:(row:ResourceRow):boolean => this.workspace.isMuted(row),
      onSession:(row:ResourceRow) => {
        if (this.rows.some((current:ResourceRow) => current.key() === row.key())) this.onOpen(row);
      },
      onProject:(key:string) => this.onRoute('project',key), onTree:(key:string) => this.onRoute('project_tree',key),
      onMachine:(tree:ProjectWorktree) => this.onRoute('machine_projects',JSON.stringify([tree.group,tree.manager,tree.machine])),
      onStart:(tree:ProjectWorktree) => this.onRoute('start',tree.key),
      onChanged:(tree:ProjectWorktree,change:WorktreeChange) => this.onChanged(tree,change),
      onRefresh:() => this.catalog.refresh(this.model) })
  }
  build() {
    ForEach([this.filterIdentity],(identity:string) => {
      this.repositoryList()
    },(identity:string):string => identity)
  }
}
