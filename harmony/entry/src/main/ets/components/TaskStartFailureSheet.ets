import { Colors } from '../design/Colors';
import { text } from '@kit.ArkGraphics2D';
import { ReadinessStyle } from '../design/ReadinessStyle';
import { PhoneOutlineIcon } from '../design/PhoneOutlineIcon';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { TaskStartupQuestionCard } from './TaskStartupQuestionCard';
import { TaskStartRecovery } from '../models/TaskStartRecovery';

@ComponentV2
export struct TaskStartFailureSheet {
  @Require @Param recovery:TaskStartRecovery;
  @Event onClose:()=>void=()=>{};
  @Event onEdit:()=>void=()=>{};
  @Local details:boolean=false;
  @Local private headingWidth:number=0;
  @Computed get fullHeading():string { return this.recovery.title || tr('task'); }
  @Computed get sheetSecondary():ResourceColor {
    return Colors.systemControls && Colors.styleID==='glass' ? $r('app.color.task_failure_secondary') : Colors.secondary;
  }
  @Computed get displayedHeading():string {
    const title=this.fullHeading;
    if (this.headingWidth<=0 || /[\n\r\u2028\u2029]/.test(title)) return title;
    const ui=this.getUIContext();
    const face:text.TextStyle={fontSize:ui.vp2px(Number.parseFloat(ReadinessStyle.font(Tokens.body))),fontWeight:text.FontWeight.W600};
    const layout=new text.ParagraphBuilder({textStyle:face,breakStrategy:text.BreakStrategy.BALANCED,
      wordBreak:text.WordBreak.BREAK_WORD,maxLines:3},text.FontCollection.getGlobalInstance());
    layout.pushStyle(face);
    layout.addText(title);
    layout.popStyle();
    const paragraph=layout.build();
    paragraph.layoutSync(ui.vp2px(this.headingWidth));
    if (paragraph.getLineCount()<=2) return title;
    // A truncated headline keeps its natural first line. Let the second fill
    // the available width up to the ellipsis, including a partial final word.
    const next=paragraph.getLineMetrics(1);
    if (!next || next.startIndex<=0 || next.startIndex>=title.length) return title;
    return title.slice(0,next.startIndex).trimEnd()+'\n'+title.slice(next.startIndex).trimStart();
  }
  private remove():void {
    if (!this.recovery.canAct('remove')) return;
    const id=this.recovery.id;
    const remove=async ():Promise<void>=>{
      if (this.recovery.id===id && await this.recovery.act('remove') && this.recovery.id===id) this.onClose();
    };
    if (!this.recovery.confirmRemoval) { remove(); return; }
    this.getUIContext().showAlertDialog({title:tr('task_recovery_remove_title'), message:this.recovery.title,
      primaryButton:{value:tr('cancel'),action:()=>{}}, secondaryButton:{value:tr('remove'),action:()=>{remove();}}});
  }
  private summary():string {
    if (this.recovery.reasonCode==='worktree_path_taken') return tr('task_recovery_workspace_taken');
    if (this.recovery.reasonCode==='launcher_missing' && this.recovery.reasonLauncher)
      return tr('task_recovery_launcher_missing').replace('{launcher}',this.recovery.reasonLauncher);
    return this.recovery.failure;
  }
  @Builder options() {
    Menu() {
      MenuItem({content:tr('edit')}).onClick(()=>this.onEdit())
      if (this.recovery.canAct('remove')) MenuItem({content:tr('remove')}).onClick(()=>this.remove())
    }
  }
  @Builder action(key:string, label:string, prominent:boolean) {
    Button({type:ButtonType.Capsule}) {
      Text(label).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontWeight(FontWeight.Medium)
        .fontColor(prominent ? Color.White : Colors.link).maxLines(1)
    }.padding({left:12,right:12}).constraintSize({minWidth:ReadinessStyle.metric(50,62),minHeight:ReadinessStyle.metric(30,37)})
      .backgroundColor(prominent ? Colors.action : Colors.readinessControl)
      .border({width:prominent ? 0 : Tokens.divider,color:Colors.readinessEdge})
      .enabled(this.recovery.canAct(key==='remove'?'remove':'retry')).id('task.'+key)
      .onClick(()=>{if(key==='remove') this.remove(); else this.recovery.act('retry',key==='retry-launcher'?this.recovery.alternative:'');})
  }
  build() {
    Column() {
      Row().width(36).height(5).borderRadius(3).backgroundColor(Colors.tertiary).margin({top:5,bottom:6}).accessibilityLevel('no')
      Row({space:Tokens.gap}) {
        Button({type:ButtonType.Normal}) {
          Stack() {
            // The physical-pixel path keeps its original viewport. Scale the finished
            // mark about its center so its stroke follows the rest of the header.
            Stack() { PhoneOutlineIcon({name:'navigationClose',color:Colors.link,iconScale:12/17}) }
              .width(12).height(12)
              .scale({x:ReadinessStyle.metric(12,17)/12,y:ReadinessStyle.metric(12,17)/12})
              .hitTestBehavior(HitTestMode.None).accessibilityLevel('no')
          }
            .width(ReadinessStyle.metric(46,56)).height(ReadinessStyle.metric(46,56))
            .borderRadius(Tokens.capsule*2).backgroundColor(Colors.readinessControl)
        }.width(ReadinessStyle.metric(56,66)).height(ReadinessStyle.metric(46,56)).padding(0).backgroundColor(Color.Transparent)
          .onClick(()=>this.onClose()).accessibilityText(tr('close')).id('task.failure.close')
        SymbolGlyph($r('sys.symbol.exclamationmark_triangle_fill')).fontSize(ReadinessStyle.font(ReadinessStyle.label))
          .width(ReadinessStyle.metric(16,21)).fontColor([Colors.danger])
        Text(this.displayedHeading).fontSize(ReadinessStyle.font(Tokens.body))
          .lineHeight(ReadinessStyle.line(21.2,28.3)).lineBreakStrategy(LineBreakStrategy.GREEDY)
          .wordBreak(this.displayedHeading===this.fullHeading ? WordBreak.BREAK_WORD : WordBreak.BREAK_ALL)
          .fontWeight(600).fontColor(Colors.ink).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
          .layoutWeight(1).accessibilityText(this.fullHeading)
          .onAreaChange((_old:Area,area:Area) => {
            const width=Number(area.width);
            if (width>0 && Math.abs(width-this.headingWidth)>0.15) this.headingWidth=width;
          }).id('task.failure.title')
        Blank().width(Tokens.gap)
        Button({type:ButtonType.Normal}) {
          Stack() {
            Row({space:ReadinessStyle.metric(2.05,3.4)}) {
              ForEach([0,1,2],(_dot:number)=>{
                Circle().width(ReadinessStyle.metric(3.3,4.3)).height(ReadinessStyle.metric(3.3,4.3)).fill(Colors.link)
              },(dot:number)=>String(dot))
            }.accessibilityLevel('no')
          }
            .width(ReadinessStyle.metric(46,56)).height(ReadinessStyle.metric(46,56))
            .borderRadius(Tokens.capsule*2).backgroundColor(Colors.readinessControl)
        }.width(ReadinessStyle.metric(56,66)).height(ReadinessStyle.metric(46,56)).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr('more')).bindMenu(this.options()).id('task.failure.more')
      }.width('100%').height(ReadinessStyle.metric(59,69.7)).padding({left:Tokens.page,right:Tokens.page,bottom:10})
      Divider().strokeWidth(0.5).color(Colors.divider)
      Scroll() {
        Column({space:12}) {
          if (this.recovery.state==='starting') {
            Text(tr('task_recovery_starting')).fontSize(ReadinessStyle.font(Tokens.body)).fontWeight(FontWeight.Medium).fontColor(Colors.ink).id('task.failure.starting')
            Text(tr('task_recovery_starting_detail')).fontSize(ReadinessStyle.font(ReadinessStyle.body)).fontColor(this.sheetSecondary)
          } else if (this.recovery.state==='gone') {
            Text(tr('task_recovery_gone')).fontSize(ReadinessStyle.font(Tokens.body)).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          } else {
            Row({space:8}) {
              SymbolGlyph($r('sys.symbol.exclamationmark_triangle_fill')).fontSize(ReadinessStyle.font(ReadinessStyle.label))
                .width(ReadinessStyle.metric(15.3,22.3)).fontColor([Colors.danger])
              Column({space:Tokens.fine}) {
                Text(tr('task_recovery_heading')).fontSize(ReadinessStyle.font(ReadinessStyle.label)).lineHeight(ReadinessStyle.line(14.3,21.7)).fontWeight(600).fontColor(Colors.ink)
                Text(this.summary()).fontSize(ReadinessStyle.font(ReadinessStyle.body)).lineHeight(ReadinessStyle.line(18,25.7)).fontColor(this.sheetSecondary).id('task.failure.reason')
                if (this.summary()!==this.recovery.failure && this.recovery.failure) {
                  Button(this.details?tr('collapse'):tr('details')).fontSize(ReadinessStyle.font(ReadinessStyle.label)).fontColor(Colors.link).backgroundColor(Color.Transparent).onClick(()=>this.details=!this.details)
                  if (this.details) Text(this.recovery.failure).fontSize(ReadinessStyle.font(Tokens.meta)).fontColor(this.sheetSecondary)
                }
              }.layoutWeight(1).alignItems(HorizontalAlign.Start)
            }.width('100%').constraintSize({minHeight:60}).padding(12).alignItems(VerticalAlign.Top)
              .backgroundColor(Colors.readinessSurface).borderRadius(Tokens.inset)
              .border({width:Tokens.divider,color:Colors.readinessEdge}).shadow(ReadinessStyle.shadow(this.getUIContext())).id('task.failure.card')
            if (this.recovery.question) TaskStartupQuestionCard({recovery:this.recovery,question:this.recovery.question!,secondaryText:this.sheetSecondary})
            if (this.recovery.alternative) {
              Row() { this.action('retry-launcher',tr('task_recovery_instead').replace('{launcher}',this.recovery.alternative),true) }.width('100%')
            }
            Row({space:8}) {
              Row() { this.action('retry',tr('retry'),!this.recovery.alternative) }.layoutWeight(1)
              Row() { this.action('remove',tr('remove'),false) }.layoutWeight(1)
            }.width('100%')
            if (this.recovery.statusMessage()) Text(tr(this.recovery.statusMessage())).fontSize(13).fontColor(Colors.secondary).id('task.failure.error')
            if (this.recovery.state==='stale' || this.recovery.state==='unknown')
              Button(tr('refresh')).fontColor(Colors.link).backgroundColor(Color.Transparent).enabled(!this.recovery.busy).onClick(()=>this.recovery.refresh()).id('task.failure.refresh')
          }
        }.width('100%').alignItems(HorizontalAlign.Start).padding(16)
      }.layoutWeight(1).width('100%').align(Alignment.Top)
    }.width('100%').height('100%').backgroundColor(Colors.surface).id('task.failure.sheet')
  }
}
