import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { TaskStartRecovery } from '../models/TaskStartRecovery';
import { TodoWorkspace } from '../models/TodoWorkspace';
import { TaskStartupQuestionCard } from './TaskStartupQuestionCard';

@ComponentV2
export struct PhoneTodoStartSection {
  @Require @Param workspace:TodoWorkspace;
  @Local recovery:TaskStartRecovery=new TaskStartRecovery();
  @Local details:boolean=false;
  private onPage:boolean=false;
  private binding:string='';
  aboutToAppear():void { this.onPage=true; this.openSelected(); }
  aboutToDisappear():void { this.onPage=false; this.binding=''; this.recovery.close(); }
  @Monitor('workspace.selected') openSelected():void {
    const todo=this.workspace.selected;
    const key=JSON.stringify([todo.manager,todo.id,todo.origin,todo.planId,todo.doneAt,todo.reopenedAt,todo.startError]);
    if (!this.onPage || key===this.binding) return;
    this.binding=key; this.details=false;
    this.workspace.openTaskStart(this.recovery,()=>this.onPage && this.binding===key);
  }
  private summary():string {
    if (this.recovery.reasonCode==='worktree_path_taken') return tr('task_recovery_workspace_taken');
    if (this.recovery.reasonCode==='launcher_missing' && this.recovery.reasonLauncher)
      return tr('task_recovery_launcher_missing').replace('{launcher}',this.recovery.reasonLauncher);
    return this.recovery.failure || this.workspace.selected.startError;
  }
  build() {
    if ((this.recovery.failure || this.workspace.selected.startError) && !['gone','starting'].includes(this.recovery.state)) {
      Column({space:10}) {
        Row({space:8}) {
          SymbolGlyph($r('sys.symbol.exclamationmark_triangle_fill')).fontSize('14vp').fontColor([Colors.danger])
          Column({space:3}) {
            Text(tr('task_recovery_heading')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Text(this.summary()).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary)
            if (this.recovery.failure && this.summary()!==this.recovery.failure) {
              Button(tr(this.details?'collapse':'details')).backgroundColor(Color.Transparent).fontColor(Colors.link).onClick(()=>this.details=!this.details)
              if (this.details) Text(this.recovery.failure).fontSize(14).fontColor(Colors.secondary)
            }
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        }.width('100%').alignItems(VerticalAlign.Top).padding(12).backgroundColor(Colors.surface).borderRadius(12).id('todo.start.failure')
        if (this.recovery.question) TaskStartupQuestionCard({recovery:this.recovery,question:this.recovery.question!})
        if (this.recovery.id && this.recovery.state==='ready') Row({space:6}) {
          if (this.recovery.alternative) Button(tr('task_recovery_instead').replace('{launcher}',this.recovery.alternative))
            .enabled(this.recovery.canAct('retry')).onClick(()=>this.recovery.act('retry',this.recovery.alternative)).id('todo.retry-launcher')
          Button(tr('retry')).enabled(this.recovery.canAct('retry')).onClick(()=>this.recovery.act('retry')).id('todo.retry')
        }.width('100%')
        if (this.recovery.statusMessage() && !this.recovery.question) Text(tr(this.recovery.statusMessage())).fontSize(13).fontColor(Colors.secondary)
        if (this.recovery.state==='stale' || this.recovery.state==='unknown') Button(tr('refresh'))
          .enabled(!this.recovery.busy).onClick(()=>this.recovery.refresh()).id('todo.start.refresh')
      }.width('100%').alignItems(HorizontalAlign.Start)
    }
  }
}
