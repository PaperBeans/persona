import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { PhoneOutlineIcon } from '../design/PhoneOutlineIcon';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { isPairingAddress } from '../core/WireCompatibility';
import { PhonePairPreview } from './PhonePairPreview';

@ComponentV2
export struct PhonePairingForm {
  @Param password: boolean = false;
  @Param manual: boolean = false;
  @Param address: string = '';
  @Param code: string = '';
  @Param error: string = '';
  @Param busy: boolean = false;
  @Param accessExpanded: boolean = false;
  @Param accessID: string = '';
  @Param accessSecret: string = '';
  @Param sshLabel: string = '';
  @Param sshPolicy: string = '';
  @Event onAddress: (value: string) => void = () => {};
  @Event onCode: (value: string) => void = () => {};
  @Event onPassword: (value: boolean) => void = () => {};
  @Event onManual: () => void = () => {};
  @Event onPhoto: () => void = () => {};
  @Event onScanned: (value: string) => void = () => {};
  @Event onAccessExpanded: (expanded: boolean) => void = () => {};
  @Event onAccessID: (value: string) => void = () => {};
  @Event onAccessSecret: (value: string) => void = () => {};
  @Event onSsh: () => void = () => {};
  @Event onClose: () => void = () => {};
  @Event onConnect: () => void = () => {};

  private typeMetric(regular: number, large: number): number {
    return PhoneStyle.formSize(regular, large, nativeEnvironment.fontScale);
  }

  private tint(): ResourceColor {
    return nativeEnvironment.dark ? '#0A84FF' : '#007AFF';
  }

  private ready(): boolean {
    return !this.busy && !!this.address.trim() &&
      (this.password || !!this.code.trim() || isPairingAddress(this.address.trim()));
  }

  @Builder
  header() {
    Stack() {
      Text(tr('phone_pair_title')).fontSize(this.typeMetric(17, 22) + 'vp').fontWeight(600)
        .fontColor(Colors.ink).maxLines(1)
      Row() {
        Button({ type: ButtonType.Circle }) {
          SymbolGlyph($r('sys.symbol.xmark')).fontSize(this.typeMetric(18, 22) + 'vp').fontColor([Colors.ink])
        }.width(44).height(44).backgroundColor(Colors.sheetControl)
          .border({ width: 0.5, color: nativeEnvironment.dark ? '#505053' : '#D4D4D7' }).accessibilityText(tr('close'))
          .onClick(() => this.onClose()).id('pair.close')
        Blank()
        Stack() {
          Button(tr('connect'), { type: ButtonType.Capsule }).height(44).padding({ left: 16, right: 16 })
            .fontSize(this.typeMetric(17, 22) + 'vp').fontWeight(600)
            .fontColor(this.ready() ? Color.White : $r('app.color.project_card_secondary'))
            .backgroundColor(Color.Transparent).enabled(this.ready())
            .onClick(() => this.onConnect()).id('pair.verify')
        }.borderRadius(22).backgroundColor(this.ready() ? this.tint() : Colors.sheetControl)
          .border({ width: 0.5, color: nativeEnvironment.dark ? '#505053' : '#D4D4D7' })
      }.width('100%')
    }.width('100%').height(76).padding({ left: 16, right: 16 })
  }

  @Builder
  segment(isPassword: boolean) {
    Button(tr(isPassword ? 'phone_pair_password' : 'phone_pair_token'), { type: ButtonType.Capsule })
      .layoutWeight(1).height(32).padding({ left: 3, right: 3 })
      .fontSize('13vp').fontWeight(this.password === isPassword ? 600 : 400).fontColor(Colors.ink)
      .backgroundColor(this.password === isPassword ? (nativeEnvironment.dark ? '#717178' : Color.White) : Color.Transparent)
      .accessibilitySelected(this.password === isPassword)
      .onClick(() => this.onPassword(isPassword)).id(isPassword ? 'pair.mode.password' : 'pair.mode.token')
  }

  @Builder
  method() {
    Column() {
      Row() {
        this.segment(true)
        this.segment(false)
      }.height(32).borderRadius(16).backgroundColor(nativeEnvironment.dark ? '#3D3D42' : '#EEEEEF')
    }.width('100%').padding({ left: 20, right: 20, top: 15, bottom: 14 })
      .borderRadius(24).backgroundColor(Colors.sheetCard)
      .margin({ bottom: 18 })
  }

