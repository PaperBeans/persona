import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { ReadinessStyle } from '../design/ReadinessStyle';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { TaskStartRecovery } from '../models/TaskStartRecovery';
import { TaskStartupQuestion } from '../models/TaskStartupQuestion';

@ComponentV2
export struct TaskStartupQuestionCard {
  @Require @Param recovery:TaskStartRecovery;
  @Require @Param question:TaskStartupQuestion;
  @Param secondaryText:ResourceColor=Colors.secondary;
  @Local details:boolean=false;
  @Local expired:boolean=false;
  @Local choiceWidth:number=0;
  @Local submitting:string='';
  private async pick(choice:string,attempt:string):Promise<void> {
    if (!this.recovery.canAnswer(choice,attempt)) return;
    const request=JSON.stringify([attempt,choice]);
    this.submitting=request;
    try { await this.recovery.act('answer',choice,attempt); }
    finally { if (this.submitting===request) this.submitting=''; }
  }
  private timer:number=-1;
  aboutToAppear():void { this.watchQuestion(); }
  aboutToDisappear():void { clearTimeout(this.timer); }
  @Monitor('question') watchQuestion():void {
    clearTimeout(this.timer); this.details=false; this.expired=!this.question.open();
    if (!this.expired && Number.isFinite(this.question.until)) this.tick();
  }
  private tick():void {
    const remaining=this.question.until-Date.now();
    if (remaining<=0) {this.expired=true;return;}
    this.timer=setTimeout(()=>this.tick(),Math.min(remaining,2147483647));
  }
  private hint():string {
    const question=this.question;
    const text=tr(question.secret ? 'task_start_secret' : question.kind==='ssh_host_key' ? 'task_start_host' : 'task_start_generic');
    if (!Number.isFinite(question.until)) return text;
    const date=new Date(question.until);
    const locale=this.recovery.language==='default' ? i18n.System.getSystemLanguage() : this.recovery.language;
    const clock=/^zh(?:[-_]|$)/i.test(locale) ? String(date.getHours())+':'+String(date.getMinutes()).padStart(2,'0') :
      date.toLocaleTimeString(locale,{hour:'numeric',minute:'2-digit'});
    return text+' '+tr('task_start_until').replace('{value}',clock);
  }
  private label(choice:string):string { return tr(this.question.label(choice)).replace('{value}',choice); }
  private fitsAcross():boolean {
    const choices=this.question.choices;
    if (choices.length<2 || this.choiceWidth<=0) return true;
    const share=(this.choiceWidth-Tokens.gap*(choices.length-1))/choices.length;
    const ui=this.getUIContext();
    return choices.every((choice:string)=>{
      const width=ui.px2vp(ui.getMeasureUtils().measureText({textContent:this.label(choice),fontSize:ReadinessStyle.font(ReadinessStyle.body),fontWeight:FontWeight.Medium}));
      return Math.ceil(width*0.8)+2*ReadinessStyle.choiceInset<=share;
    });
  }
  @Builder answer(choice:string, attempt:string) {
    Button({type:ButtonType.Capsule}) {
      Stack() {
        Text(this.label(choice)).fontSize(ReadinessStyle.font(ReadinessStyle.body)).minFontSize(ReadinessStyle.metric(12,16.8)+'vp')
          .maxFontSize(ReadinessStyle.font(ReadinessStyle.body)).fontWeight(FontWeight.Medium).maxLines(1).fontColor(Colors.link)
          .opacity(this.submitting===JSON.stringify([attempt,choice]) ? 0 : 1)
        if (this.submitting===JSON.stringify([attempt,choice])) {
          LoadingProgress().width(ReadinessStyle.metric(15,20)).height(ReadinessStyle.metric(15,20))
            .color(Colors.link).accessibilityLevel('no')
        }
      }
    }.padding({left:ReadinessStyle.choiceInset,right:ReadinessStyle.choiceInset})
      .constraintSize({minHeight:ReadinessStyle.metric(32,39.3),maxWidth:'100%'}).backgroundColor(Colors.readinessControl)
      .border({width:Tokens.divider,color:Colors.readinessEdge}).accessibilityText(this.label(choice))
      .enabled(this.recovery.canAnswer(choice,attempt)).onClick(()=>this.pick(choice,attempt))
      .id('readiness-choice-'+choice)
  }
  @Builder actions() {
    Column({space:Tokens.gap}) {
      if (this.fitsAcross()) {
        Row({space:Tokens.gap}) {
          ForEach(this.question.choices,(choice:string)=>{
            Row() { this.answer(choice,this.question.attempt) }.layoutWeight(1).justifyContent(FlexAlign.Start)
          },(choice:string)=>JSON.stringify([this.question.attempt,choice]))
        }.width('100%')
      } else {
        ForEach(this.question.choices,(choice:string)=>{
          this.answer(choice,this.question.attempt)
        },(choice:string)=>JSON.stringify([this.question.attempt,choice]))
      }
      if (this.question.screen) {
        Row() {
          Button({type:ButtonType.Normal}) {
            Row({space:ReadinessStyle.metric(8.3,10)}) {
              SymbolGlyph(this.details?Symbols.up:Symbols.down).fontSize(ReadinessStyle.font(ReadinessStyle.label)).fontColor([this.secondaryText])
              Text(tr('details')).fontSize(ReadinessStyle.font(ReadinessStyle.label))
                .lineHeight(ReadinessStyle.line(14.3,21.7)).fontColor(this.secondaryText).maxLines(1)
            }
          }.height(ReadinessStyle.metric(14.3,21.7)).padding(0).backgroundColor(Color.Transparent)
            .accessibilityText(tr('details')).accessibilityDescription(tr(this.details?'control_expanded':'control_collapsed'))
            .onClick(()=>this.details=!this.details).id('task.start.details')
        }.width('100%').justifyContent(FlexAlign.End)
        if (this.details) Text(this.question.screen).fontSize(ReadinessStyle.font(Tokens.caption)).fontFamily('monospace').copyOption(CopyOptions.LocalDevice)
          .fontColor(this.secondaryText).width('100%').padding(Tokens.gap).backgroundColor(Colors.control).borderRadius(Tokens.gap)
      }
    }.width('100%').alignItems(HorizontalAlign.Start).onAreaChange((_old:Area,next:Area)=>{
      const width=Number(next.width);if(Number.isFinite(width)&&width!==this.choiceWidth)this.choiceWidth=width;
    })
  }
  build() {
    if (!this.expired && this.question.open() && this.recovery.question?.attempt===this.question.attempt) {
      Column({space:Tokens.gap}) {
        Row({space:Tokens.gap}) {
          Row({space:1}) {
            SymbolGlyph(Symbols.expand).fontSize(ReadinessStyle.metric(7,10.5)+'vp').fontColor([this.secondaryText])
            Rect().width(ReadinessStyle.metric(3,4.5)).height(1).fill(this.secondaryText).alignSelf(ItemAlign.End)
          }.width(ReadinessStyle.metric(16.7,25.7)).height(ReadinessStyle.metric(13,19.5)).padding(2)
            .border({width:1,radius:2,color:this.secondaryText}).accessibilityLevel('no')
          Column({space:Tokens.fine}) {
            Text(tr('task_start_title').replace('{value}',this.question.line)).fontSize(ReadinessStyle.font(ReadinessStyle.label))
              .lineHeight(ReadinessStyle.line(14.3,22.3)).lineBreakStrategy(LineBreakStrategy.BALANCED).fontWeight(600).fontColor(Colors.ink)
            Text(this.hint()).fontSize(ReadinessStyle.font(ReadinessStyle.body)).lineHeight(ReadinessStyle.line(19,25.7)).fontColor(this.secondaryText)
            if (this.recovery.error) Text(tr(this.recovery.error)).fontSize(ReadinessStyle.font(ReadinessStyle.body)).fontColor(Colors.danger)
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        }.width('100%').alignItems(VerticalAlign.Top)
        Row() { this.actions() }.width('100%').padding({left:Tokens.page})
      }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.inset)
        .backgroundColor(Colors.readinessSurface).borderRadius(Tokens.inset)
        .border({width:Tokens.divider,color:Colors.readinessEdge})
        .shadow(ReadinessStyle.shadow(this.getUIContext())).id('shell-question')
    }
  }
}
