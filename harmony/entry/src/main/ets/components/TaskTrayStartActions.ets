import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { AssistantFeedModel } from '../models/AssistantFeed';
import { ClientModel } from '../models/ClientModel';
import { TaskStartRecovery } from '../models/TaskStartRecovery';
import { ResourceRow, Wire } from '../models/Wire';
import { TaskStartupQuestionCard } from './TaskStartupQuestionCard';

@ComponentV2
export struct TaskTrayStartActions {
  @Require @Param model:ClientModel;
  @Require @Param feed:AssistantFeedModel;
  @Require @Param row:ResourceRow;
  @Event visible:()=>boolean=()=>false;
  @Local recovery:TaskStartRecovery=new TaskStartRecovery();
  private mounted:boolean=false;
  private bound:string='';
  aboutToAppear():void { this.mounted=true; this.openRow(); }
  aboutToDisappear():void { this.mounted=false; this.bound=''; this.recovery.close(); }
  @Monitor('row','feed.todos','feed.plans') openRow():void {
    if (!this.mounted) return;
    const key=this.row.key(), todo=this.feed.todo(this.row.id);
    const current=JSON.stringify([key,this.model.scope(),this.model.connectionGeneration(),Wire.text(todo,'start_error'),Wire.text(todo,'done_at'),Wire.text(todo,'reopened_at')]);
    if (this.bound===current) { this.recovery.observeNextAttempt(); return; }
    this.bound=current;
    this.recovery.open(this.model,this.feed,this.row,()=>this.mounted && this.visible() && this.row.key()===key);
  }
  private remove():void {
    if (!this.recovery.canAct('remove')) return;
    const id=this.recovery.id;
    const apply=():void=>{if (this.recovery.id===id) this.recovery.act('remove');};
    if (!this.recovery.confirmRemoval) {apply();return;}
    this.getUIContext().showAlertDialog({title:tr('task_recovery_remove_title'),message:this.recovery.title,
      primaryButton:{value:tr('cancel'),action:()=>{}},secondaryButton:{value:tr('remove'),action:apply}});
  }
  @Builder action(key:string,label:string) {
    Button(label,{type:ButtonType.Capsule}).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
      .constraintSize({minHeight:30}).padding({left:10,right:10}).backgroundColor(key==='remove' ? Colors.control : Colors.link)
      .fontColor(key==='remove' ? Colors.ink : Color.White).enabled(this.recovery.canAct(key==='remove'?'remove':'retry'))
      .onClick(()=>{if (key==='remove') this.remove();else this.recovery.act('retry',key==='retry-launcher'?this.recovery.alternative:'');})
      .id('action-tray.'+key)
  }
  build() {
    if (this.recovery.id && this.recovery.state!=='starting' && this.recovery.state!=='gone') Column({space:6}) {
      if (this.recovery.question) TaskStartupQuestionCard({recovery:this.recovery,question:this.recovery.question!})
      if (this.recovery.alternative) this.action('retry-launcher',tr('task_recovery_instead').replace('{launcher}',this.recovery.alternative))
      Row({space:6}) { this.action('retry',tr('retry')); this.action('remove',tr('remove')) }
      if (this.recovery.statusMessage() && !this.recovery.question) Text(tr(this.recovery.statusMessage())).fontSize(13).fontColor(Colors.secondary)
    }.width('100%').padding({left:36,right:12,bottom:6}).alignItems(HorizontalAlign.Start)
  }
}
