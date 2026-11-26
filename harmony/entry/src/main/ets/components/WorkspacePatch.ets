import { Colors } from '../design/Colors';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { PatchLine, patchLines } from '../models/WorkspaceBrowser';

@ComponentV2
export struct WorkspacePatch {
  @Require @Param patch:string;
  @Param fileOnly:boolean = false;
  @Param compact:boolean = false;
  @Local available:number = 320;
  @Computed get lines():PatchLine[] { return patchLines(this.patch, this.fileOnly); }
  @Computed get contentWidth():number {
    const columns=this.lines.reduce((n:number,line:PatchLine) => Math.max(n,line.text.length),0);
    // The compact reader uses the same fixed 11-point code face and 13-character gutter as iPhone.
    const width = this.compact ? (columns+15)*6.8+12 : (columns*7+80)*nativeEnvironment.fontScale;
    return Math.max(this.available,Math.min(36000,width));
  }
  private gutter(line:PatchLine):string {
    const sign=line.kind==='added' ? '+' : line.kind==='removed' ? '−' : ' ';
    return line.oldLine.padStart(5,' ') + line.newLine.padStart(5,' ') + ' ' + sign + ' ';
  }
  private body(line:PatchLine):string {
    return line.kind==='added' || line.kind==='removed' || line.kind==='context' ? line.text.slice(1) : line.text;
  }
  private fill(kind:string):ResourceColor {
    if (kind==='added') return this.compact ? Colors.codeAddedFill : '#182CAB69';
    if (kind==='removed') return this.compact ? Colors.codeRemovedFill : '#1CDA5350';
    if (kind==='hunk') return this.compact ? Colors.codeHunk : Colors.control;
    return Color.Transparent;
  }
  build() {
    Scroll() {
      List() {
        Repeat<PatchLine>(this.lines).each((entry:RepeatItem<PatchLine>) => {
          ListItem() {
            Row({space:this.compact ? 0 : 3}) {
              if (this.compact) {
                Text(this.gutter(entry.item)).width(88.4).fontSize('11vp').lineHeight('13.12vp').fontFamily('monospace').fontColor(Colors.tertiary)
              } else {
                Text(entry.item.oldLine).width(31*nativeEnvironment.fontScale).fontSize(11).fontFamily('monospace').fontColor(Colors.secondary).textAlign(TextAlign.End)
                Text(entry.item.newLine).width(31*nativeEnvironment.fontScale).fontSize(11).fontFamily('monospace').fontColor(Colors.secondary).textAlign(TextAlign.End)
              }
              Text((this.compact ? this.body(entry.item) : entry.item.text) || ' ').layoutWeight(1)
                .fontSize(this.compact ? '11vp' : 11).lineHeight(this.compact ? '13.12vp' : undefined).fontFamily('monospace')
                .fontColor(entry.item.kind==='hunk' || entry.item.kind==='meta' ? Colors.secondary : Colors.ink)
                .copyOption(CopyOptions.LocalDevice)
            }.width('100%').constraintSize({minHeight:this.compact ? 13.12 : 14*nativeEnvironment.fontScale}).alignItems(VerticalAlign.Top)
              .backgroundColor(this.fill(entry.item.kind))
          }
        }).key((line:PatchLine,index:number) => String(index)+':'+line.kind+':'+line.oldLine+':'+line.newLine+':'+line.text).virtualScroll({totalCount:this.lines.length,reusable:false})
      }.width(this.contentWidth).height('100%').padding({top:this.compact ? 6 : 0,bottom:this.compact ? 6 : 0})
    }.scrollable(ScrollDirection.Horizontal).width('100%').height('100%').align(Alignment.TopStart)
      .onAreaChange((_:Area,a:Area) => this.available=Number(a.width)).id('session.git.patch')
  }
}
