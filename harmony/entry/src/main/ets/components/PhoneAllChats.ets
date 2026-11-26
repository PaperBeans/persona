import { AssistantChatStateMark, AssistantChatTitle, assistantSourceSymbol } from './AssistantChatMarks';
import { AllChatActions, ChatActionTarget, ChatDirection } from '../models/AllChatActions';
import { LengthMetrics, SymbolGlyphModifier } from '@kit.ArkUI';
import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { Tokens } from '../design/Tokens';
import { tr, trError } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { AdminSnapshot, AdminWorkspace } from '../models/AdminWorkspace';
import { AllChatGroup, allAssistantChatGroups, allChatDetail, assistantChatSource, assistantChatSourceKey,
  assistantChatState, sidebarConversationTitle, SidebarTaskHistory } from '../models/AssistantRecentChats';
import { ClientModel } from '../models/ClientModel';
import { BoundRow, reconcileBoundRows } from '../models/RowBinding';
import { ResourceRow, Wire } from '../models/Wire';

@ComponentV2
export struct PhoneAllChats {
  @Require @Param model: ClientModel;
  @Require @Param history: SidebarTaskHistory;
  @Event onClose: () => void = () => {};
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Local actions: AllChatActions = new AllChatActions();
  private noticeTimer: number = -1;
  @Local filter: string = 'all';
  @Local groups: AllChatGroup[] = [];
  @Local rows: BoundRow<ResourceRow>[] = [];
  @Local options: AdminWorkspace = new AdminWorkspace();
  @Local clock: number = Date.now();
  private timer: number = -1;
  private owner: ClientModel | undefined = undefined;
  private scope: string = '';
  private generation: number = -1;
  private list: Scroller = new Scroller();

