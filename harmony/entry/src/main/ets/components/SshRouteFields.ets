import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { SshEndpointRow } from './SshEndpointRow';

@ComponentV2
export struct SshRouteFields {
  @Param host: string = '';
  @Param port: string = '';
  @Param always: boolean = false;
  @Event onHost: (value: string) => void = () => {};
  @Event onPort: (value: string) => void = () => {};
  @Event onAlways: (value: boolean) => void = () => {};

  build() {
    Column({space:8}) {
      Text(tr('phone_pair_ssh_target')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      Column() {
        SshEndpointRow({label:tr('phone_pair_ssh_target_address'),value:this.host,prompt:'127.0.0.1',fieldId:'ssh.targetHost',onValue:this.onHost})
        Divider().color(Colors.divider).strokeWidth(0.5).margin({left:18})
        SshEndpointRow({label:tr('phone_pair_ssh_port'),spokenLabel:tr('phone_pair_ssh_forward_port'),value:this.port,prompt:'18660',
          fieldId:'ssh.targetPort',numeric:true,maxLength:5,onValue:this.onPort})
      }.width('100%').backgroundColor(Colors.surface).borderRadius(14)
      Text(tr('phone_pair_ssh_target_help')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      Row({space:14}) {
        Text(tr('phone_pair_ssh_always')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontColor(Colors.ink).layoutWeight(1)
        Toggle({type:ToggleType.Switch,isOn:this.always}).onChange(this.onAlways).accessibilityText(tr('phone_pair_ssh_always')).id('ssh.always')
      }.width('100%').constraintSize({minHeight:52}).padding({left:18,right:16}).backgroundColor(Colors.surface).borderRadius(14)
      Text(tr('phone_pair_ssh_fallback_help')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
}
