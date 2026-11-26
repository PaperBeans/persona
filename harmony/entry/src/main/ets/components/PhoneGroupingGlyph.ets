import { Colors } from '../design/Colors';

@ComponentV2
export struct PhoneGroupingGlyph {
  @Param controlHeight:number = 44;

  build() {
    Column({space:2}) {
      ForEach([14,10,5],(width:number) => {
        Row().width(width*this.controlHeight/44).height(1.5*this.controlHeight/44).borderRadius(1).backgroundColor(Colors.ink)
      },(width:number) => String(width))
    }.alignItems(HorizontalAlign.Center).accessibilityLevel('no')
  }
}