  aboutToAppear(): void {
    this.synchronize();
    this.timer = setInterval(() => { this.clock = Date.now(); }, 60000);
  }
  aboutToDisappear(): void { clearInterval(this.timer);clearTimeout(this.noticeTimer);this.actions.close();this.options.hide(); }
  @Monitor('model', 'model.profile', 'model.connected', 'model.conversations', 'model.features', 'model.mainId',
    'model.mainMachineId', 'model.sidebarConfig', 'actions.hidden', 'history.revision') synchronize(): void {
    const scope = this.model.scope(), generation = this.model.connectionGeneration();
    const rebound = this.owner !== this.model || this.scope !== scope || this.generation !== generation;
    if (rebound) { this.filter = 'all'; this.options.hide(); }
    const previous = this.owner, previousScope = this.scope;
    this.owner = this.model; this.scope = scope; this.generation = generation;
    this.actions.bind(this.model);
    const conversations=this.actions.hidden.length ? this.model.conversations.filter((r:ResourceRow) =>
      !this.actions.hidden.includes(new ChatActionTarget(this.model,r).key)) : this.model.conversations;
    this.groups = allAssistantChatGroups(conversations, this.model.mainId, this.model.mainMachineId,
      this.model.profile.id, this.model.profile.manager, this.model.features.includes('conversation_origin'), this.history.known(scope));
    this.bindRows(previous, previousScope);
    if (this.model.connected && !this.model.demo && !this.options.showsConversationOptions(this.model)) {
      this.options.open(this.model, 'conversation_options', true).catch(() => undefined);
    }
    this.history.refresh(this.model, true).catch(() => undefined);
  }
  private bindRows(previous: ClientModel | undefined, previousScope: string): void {
    const selected = this.groups.find((group: AllChatGroup) => group.id === this.filter);
    this.rows = reconcileBoundRows(this.rows, selected?.rows || [], (r: ResourceRow) => r.key(),
      previous, this.model, previousScope, this.scope);
  }
  private choose(key: string): void {
    if (!this.groups.some((g: AllChatGroup) => g.id === key)) return;
    this.filter = key;
    this.bindRows(this.owner, this.scope);
    this.list.scrollEdge(Edge.Top);
  }
  private hasOptions(): boolean {
    return this.model.connected && !this.model.demo && this.options.showsConversationOptions(this.model) &&
      typeof this.options.assistantData['recent_task_chats'] === 'boolean';
  }
  private source(row: ResourceRow): string { return assistantChatSource(row, this.history.known(this.scope)); }
  private detail(row: ResourceRow): string {
    return allChatDetail(row, this.source(row), this.filter,
      this.model.language === 'default' ? i18n.System.getSystemLanguage() : this.model.language, this.clock, tr);
  }
  private spoken(row: ResourceRow): string {
    const src = this.source(row);
    return [sidebarConversationTitle(row,tr), this.filter !== 'all' && src ? tr(assistantChatSourceKey(src,true)) : '',
      this.detail(row)].filter((s:string) => !!s).join(tr('phone_sidebar_label_separator'));
  }
  private filterName(key: string): string {
    return tr(key === 'all' ? 'all' : key === 'mine' ? 'phone_all_chats_mine' : assistantChatSourceKey(key));
  }
  @Builder displayMenu(snapshot: AdminSnapshot) {
    Menu() {
      MenuItemGroup({header:tr('phone_sidebar_recent_heading')}) {
        MenuItem({content:tr('phone_sidebar_show_scheduled')}).selectIcon(true).selected(Wire.bool(this.options.assistantData,'recent_task_chats'))
          .enabled(!this.options.saving && !this.model.operationBlocked('/api/v1/assistant'))
          .onClick(() => {
            if (this.hasOptions() && this.options.admits(snapshot,'/api/v1/assistant')) {
              this.options.setRecentTaskChats(snapshot,!Wire.bool(this.options.assistantData,'recent_task_chats')).catch(() => undefined);
            }
          }).id('phone_search.recent_task_chats')
      }
    }
  }
  @Builder heading() {
    Stack({alignContent:Alignment.Center}) {
      Text(tr('flow_all_chats')).fontSize(Tokens.body).maxFontScale(Tokens.navigationTextMaxScale)
        .fontWeight(FontWeight.Medium).fontColor(Colors.ink).maxLines(1)
      Row() {
        Button({type:ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.xmark')).fontSize('18vp').fontColor([Colors.ink]) }
          .width(40).height(40).padding(0).backgroundColor(Colors.surface).border({width:Tokens.divider,color:Colors.divider,radius:20})
          .responseRegion({x:-2,y:-2,width:44,height:44}).accessibilityText(tr('close')).id('all_chats.close').onClick(() => this.onClose())
        Blank().layoutWeight(1)
        if (this.hasOptions()) Button({type:ButtonType.Circle}) {
          Row({space:3}) {
            ForEach([0,1,2],(dot:number) => { Circle().width(3.5).height(3.5).fill(Colors.link) },(dot:number) => String(dot))
          }
        }.width(40).height(40).padding(0).backgroundColor(Colors.surface).border({width:Tokens.divider,color:Colors.divider,radius:20})
          .responseRegion({x:-2,y:-2,width:44,height:44}).accessibilityText(tr('phone_all_chats_display_options'))
          .bindMenu(this.displayMenu(this.options.capture('/api/v1/assistant'))).id('phone_search.display_options')
      }.width('100%').padding({left:Tokens.page,right:Tokens.page})
    }.width('100%').height(54).margin({top:16})
  }
  @Builder filters() {
    Flex({wrap:FlexWrap.Wrap,alignItems:ItemAlign.Center,space:{main:LengthMetrics.vp(6),cross:LengthMetrics.vp(6)}}) {
      ForEach(this.groups,(g: AllChatGroup) => {
        Button({type:ButtonType.Capsule}) {
          Row({space:4}) {
            SymbolGlyph(assistantSourceSymbol(g.id)).fontSize(Tokens.interfaceFontVp(13,nativeEnvironment.fontScale)).fontColor([this.filter === g.id ? Colors.link : Colors.secondary])
            Text(this.filterName(g.id) + ' ' + String(g.rows.length)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
              .fontWeight(FontWeight.Medium).fontColor(this.filter === g.id ? Colors.link : Colors.secondary).maxLines(1)
          }
        }.height(Math.max(20,Math.round(16*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(13)))+4)).padding({left:8,right:8,top:1,bottom:1}).backgroundColor(this.filter === g.id ? Colors.selection : Colors.control)
          .accessibilityText(tr('phone_all_chats_filter_count').replace('{name}',this.filterName(g.id)).replace('{count}',String(g.rows.length)))
          .accessibilitySelected(this.filter === g.id).onClick(() => this.choose(g.id)).id('all_chats.filter.'+g.id)
      },(g: AllChatGroup) => g.id+':'+String(g.rows.length))
    }.width('100%').padding({left:Tokens.page,right:Tokens.page,top:19,bottom:19}).id('all_chats.filters')
  }
  @Builder chat(binding: BoundRow<ResourceRow>) {
    Row({space:Tokens.inset}) {
      Column({space:3}) {
        AssistantChatTitle({title:sidebarConversationTitle(binding.value,tr),source:this.source(binding.value),
          identity:'all_chats.mark.'+(binding.value.session || binding.value.id)}).width('100%')
        if (this.detail(binding.value)) Text(this.detail(binding.value)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
          .fontColor(Colors.secondary).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      AssistantChatStateMark({state:assistantChatState(binding.value),identity:'all_chats.mark.'+(binding.value.session || binding.value.id)})
    }.width('100%').constraintSize({minHeight:PhoneStyle.formSize(74,89,nativeEnvironment.fontScale)}).padding({left:20,right:16,top:8,bottom:8}).alignItems(VerticalAlign.Center)
      .accessibilityGroup(true).accessibilityText(this.spoken(binding.value)).onClick(() => {
        const row=binding.value;
        if (this.model.owns(this.generation,this.scope) && this.model.conversations.includes(row)) this.onOpen(row);
      }).id('all_chats.row.'+(binding.value.session || binding.value.id))
  }
  @Monitor('actions.notice') noticeChanged(): void {
    clearTimeout(this.noticeTimer);
    if (this.actions.notice) this.noticeTimer=setTimeout(() => this.actions.clearNotice(),7500);
  }
  private confirmPin(target: ChatActionTarget): void {
    const row=this.actions.row(target);
    if (!row || !this.actions.canChangeChat(target)) return;
    const lead=this.actions.pinned(target);
    const message=lead ? tr('phone_chat_unpin_message').replace('{name}',sidebarConversationTitle(row,tr))+
      (this.actions.hasFiling() ? '\n'+tr('phone_chat_unpin_filing') : '') : tr('phone_chat_pin_message');
    const title=tr(lead ? 'phone_chat_unpin_title' : 'phone_chat_pin_title');
    const confirmButton: AlertDialogButtonBaseOptions={value:tr(lead ? 'phone_chat_unpin' : 'phone_chat_pin'),
      action:() => { this.actions.pin(target,!lead).catch(() => {}); }};
    if (nativeEnvironment.fontScale<=Tokens.interfaceFontMaxScale) {
      this.getUIContext().showAlertDialog({title:title,message:message,width:250,alignment:DialogAlignment.Center,autoCancel:true,confirm:confirmButton});
    } else {
      this.getUIContext().showAlertDialog({title:title,message:message,primaryButton:{value:tr('cancel'),action:() => {}},secondaryButton:confirmButton});
    }
  }
  @Builder moveMenu(target: ChatActionTarget) {
    Menu() {
      ForEach(this.actions.choices(target),(choice:ChatDirection) => {
        MenuItem({content:choice.name || tr('phone_new_chat_title')}).onClick(() => { this.actions.file(target,choice).catch(() => {}); })
          .id('all_chats.move.'+choice.id)
      },(choice:ChatDirection) => choice.id)
      MenuItem({content:tr('phone_chat_own')}).onClick(() => { this.actions.file(target,undefined).catch(() => {}); }).id('all_chats.move.own')
    }
  }
  @Builder rowMenu(target: ChatActionTarget) {
    Menu() {
      if (this.actions.canChangeChat(target)) MenuItem({content:tr(this.actions.pinned(target) ? 'phone_chat_unpin' : 'phone_chat_pin'),
        symbolStartIcon:new SymbolGlyphModifier($r('sys.symbol.pin'))}).onClick(() => this.confirmPin(target)).id('all_chats.action.pin')
      if (this.actions.choices(target).length) MenuItem({content:tr('phone_chat_move'),builder:() => {this.moveMenu(target);}})
        .id('all_chats.action.move')
      if (this.actions.canChangeChat(target)) MenuItem({content:tr('archive'),symbolStartIcon:new SymbolGlyphModifier($r('sys.symbol.archivebox'))})
        .onClick(() => {this.actions.archive(target).catch(() => {});}).id('all_chats.action.archive')
    }.width(250).subMenuExpandingMode(SubMenuExpandingMode.EMBEDDED_EXPAND)
  }
  @Builder swipeArchive(target: ChatActionTarget) {
    Button({type:ButtonType.Normal}) {
      Column({space:4}) {
        SymbolGlyph($r('sys.symbol.archivebox')).fontSize('20vp').fontColor([Colors.onAction])
        Text(tr('archive')).fontSize(Tokens.caption).maxFontScale(Tokens.interfaceScaleFor(Tokens.caption)).fontColor(Colors.onAction)
      }
    }.width(78).height('100%').padding(0).backgroundColor(Colors.action).accessibilityText(tr('archive'))
      .onClick(() => { this.actions.archive(target).catch(() => {}); }).id('all_chats.swipe.archive')
  }
  @Builder itemBody(binding: BoundRow<ResourceRow>) {
    Column() { this.chat(binding); Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({left:20,right:16}) }.width('100%')
  }
  @Builder listItem(binding: BoundRow<ResourceRow>,target: ChatActionTarget) {
    if (this.actions.canChangeChat(target)) {
      ListItem() { this.itemBody(binding) }
        .swipeAction({end:{builder:() => {this.swipeArchive(target);},actionAreaDistance:80,onAction:() => {this.actions.archive(target).catch(() => {});}}})
        .bindContextMenu(this.rowMenu(target),ResponseType.LongPress,{preview:MenuPreviewMode.IMAGE,placement:Placement.BottomLeft})
    } else if (this.actions.choices(target).length) {
      ListItem() { this.itemBody(binding) }.bindContextMenu(this.rowMenu(target),ResponseType.LongPress,{preview:MenuPreviewMode.IMAGE,placement:Placement.BottomLeft})
    } else {
      ListItem() { this.itemBody(binding) }
    }
  }
  @Builder notice() {
    Row({space:8}) {
      Text(this.actions.notice==='from_hub' ? this.actions.detail : tr(this.actions.notice).replace('{name}',this.actions.detail)).fontSize(Tokens.meta).maxFontScale(Tokens.interfaceScaleFor(Tokens.meta))
        .fontColor(this.actions.failure ? Colors.danger : Colors.ink).flexShrink(1).id('all_chats.feedback')
      if (this.actions.undo) Button(tr('phone_chat_undo')).height(38).fontColor(Colors.link).backgroundColor(Color.Transparent)
        .onClick(() => { this.actions.undoMove().catch(() => {}); }).id('all_chats.action.undo')
    }.padding({left:16,right:16,top:9,bottom:9}).margin({left:16,right:16,bottom:16})
      .backgroundColor(Colors.surface).border({width:Tokens.divider,color:Colors.divider,radius:18}).borderRadius(18)
  }
  build() {
    Stack({alignContent:Alignment.Bottom}) {
    Column() {
      this.heading()
      List({scroller:this.list}) {
        if (this.hasOptions() && this.options.error) ListItem() {
          Text(trError(this.options.error)).fontSize(Tokens.meta).fontColor(Colors.danger).width('100%')
            .padding({left:Tokens.page,right:Tokens.page}).id('phone_search.options_error')
        }
        ListItem() { this.filters() }
        ForEach(this.rows,(binding:BoundRow<ResourceRow>) => {
          this.listItem(binding,new ChatActionTarget(this.model,binding.value))
        },(binding:BoundRow<ResourceRow>) => binding.key)
        if (!this.rows.length) ListItem() {
          Column({space:Tokens.gap}) {
            Text(tr('phone_all_chats_empty')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Text(tr('phone_all_chats_empty_detail')).fontSize(Tokens.meta).fontColor(Colors.secondary).textAlign(TextAlign.Center)
          }.width('100%').padding(Tokens.section)
        }
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
      if (this.actions.notice) this.notice()
    }.width('100%').height('100%').id('shell.all_chats')
  }
}
