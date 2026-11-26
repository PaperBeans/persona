import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';

// Follow the width of this row's actual text column, including in a split pane or a nested checkout.
@ComponentV2
export struct ProjectAgentPlaceLine {
  @Param place: string = '';
  @Param modelName: string = '';
  @Param showsSource: boolean = false;
  @Param sourceTitle: string = '';
  @Param todoTitle: string = '';
  @Param sessionId: string = '';
  @Param starter: boolean = false;
  @Param fontScale: number = 1;
  @Local availableWidth: number = 0;

  @Computed get fit(): string {
    const ui = this.getUIContext(), scale = this.fontScale;
    const width = (text:string, size:number): number => !text ? 0 :
      ui.px2vp(Math.ceil(ui.getMeasureUtils().measureText({textContent:text,fontSize:Tokens.interfaceFontVp(size,scale)})));
    const source = this.showsSource ? width(this.sourceTitle || tr('terminal'),13)+10 : 0;
    const model = this.modelName ? width(this.modelName,12)+12 : 0;
    const icon = this.todoTitle ? 13*Math.min(scale,Tokens.interfaceScaleFor(13)) : 0;
    const todo = icon ? icon+4+width(this.todoTitle,13)+14 : 0;
    const placeWidth = width(this.place,13), starterWidth = this.starter ? width(tr('session_space_starter'),12) : 0;
    const occupied = (modelWidth:number,todoWidth:number): number => {
      const parts = [source,placeWidth,modelWidth,todoWidth,starterWidth]
        .filter((part:number) => part>0);
      return parts.reduce((sum:number,part:number) => sum+part,0) + 4*Math.max(0,parts.length-1);
    };
    if (occupied(model,todo)<=this.availableWidth) return 'full';
    if (occupied(model,icon)<=this.availableWidth) return 'todo-icon';
    return 'place';
  }
  build() {
    Row({space:4}) {
      if (this.showsSource) Text(this.sourceTitle || tr('terminal')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.ai)
        .maxLines(1).padding({left:5,right:5,top:1,bottom:1}).backgroundColor(Colors.subtle).borderRadius(7)
      if (this.place) Text(this.place).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
        .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
      if (this.modelName && this.fit !== 'place') Text(this.modelName).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary)
        .maxLines(1).padding({left:6,right:6,top:1,bottom:1}).border({width:0.5,color:Colors.divider,radius:8})
      if (this.todoTitle) {
        Row({space:4}) {
          SymbolGlyph($r('sys.symbol.list_checkmark')).fontSize(Tokens.interfaceFontVp(13,this.fontScale)).fontColor([Colors.secondary])
          if (this.fit === 'full') Text(this.todoTitle).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).maxLines(1)
        }.padding({left:this.fit === 'full' ? 7 : 0,right:this.fit === 'full' ? 7 : 0,top:this.fit === 'full' ? 1 : 0,bottom:this.fit === 'full' ? 1 : 0})
          .border({width:this.fit === 'full' ? 0.5 : 0,color:Colors.divider,radius:8})
          .accessibilityGroup(true).accessibilityText(tr('todo')+' · '+this.todoTitle).id('projects.session_todo.'+this.sessionId)
      }
      if (this.starter) Text(tr('session_space_starter')).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary).maxLines(1)
    }.width('100%').margin({top:2})
      .onAreaChange((before:Area,after:Area) => {
        const width = Number.parseFloat(String(after.width));
        if (width>0 && width!==this.availableWidth) this.availableWidth = width;
      })
  }
}