  @Builder
  sectionHeading(value: string) {
    Text(value).fontSize(this.typeMetric(16, 21) + 'vp').fontWeight(FontWeight.Medium)
      .fontColor($r('app.color.project_card_secondary')).height(this.typeMetric(40.33, 47.67))
      .width('100%').padding({ left: 20, right: 20 }).textAlign(TextAlign.Start)
  }

  @Builder
  footer(value: string) {
    Text(value).fontSize(this.typeMetric(13, 17) + 'vp').lineHeight(this.typeMetric(16, 22) + 'vp')
      .fontColor($r('app.color.project_card_secondary')).width('100%')
      .padding({ left: 20, right: 20, top: this.typeMetric(6.5, 7.5), bottom: this.typeMetric(7.17, 7.5) })
  }

  @Builder
  rowSymbol(identity: string, symbol: Resource) {
    Stack() {
      if (identity === 'pair.ssh') {
        PhoneOutlineIcon({ name: 'networkShield', iconScale: this.typeMetric(22, 28) / 25, color: this.tint() })
      } else if (identity === 'pair.other') {
        Row({ space: this.typeMetric(3, 4) }) {
          ForEach([0, 1, 2], (_dot: number) => {
            Circle().width(this.typeMetric(3.3, 4.3)).height(this.typeMetric(3.3, 4.3)).fill(this.tint())
          }, (index: number) => String(index))
        }
      } else {
        SymbolGlyph(symbol).fontSize(this.typeMetric(22, 28) + 'vp').fontColor([this.tint()])
      }
    }.width(this.typeMetric(28, 35)).accessibilityLevel('no')
  }

