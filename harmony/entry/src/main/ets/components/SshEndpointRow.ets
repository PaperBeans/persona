import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';

@ComponentV2
export struct SshEndpointRow {
  @Require @Param label: string;
  @Require @Param value: string;
  @Require @Param prompt: string;
  @Require @Param fieldId: string;
  @Param spokenLabel: string = '';
  @Param maxLength: number = 253;
  @Param numeric: boolean = false;
  @Event onValue: (value: string) => void = () => {};

  build() {
    Row({space:10}) {
      Text(this.label).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontColor(Colors.ink)
      TextInput({text:this.value,placeholder:this.prompt}).type(this.numeric ? InputType.Number : InputType.Normal)
        .maxLength(this.maxLength).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).textAlign(TextAlign.End)
        .padding(0).backgroundColor(Color.Transparent).layoutWeight(1)
        .onChange(this.onValue).accessibilityText(this.spokenLabel || this.label).id(this.fieldId)
    }.width('100%').height(52).padding({left:18,right:18})
  }
}
