import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct PhoneFirstRun {
  @Param error: string = '';
  @Param pendingCleanup: boolean = false;
  @Event onScan: () => void = () => {};
  @Event onManual: () => void = () => {};
  @Event onDemo: () => void = () => {};
  @Event onCleanup: () => void = () => {};
  @Local private viewportWidth: number = 402;

  private metric(standard: number, largest: number): number {
    return PhoneStyle.formSize(standard, largest, nativeEnvironment.fontScale);
  }

  private ink(): ResourceColor {
    return nativeEnvironment.dark ? Color.White : Color.Black;
  }

  private help(): void {
    this.getUIContext().showAlertDialog({
      title: tr('welcome_action_help'),
      message: tr('welcome_action_hint'),
      alignment: DialogAlignment.Center,
      width: Math.min(320, Math.max(0, this.viewportWidth - 32)),
      cornerRadius: 30,
      primaryButton: { value: tr('welcome_action_done'), action: () => {} }
    });
  }

  @Builder
  shortcut(kind: string, image: Resource, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 4 }) {
        Stack() {
          SymbolGlyph(image).fontSize(this.metric(23, 29) + 'vp')
            .fontColor([this.ink()]).accessibilityLevel('no')
        }.width(this.metric(44, 57.7)).aspectRatio(1).borderRadius(40)
          .backgroundColor(nativeEnvironment.dark ? '#252526' : '#DDDDDE')
        Text(tr('welcome_action_' + kind)).fontSize(this.metric(15, 20) + 'vp')
          .lineHeight(this.metric(18, 25.3) + 'vp').fontWeight(600)
          .fontColor(this.ink()).maxLines(1)
      }
    }.padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('welcome_action_' + kind))
      .onClick(action).id('welcome.' + kind)
  }

  @Builder
  actions() {
    this.shortcut('other', $r('sys.symbol.keyboard'), () => this.onManual())
    this.shortcut('help', $r('sys.symbol.questionmark_circle'), () => this.help())
    this.shortcut('demo', $r('sys.symbol.play_circle_fill'), () => this.onDemo())
  }

  build() {
    Scroll() {
      Column({ space: 24 }) {
        Column({ space: 16 }) {
          Image(nativeEnvironment.dark ? $r('app.media.welcome_app_icon_dark') : $r('app.media.welcome_app_icon'))
            .width(80).height(80).borderRadius(18).objectFit(ImageFit.Cover).accessibilityLevel('no')
          Text(tr('persona')).fontSize(this.metric(28, 34) + 'vp').lineHeight(this.metric(34, 41) + 'vp')
            .fontWeight(FontWeight.Bold).fontColor(this.ink())
        }
        Button({ type: ButtonType.Capsule }) {
          Row({ space: 8 }) {
            SymbolGlyph($r('sys.symbol.qrcode')).fontSize(this.metric(17, 23) + 'vp')
              .fontColor([Color.White]).accessibilityLevel('no')
            Text(tr('welcome_action_scan')).fontSize(this.metric(17, 23) + 'vp').fontWeight(600)
              .fontColor(Color.White).maxLines(1)
          }
        }.width('100%').height(this.metric(62, 72)).padding({ left: 12, right: 12 })
          .backgroundColor(nativeEnvironment.dark ? '#0A84FF' : '#007AFF')
          .accessibilityText(tr('welcome_action_scan')).onClick(() => this.onScan()).id('welcome.connect')
        if (this.viewportWidth >= this.metric(260, 330)) {
          Row({ space: 24 }) { this.actions() }
        } else {
          Column({ space: 8 }) { this.actions() }
        }
        if (this.error) {
          Text(this.error).fontSize(14).fontColor(Colors.danger).textAlign(TextAlign.Center)
        }
        if (this.pendingCleanup) {
          Button(tr('retry_cleanup'), { buttonStyle: ButtonStyleMode.TEXTUAL }).onClick(() => this.onCleanup())
        }
      }.width('100%').constraintSize({ maxWidth: 380, minHeight: '100%' })
        .padding({ left: 24, right: 24, top: 40, bottom: 24 }).justifyContent(FlexAlign.Center)
    }.width('100%').height('100%').scrollBar(BarState.Off).backgroundColor(Colors.canvas)
      .onAreaChange((_: Area, area: Area) => { this.viewportWidth = Number(area.width); })
  }
}
