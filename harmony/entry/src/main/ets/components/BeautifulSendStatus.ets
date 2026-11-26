import { accessibility } from '@kit.AccessibilityKit';
import { FrameCallback } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { PhoneOutlineIcon } from '../design/PhoneOutlineIcon';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { tr } from '../design/Strings';

class SendMotionFrame extends FrameCallback {
  private callback: () => void;
  constructor(callback: () => void) { super(); this.callback = callback; }
  onFrame(_time: number): void { this.callback(); }
}

// Persona's BeautifulUI bridge uses the system label for the highlight and secondary label for both base inks.
@Extend(Text) function feedbackType(workHeader: boolean) {
  .fontSize(15).maxFontScale(Tokens.interfaceScaleFor(workHeader ? 12.5 : 15))
  .letterSpacing(workHeader ? -0.15 : 0).lineHeight(workHeader ? 22.5 : undefined)
  .maxLines(workHeader ? 1 : undefined)
}

@ComponentV2
export struct BeautifulSendStatus {
  @Require @Param phase: string;
  @Require @Param workHeader: boolean;
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private reduced: boolean = true;
  @Local private visible: boolean = false;
  @Local private travel: boolean = false;
  @Local private textWidth: number = 0;
  @Local private textHeight: number = 0;
  private mounted: boolean = false;
  private animationGeneration: number = 0;
  private readonly reducedChanged = (value: boolean): void => { this.reduced = value; };

  aboutToAppear(): void {
    this.mounted = true;
    this.reduced = this.env.reduceMotion;
    try { accessibility.onAnimationReduceStateChange(this.reducedChanged); } catch (_) {}
  }
  aboutToDisappear(): void {
    this.mounted = false; this.animationGeneration++; this.travel = false;
    try { accessibility.offAnimationReduceStateChange(this.reducedChanged); } catch (_) {}
  }
  private moves(): boolean { return this.mounted && this.visible && this.env.foreground && !this.reduced && this.textWidth > 0; }
  @Monitor('reduced', 'env.foreground', 'visible', 'textWidth', 'workHeader') restart(): void {
    const generation = ++this.animationGeneration; this.travel = false;
    if (!this.moves()) return;
    this.getUIContext().postFrameCallback(new SendMotionFrame(() => {
      if (generation === this.animationGeneration && this.moves()) this.travel = true;
    }));
  }
  @Builder band() {
    if (this.moves() && this.textHeight > 0) {
      Stack({alignContent:Alignment.TopStart}) {
        Row().width(this.textWidth*2).height(this.textHeight)
          .linearGradient({angle:90,colors:[[Color.Transparent,0.35],[$r('sys.color.font_primary'),0.5],[Color.Transparent,0.65]]})
          .position({x:0,y:0}).translate({x:(this.travel ? 0.5 : -1.5)*this.textWidth})
          .animation(this.travel ? {duration:1400,curve:Curve.Linear,iterations:-1,playMode:PlayMode.Normal} : undefined)
        Text(tr('chat_feedback_'+this.phase)).feedbackType(this.workHeader)
          .width(this.textWidth).height(this.textHeight).fontColor(Color.White)
          .blendMode(BlendMode.DST_IN,BlendApplyType.OFFSCREEN)
      }.width(this.textWidth).height(this.textHeight).clip(true)
        .blendMode(BlendMode.SRC_OVER,BlendApplyType.OFFSCREEN)
        .accessibilityLevel('no-hide-descendants').hitTestBehavior(HitTestMode.None)
    }
  }
  @Builder arc(animated: boolean) {
    Shape() {
      Path().commands('M2.4634 2.4634 A5.85 5.85 0 0 1 10.7366 2.4634')
        .fill(Color.Transparent).stroke($r('sys.color.font_secondary')).strokeWidth('1.5px').strokeLineCap(LineCapStyle.Butt)
    }.viewPort({x:0,y:0,width:'13.2px',height:'13.2px'}).width(13.2).height(13.2)
      .rotate({angle:animated && this.travel ? 360 : 0})
      .animation(animated && this.travel ? {duration:700,curve:Curve.Linear,iterations:-1,playMode:PlayMode.Normal} : undefined)
  }
  @Builder runIcon() {
    Stack() {
      Circle().width(11.7).height(11.7).fill(Color.Transparent)
        .stroke($r('sys.color.font_primary')).strokeWidth(1.5).opacity(0.16)
      if (this.moves()) this.arc(true)
      else this.arc(false)
    }.width(14.4).height(13.2).accessibilityLevel('no-hide-descendants')
  }
  build() {
    Row({space:6}) {
      if (this.workHeader) this.runIcon()
      else PhoneOutlineIcon({name:'beautifulSparkle',iconScale:13/24,color:Colors.ai})
      Text(tr('chat_feedback_'+this.phase)).feedbackType(this.workHeader).fontColor($r('sys.color.font_secondary'))
        .onAreaChange((_old:Area,area:Area) => {
          this.textWidth = Number(area.width); this.textHeight = Number(area.height);
        })
        .overlay(this.band(),{align:Alignment.TopStart})
    }.width('100%').padding({left:Tokens.page,right:Tokens.page,top:2,bottom:3})
      .accessibilityGroup(true).accessibilityText(tr('chat_feedback_'+this.phase)).id('chat.sendFeedback.'+this.phase)
      .onVisibleAreaChange([0],(visible:boolean,ratio:number) => { this.visible = visible || ratio > 0; })
  }
}
