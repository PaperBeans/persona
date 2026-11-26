import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';

// The role column has the same width for every card. Its three ink shapes keep their own aspect ratios.
@ComponentV2
export struct AgentNeedsYouMark {
  @Param kind: string = '';
  @Param fontScale: number = 1;
  @Param surface: ResourceColor = Colors.surface;

  @Computed get glyphScale(): number { return Math.min(this.fontScale,23/17); }

  @Computed get inkHeight(): number {
    return this.kind==='plan' ? 20 : this.kind==='question' ? 17.3 : this.kind==='permission' ? 17.7 : 18;
  }

  build() {
    Stack() {
      if (this.kind === 'plan') {
        Shape() {
          Path().commands('M2.4 2 H4 C4.2 0.8 5.2 0 7 0 S9.8 0.8 10 2 H11.6 Q14 2 14 4.4 V17.6 Q14 20 11.6 20 H2.4 Q0 20 0 17.6 V4.4 Q0 2 2.4 2 Z')
            .fill(Colors.needsMark).stroke(Color.Transparent).strokeWidth(0)
          Path().commands('M5.3 2.4 H8.7 M3.2 8 H3.3 M5.7 8 H10.8 M3.2 11 H3.3 M5.7 11 H10.8 M3.2 14 H3.3 M5.7 14 H9.2')
            .fill(Color.Transparent).stroke(this.surface).strokeWidth('1.2px').strokeLineCap(LineCapStyle.Round)
        }.viewPort({x:0,y:0,width:'14px',height:'20px'}).width(14).height(20).scale({x:this.glyphScale,y:this.glyphScale})
      } else if (this.kind === 'question') {
        Shape() {
          Path().commands('M9.2 0 C14.4 0 18.3 3.2 18.3 7.8 S14.4 15.5 9.2 15.5 C8 15.5 7 15.4 6.1 15.2 C4.6 16.4 3.1 17.1 1.6 17.3 L2.5 13.1 C0.9 11.7 0 9.9 0 7.8 C0 3.2 3.9 0 9.2 0 Z')
            .fill(Colors.needsMark).stroke(Color.Transparent).strokeWidth(0)
          Path().commands('M6.8 5.3 C6.9 2.8 11.8 2.7 11.8 5.3 C11.8 7 9.1 7 9.1 9 M9.1 11.2 V11.3')
            .fill(Color.Transparent).stroke(this.surface).strokeWidth('1.5px').strokeLineCap(LineCapStyle.Round)
        }.viewPort({x:0,y:0,width:'18.3px',height:'17.3px'}).width(18.3).height(17.3).scale({x:this.glyphScale,y:this.glyphScale})
      } else if (this.kind === 'permission') {
        Shape() {
          Path().commands('M1.2 11.3 C0.4 10.1 -0.2 8.8 0.4 8.2 Q1.3 7.5 2.2 8.8 L3.7 10.9 L3.4 3.5 C3.3 1.9 5.1 1.7 5.3 3.2 L5.8 8.1 L5.8 1.4 C5.8 -0.3 7.8 -0.4 7.9 1.4 L8.1 7.9 L8.6 2.1 C8.8 0.5 10.6 0.8 10.5 2.3 L10.1 8.3 L11 4.5 C11.3 3.1 13.1 3.5 12.8 5 L12.4 8.3 L13 10.8 C13.6 14.4 12 17.7 8.1 17.7 C5.2 17.7 3.6 15.8 1.2 11.3 Z')
            .fill(Colors.needsMark).stroke(Color.Transparent).strokeWidth(0)
        }.viewPort({x:0,y:0,width:'13.3px',height:'17.7px'}).width(13.3).height(17.7).scale({x:this.glyphScale,y:this.glyphScale})
      } else {
        SymbolGlyph($r('sys.symbol.questionmark_circle')).fontSize(String(18*this.glyphScale)+'vp').fontColor([Colors.needsMark])
      }
    }.width(PhoneStyle.sidebarMetric(24,this.fontScale)).height(this.inkHeight*this.glyphScale).flexShrink(0).accessibilityLevel('no')
  }
}
