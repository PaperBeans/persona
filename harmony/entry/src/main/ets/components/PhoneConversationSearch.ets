import { Tokens } from '../design/Tokens';
import { PhoneStyle } from '../design/PhoneStyle';
import { phoneSearchExcerpt, SearchTextPart } from '../core/PhoneSearchExcerpt';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow } from '../models/Wire';
import { PhoneSearchIndex, PhoneChatMatch, localChatMatches, mergeChatMatches } from '../models/PhoneChatSearch';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';

@ComponentV2
export struct PhoneConversationSearch {
  @Require @Param model: ClientModel;
  @Param initialAgents: boolean = false;
  @Param inline: boolean = false;
  @Param showScopes: boolean = true;
  @Event onCancel: () => void = () => {};
  @Event onOpen: (row: ResourceRow,hit:string) => void = () => {};
  @Local query: string = '';
  @Local agents: boolean = false;
  @Local search: PhoneSearchIndex = new PhoneSearchIndex();
  private field: TextInputController = new TextInputController();
  aboutToAppear(): void { this.agents = this.initialAgents; }
  aboutToDisappear(): void { this.search.close(); }
  @Monitor('model.profile') newOwner(): void { this.query = ''; this.search.close(); }
  @Monitor('query','agents','model.connected') update(): void { this.search.schedule(this.model, this.query, this.agents); }
  @Computed get rows(): PhoneChatMatch[] {
    const rows = this.agents ? this.model.directorySessions : this.model.conversations;
    return mergeChatMatches(localChatMatches(rows,this.query,this.agents),this.agents ? [] : this.search.matches);
  }
  private main(hit:PhoneChatMatch):boolean {
    return !this.agents && !!this.model.mainId && hit.row.id===this.model.mainId &&
      (!this.model.mainMachineId || hit.row.machine===this.model.mainMachineId);
  }
  @Computed get assistantDrawer():boolean { return this.inline && !this.agents; }
  private sidebarSize(regular:number,large:number):number { return PhoneStyle.formSize(regular,large,nativeEnvironment.fontScale); }
  private fieldHeight():number {
    return this.assistantDrawer ? this.sidebarSize(40,52.7) : Math.max(38,Math.ceil(24*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(16)))+8);
  }
  @Builder fieldBar() {
    Row({space:8}) {
      Row({space:this.assistantDrawer ? 8 : 5}) {
        SymbolGlyph($r('sys.symbol.magnifyingglass')).fontSize(this.assistantDrawer ? Tokens.interfaceFontVp(15,nativeEnvironment.fontScale) : '16vp').fontColor([Colors.secondary])
        TextInput({text:this.query,placeholder:tr(this.agents ? 'search_sessions' : 'flow_search_chats'),controller:this.field})
          .height(this.fieldHeight()).padding({left:2,right:1}).layoutWeight(1).fontColor(Colors.ink)
          .fontSize(Tokens.interfaceFontVp(16,nativeEnvironment.fontScale)).placeholderFont({size:Tokens.interfaceFontVp(16,nativeEnvironment.fontScale)})
          .backgroundColor(Color.Transparent).cancelButton({style:CancelButtonStyle.INPUT,icon:{
            size:this.sidebarSize(24,32),color:Colors.tertiary,src:$r('app.media.phone_search_clear')
          }})
          .onChange((v:string) => this.query = v).enterKeyType(EnterKeyType.Search).defaultFocus(true).id('phone_search.field')
      }.layoutWeight(1).alignItems(VerticalAlign.Center).padding({left:this.assistantDrawer ? 12 : 10,right:this.assistantDrawer ? 12 : 5})
        .backgroundColor(this.assistantDrawer ? Colors.tertiaryFill : Colors.control).borderRadius(this.assistantDrawer ? this.fieldHeight()/2 : 12)
      Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).height(this.fieldHeight()).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.link).padding({left:3,right:2})
        .onClick(() => this.onCancel()).id('phone_search.cancel')
    }.width('100%').padding({left:this.assistantDrawer ? 24 : 12,right:this.assistantDrawer ? 24 : 12,
      top:this.assistantDrawer ? 8 : 6,bottom:this.assistantDrawer ? 8 : 6})
  }
  @Builder scopeButton(isAgent:boolean,label:string) {
    Button(tr(label),{type:ButtonType.Capsule}).layoutWeight(1).height(Math.max(31,Math.ceil(21*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(14)))+8)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14))
      .fontWeight(this.agents === isAgent ? FontWeight.Bold : FontWeight.Normal)
      .fontColor(this.agents === isAgent ? Colors.ink : Colors.secondary)
      .backgroundColor(this.agents === isAgent ? Colors.surface : Color.Transparent)
      .accessibilitySelected(this.agents === isAgent).onClick(() => this.agents = isAgent).id(isAgent ? 'phone_search.agents' : 'phone_search.chats')
  }
  build() {
    Column() {
      if (this.inline) this.fieldBar()
      else Row() {
        Text(tr(this.agents ? 'search_sessions' : 'flow_search_chats')).fontSize(29).maxFontScale(Tokens.interfaceScaleFor(29)).fontWeight(FontWeight.Bold).fontColor(Colors.ink).layoutWeight(1)
      }.width('100%').padding({left:16,right:16,top:12,bottom:8})
      if (this.showScopes) Row() { this.scopeButton(false,'persona'); this.scopeButton(true,'agents') }
        .constraintSize({minHeight:35}).padding(2).margin({left:12,right:12,top:3,bottom:5}).width('92%').backgroundColor(Colors.control).borderRadius(18)
      List() {
        if (this.rows.length && this.query.trim()) ListItem() {
          Text(tr(this.agents ? 'project_agent_count' : 'phone_search_conversation_count').replace('{count}',String(this.rows.length)))
            .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
            .height(this.assistantDrawer ? this.sidebarSize(35.7,43) : undefined)
            .padding({left:this.assistantDrawer ? 20 : 15,right:12,top:7,bottom:5}).id('phone_search.count')
        }
        if (this.search.busy) ListItem() { LoadingProgress().width(23).height(23).margin(10) }
        if (!this.rows.length && !this.search.busy) ListItem() { Text(tr(this.query ? 'no_results' : 'empty')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary).padding(16) }
        ForEach(this.rows,(hit:PhoneChatMatch) => {
          ListItem() {
            Row() {
            Row({space:this.assistantDrawer ? 12 : 10}) {
              SymbolGlyph(this.agents ? Symbols.agent : this.main(hit) ? $r('sys.symbol.house') : Symbols.persona)
                .fontSize(this.assistantDrawer ? String(this.sidebarSize(22,29))+'vp' : '19vp')
                .width(this.assistantDrawer ? this.sidebarSize(24,31.5) : undefined).fontColor([Colors.secondary])
              Column({space:this.assistantDrawer ? 2 : 3}) {
                Text(this.main(hit) ? tr('main_chat') : hit.row.title || tr('persona'))
                  .fontSize(this.assistantDrawer ? Tokens.interfaceFontVp(17,nativeEnvironment.fontScale) : 16).maxFontScale(Tokens.interfaceScaleFor(16))
                  .lineHeight(this.assistantDrawer ? String(this.sidebarSize(20.3,27.7))+'vp' : undefined)
                  .fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(this.assistantDrawer ? 1 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
                if (hit.subtitle && this.assistantDrawer) {
                  Text() {
                    ForEach(phoneSearchExcerpt(hit.subtitle,hit.ranges,nativeEnvironment.fontScale),(part:SearchTextPart) => {
                      Span(part.text).fontWeight(part.matched ? FontWeight.Bold : FontWeight.Normal)
                    },(part:SearchTextPart,index:number) => JSON.stringify([index,part.matched,part.text]))
                  }.fontSize(Tokens.interfaceFontVp(15,nativeEnvironment.fontScale))
                    .lineHeight(String(this.sidebarSize(18,25.3))+'vp').fontColor(Colors.secondary)
                    .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).id('phone_search.snippet')
                } else if (hit.subtitle) {
                  Text(hit.subtitle).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).maxLines(this.inline ? 1 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
                }
              }.alignItems(HorizontalAlign.Start).layoutWeight(1)
              if (!this.inline) SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
            }.layoutWeight(1).constraintSize({minHeight:this.assistantDrawer ? this.sidebarSize(44,57.7) : 48})
              .padding({left:this.assistantDrawer ? 12 : 13,right:12,top:this.assistantDrawer ? 4 : 8,bottom:this.assistantDrawer ? 4 : 8})
              .backgroundColor(this.inline ? Colors.subtle : Colors.surface).borderRadius(this.inline ? 12 : 0)
            }.width('100%').padding({left:this.assistantDrawer ? 8 : 0,right:this.assistantDrawer ? 8 : 0})
          }.onClick(() => this.onOpen(hit.row,hit.hitId)).accessibilityGroup(true)
            .accessibilityText([this.main(hit) ? tr('main_chat') : hit.row.title,hit.subtitle].filter((s:string)=>!!s).join(', ')).id('phone_search.result')
        },(hit:PhoneChatMatch) => JSON.stringify([hit.row.key(),hit.row.title,hit.subtitle,hit.hitId,hit.ranges]))
        if (this.search.partial || this.search.failed) ListItem() {
          Text(tr(this.search.failed ? 'flow_search_local' : 'flow_search_partial')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).padding(12)
        }
      }.layoutWeight(1).width('100%').divider({strokeWidth:this.assistantDrawer ? 0 : 0.5,startMargin:43,endMargin:12,color:Colors.divider})
      if (!this.inline) this.fieldBar()
    }.width('100%').height('100%').backgroundColor(this.inline ? Colors.canvas : Colors.grouped).id('phone_search.results')
  }
}