  @Builder
  actionRow(title: string, symbol: Resource, id: string, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 12 }) {
        this.rowSymbol(id, symbol)
        Text(title).fontSize(this.typeMetric(17, 22) + 'vp').fontColor(this.tint()).maxLines(2).layoutWeight(1)
      }.width('100%')
    }.width('100%').constraintSize({ minHeight: this.typeMetric(52.33, 59.33) }).padding({ left: 20, right: 20 })
      .borderRadius(20).backgroundColor(Colors.sheetCard).enabled(!this.busy)
      .accessibilityText(title).onClick(action).id(id)
  }

  @Builder
  settingsRow(title: string, symbol: Resource, key: string, trailing: string, expanded: boolean, tap: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 12 }) {
        this.rowSymbol(key, symbol)
        Text(title).fontSize(this.typeMetric(17, 22) + 'vp').fontColor(Colors.ink).maxLines(1).layoutWeight(1)
        if (trailing) {
          Text(trailing).fontSize(this.typeMetric(15, 19) + 'vp').fontColor(Colors.secondary)
            .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).constraintSize({ maxWidth: '33%' })
        }
        SymbolGlyph(expanded ? $r('sys.symbol.chevron_down') : $r('sys.symbol.chevron_right')).fontSize(this.typeMetric(12, 15) + 'vp')
          .fontColor([Colors.secondary]).accessibilityLevel('no')
      }.width('100%')
    }.height(this.typeMetric(52.33, 59.33)).width('100%').padding({ left: 20, right: 20 }).backgroundColor(Color.Transparent)
      .accessibilityText(trailing ? title + ' ' + trailing : title).onClick(tap).id(key)
  }

  @Builder
  accessSection() {
    Column() {
      Column() {
        this.settingsRow(tr('phone_pair_access'), $r('sys.symbol.person_shield'), 'pair.access', '', this.accessExpanded,
          () => this.onAccessExpanded(!this.accessExpanded))
        if (this.accessExpanded) {
          Divider().color($r('app.color.persona_divider')).margin({ left: 20, right: 20 })
          TextInput({ text: this.accessID, placeholder: tr('phone_pair_access_id') })
            .fontSize(this.typeMetric(17, 22) + 'vp').height(this.typeMetric(52, 60)).padding({ left: 20, right: 20 })
            .placeholderFont({ size: this.typeMetric(17, 22) + 'vp' }).placeholderColor($r('app.color.project_card_tertiary'))
            .backgroundColor(Color.Transparent).onChange((value: string) => this.onAccessID(value)).id('pair.access.id')
          Divider().color($r('app.color.persona_divider')).margin({ left: 20, right: 20 })
          TextInput({ text: this.accessSecret, placeholder: tr('phone_pair_access_secret') })
            .type(InputType.Password).showPasswordIcon(false).fontSize(this.typeMetric(17, 22) + 'vp')
            .placeholderFont({ size: this.typeMetric(17, 22) + 'vp' }).placeholderColor($r('app.color.project_card_tertiary'))
            .height(this.typeMetric(52, 60)).padding({ left: 20, right: 20 }).backgroundColor(Color.Transparent)
            .onChange((value: string) => this.onAccessSecret(value)).id('pair.access.secret')
        }
      }.width('100%').borderRadius(20).clip(true).backgroundColor(Colors.sheetCard)
      this.footer(tr('phone_pair_access_info'))
    }.margin({ top: 17.67 })
  }

  @Builder
  sshSection() {
    Column() {
      Column() {
        this.settingsRow(tr('phone_pair_ssh'), $r('sys.symbol.key'), 'pair.ssh', this.sshLabel, false, () => this.onSsh())
      }.width('100%').borderRadius(20).clip(true).backgroundColor(Colors.sheetCard)
      this.footer(tr(!this.sshLabel ? 'phone_pair_ssh_info' : this.sshPolicy === 'fallback' ? 'phone_pair_ssh_fallback_parent' :
        this.sshPolicy === 'always' ? 'phone_pair_ssh_always_parent' : 'phone_pair_ssh_configured'))
    }.margin({ top: 17.67 })
  }

  @Builder
  addressFields() {
    Column() {
      this.sectionHeading(tr(this.password ? 'phone_pair_password' : 'phone_pair_manual'))
      Column() {
        TextInput({ text: this.address, placeholder: tr(this.password ? 'phone_pair_https' : 'phone_pair_address') })
          .type(InputType.URL).fontSize(this.typeMetric(17, 22) + 'vp').height(this.typeMetric(52.16, 59.16)).padding(0)
          .placeholderFont({ size: this.typeMetric(17, 22) + 'vp' }).placeholderColor($r('app.color.project_card_tertiary'))
          .backgroundColor(Color.Transparent).onChange((value: string) => this.onAddress(value)).id('pair.address')
        if (!this.password) {
          Divider().color($r('app.color.persona_divider'))
          TextInput({ text: this.code, placeholder: tr('phone_pair_code') })
            .type(InputType.Password).showPasswordIcon(false).fontSize(this.typeMetric(17, 22) + 'vp')
            .placeholderFont({ size: this.typeMetric(17, 22) + 'vp' }).placeholderColor($r('app.color.project_card_tertiary'))
            .height(this.typeMetric(52.16, 59.16)).padding(0).backgroundColor(Color.Transparent)
            .onChange((value: string) => this.onCode(value)).id('pair.code')
        }
      }.padding({ left: 20, right: 20 }).width('100%').borderRadius(20).backgroundColor(Colors.sheetCard)
      if (this.password) { this.footer(tr('phone_pair_password_info')) }
    }.width('100%').margin({ bottom: this.password ? 0 : 17.67 })
  }

  build() {
    Column() {
      this.header()
      Scroll() {
        Column() {
          this.method()
          if (this.password || this.manual) {
            this.addressFields()
            if (!this.password) {
              this.sshSection()
              Column() {
                this.sectionHeading(tr('phone_pair_command'))
                Text('personad hub pair').fontSize(this.typeMetric(15, 19) + 'vp').fontFamily('monospace')
                  .textSelectable(TextSelectableMode.SELECTABLE_UNFOCUSABLE).fontColor(Colors.ink)
                  .padding({ left: 20, right: 20 }).width('100%').height(this.typeMetric(52.33, 59.33))
                  .borderRadius(20).backgroundColor(Colors.sheetCard).id('pair.command')
                this.footer(tr('phone_pair_command_info'))
              }
            }
          } else {
            PhonePairPreview({ active: !this.busy, onCode: (value: string) => this.onScanned(value) })
            Column() {
              this.actionRow(tr('phone_pair_photo'), $r('sys.symbol.picture_on_square'), 'pair.choosePhoto', () => this.onPhoto())
            }.margin({ top: 17.67, bottom: 18 })
            Column() {
              this.actionRow(tr('welcome_action_other'), $r('sys.symbol.ellipsis_circle'), 'pair.other', () => this.onManual())
              this.footer(tr('phone_pair_other_info'))
            }.margin({ top: 17.67 })
          }
          if (!this.password) { this.accessSection() }
          if (this.error) {
            Text(this.error).fontSize(this.typeMetric(13, 17) + 'vp').fontColor(Colors.danger).width('100%').margin({ top: 17.67 })
          }
        }.width('100%').padding({ left: 16, right: 16, top: 29, bottom: 32 })
      }.width('100%').layoutWeight(1).align(Alignment.Top).scrollBar(BarState.Auto)
    }.width('100%').height('100%').backgroundColor(Colors.sheetBackground)
  }
}
