import { pasteboard } from '@kit.BasicServicesKit';
import { common } from '@kit.AbilityKit';
import { i18n } from '@kit.LocalizationKit';
import { Colors } from '../design/Colors';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { tr, trError } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { PhoneChatForward, ForwardSection, forwardRoute } from '../models/PhoneChatForward';
import { ChatMessage, ResourceRow, Wire } from '../models/Wire';
import { forwardOrigin } from '../core/ChatForwardWire';
import { shareForwardImages } from '../platform/ShareForwardImages';

function selectedCount(n:number):string {return tr('chat_forward_count').replace('{1}',String(n));}
function summary(r:Record<string,Object>):string {return Wire.text(r,'text').replace(/\s+/g,' ').trim();}
function heading(refs:Record<string,Object>[]):string {
  if(refs.length<2)return refs.length?Wire.text(refs[0],'title'):'';
  const chat=Wire.text(refs[0],'chat');return tr('chat_forward_many').replace('{1}',String(refs.length))+(chat?' · '+chat:'');
}

@ComponentV2
export struct PhoneChatPickBar {
  @Require @Param source:ClientModel;
  @Require @Param state:PhoneChatForward;
  @Local working:boolean=false;
  private async copy(exportImage:boolean):Promise<void> {
    if(this.working||!this.state.isPicking(this.source)||!this.state.picked.length)return;
    this.working=true;
    const source=this.source,ids=this.state.selected(source).map(m=>m.itemId).join('\n');
    const valid=():boolean=>this.state.isPicking(source) && !this.state.choosing && this.state.selected(source).map(m=>m.itemId).join('\n')===ids;
    try{
      const done=await this.state.complete(source,exportImage,valid,true);
      if(!valid())return;
      if(exportImage)await shareForwardImages(this.getUIContext().getHostContext() as common.UIAbilityContext,done,valid);
      else await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,
        done.refs.map((r:Record<string,Object>)=>Wire.text(r,'author')+tr('chat_forward_byline')+'\n'+Wire.text(r,'text')).join('\n\n')));
      if(valid())this.state.end();
    }catch(e){if(valid())this.state.error=(e as Error).message||'failed';}finally{this.working=false;}
  }
  @Builder action(label:string,mark:Resource,id:string,run:()=>void) {
    Column({space:4}){
      SymbolGlyph(mark).fontSize(21).fontColor([this.state.picked.length ? Colors.interfaceAccent : Colors.tertiary])
      Text(label).fontSize(13).maxFontScale(1.35).maxLines(1).fontColor(this.state.picked.length ? Colors.interfaceAccent : Colors.tertiary)
    }.layoutWeight(1).height(62).justifyContent(FlexAlign.Center).accessibilityGroup(true).accessibilityText(label)
    .accessibilityRole(AccessibilityRoleType.BUTTON).enabled(!this.working && !!this.state.picked.length)
    .onClick(run).id(id)
  }
  build(){
    Column({space:8}){
      Row(){
        Row(){Button(tr('cancel')).fontColor(Colors.link).backgroundColor(Color.Transparent).onClick(()=>this.state.end()).id('chat.pickCancel')}
          .layoutWeight(1).justifyContent(FlexAlign.Start)
        Text(selectedCount(this.state.picked.length)).fontSize(13).fontColor(Colors.secondary).id('chat.pickCount')
        Row(){
          if(this.state.offersQuestions(this.source)){
          Button({type:ButtonType.Capsule}) {
            Row({space:4}) {
              SymbolGlyph(this.state.include ? $r('sys.symbol.checkmark_circle_fill') : $r('sys.symbol.circle')).fontSize(13)
                .fontColor([this.state.include ? Colors.interfaceAccent : Colors.secondary])
              Text(tr('chat_forward_questions')).fontSize(13).fontColor(this.state.include ? Colors.interfaceAccent : Colors.secondary)
            }
          }.padding({left:8,right:8}).backgroundColor(this.state.include ? Colors.accentWell : Color.Transparent)
            .border({width:this.state.include ? 0 : .5,color:Colors.divider,radius:24})
            .accessibilityText(tr('chat_forward_questions')).accessibilitySelected(this.state.include)
            .onClick(()=>this.state.setInclude(this.source,!this.state.include)).id('chat.pickQuestions')
          }
        }.layoutWeight(1).justifyContent(FlexAlign.End)
      }.width('100%')
      Row(){
        this.action(tr('chat_forward_action'),$r('sys.symbol.arrowshape_turn_up_right'),'chat.pickForward',()=>this.state.forwardPicks(this.source))
        this.action(tr('copy'),$r('sys.symbol.doc_text_on_doc_text'),'chat.pickCopy',()=>this.copy(false))
        this.action(tr('chat_forward_export'),$r('sys.symbol.picture_on_square'),'chat.pickExport',()=>this.copy(true))
      }.width('100%').backgroundColor(Colors.surface).borderRadius(14)
        .outline({width:Colors.styleID==='island' ? 0 : .5,color:Colors.styleEdge || Colors.divider,radius:14})
      if(this.state.error){Text(trError(this.state.error)).fontSize(13).fontColor(Colors.danger)}
    }.width('100%').padding({left:16,right:16,top:8,bottom:8})
  }
}

@ComponentV2
export struct PhoneChatForwardPicker {
  @Require @Param source:ClientModel;
  @Require @Param state:PhoneChatForward;
  @Event onTarget:(row:ResourceRow)=>void=()=>{};
  @Event onNewAgent:()=>void=()=>{};
  @Local editingSearch:boolean=false;
  @Local compactSheet:boolean=false;
  private measureSheet(area:Area):void {
    const window=this.getUIContext().px2vp(nativeEnvironment.windowHeightPx);
    // Sheets report positions in their own native window. Use their height only when the keyboard is absent.
    if(this.editingSearch || nativeEnvironment.keyboardHeightPx>0){this.compactSheet=false;return;}
    const height=Number(area.height);
    if(window>0 && Number.isFinite(height) && height>0)this.compactSheet=height<window*.72;
  }
  private searchField:SearchController=new SearchController();
  @Computed get refs():Record<string,Object>[] {return this.state.previews(this.source);}
  @Computed get previewMeta():string {
    if(!this.refs.length) return '';
    const ref=this.refs[0],machine=Wire.text(ref,'machine'),at=Date.parse(Wire.text(ref,'time'));
    if(this.refs.length!==1 || !Number.isFinite(at)) return machine;
    const date=new Date(at),today=new Date();
    const options:Intl.DateTimeFormatOptions={hour:'numeric',minute:'2-digit'};
    if(date.toDateString()!==today.toDateString()){options.month='short';options.day='numeric';}
    const locale=this.source.language==='default' ? i18n.System.getSystemLanguage() : this.source.language;
    const time=new Intl.DateTimeFormat(locale,options).format(date);
    return machine ? machine+' · '+time : time;
  }
  @Computed get groups():ForwardSection[] {return this.state.sections(this.source);}
  private async pick(row:ResourceRow):Promise<void>{if(await this.state.choose(this.source,row))this.onTarget(row);}
  private async newAgent():Promise<void>{if(await this.state.chooseNewAgent(this.source))this.onNewAgent();}
  private async share():Promise<void> {
    if(this.state.busy)return;
    const source=this.source,valid=():boolean=>this.state.isChoosing(source);this.state.busy=true;
    try {const done=await this.state.complete(source,true,valid,true);if(valid())await shareForwardImages(this.getUIContext().getHostContext() as common.UIAbilityContext,done,valid);}
    catch(e){if(valid())this.state.error=(e as Error).message || 'failed';}finally{if(valid())this.state.busy=false;}
  }
  @Builder option(title:string,detail:string,icon:Resource,id:string,run:()=>void) {
    Row({space:12}){
      SymbolGlyph(icon).fontSize(21).fontColor([Colors.link]).width(33)
      Column({space:2}){
        Text(title).fontSize(17).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
        if(detail){Text(detail).fontSize(13).fontColor(Colors.secondary).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})}
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
    }.width('100%').constraintSize({minHeight:detail?64:50}).padding({left:16,right:16,top:8,bottom:8})
    .backgroundColor(Colors.sheetCard).accessibilityGroup(true).accessibilityRole(AccessibilityRoleType.BUTTON)
    .onClick(run).id(id)
  }
  @Builder closeControl(size:number,label:string,id:string,run:()=>void) {
    Button({type:ButtonType.Circle}) {
      SymbolGlyph($r('sys.symbol.xmark')).fontSize(17).fontWeight(FontWeight.Medium).fontColor([Colors.ink])
    }.width(size).height(size).padding(0).backgroundColor(Colors.sheetControl)
    .accessibilityText(label).onClick(run).id(id)
  }
  @Builder target(row:ResourceRow,showMachine:boolean) {
    this.option(row.title,this.state.detail(this.source,row,showMachine),$r('sys.symbol.text_bubble_left_2'),'forward.session.'+(row.session||row.id),()=>this.pick(row))
  }
  build(){
    Column({space:8}){
      if(!this.editingSearch){
        Stack({alignContent:Alignment.Start}){
          Text(tr('chat_forward_to')).fontSize(17).fontWeight(FontWeight.Medium).fontColor(Colors.ink).width('100%').textAlign(TextAlign.Center)
          this.closeControl(40,tr('close'),'forward.cancel',()=>this.state.cancelPicker())
        }.width('100%').height(44)
      }
      Row({space:10}){
        Search({value:this.state.query,placeholder:tr('chat_forward_search'),controller:this.searchField}).height(44).layoutWeight(1)
          .onEditChange((editing:boolean)=>{if(editing){this.editingSearch=true;this.state.expanded=true;}})
          .onChange((v:string)=>{this.state.query=v;if(v)this.state.expanded=true;}).id('forward.search')
        if(this.editingSearch){
          this.closeControl(44,tr('close'),'forward.search.cancel',()=>{this.searchField.stopEditing();this.state.query='';this.editingSearch=false;})
        }
      }.width('100%')
      Scroll(){
        Column({space:24}){
          Column(){
            if(!this.state.picking){
              this.option(tr('chat_forward_share'),'', $r('sys.symbol.rectangle_and_arrowshape_turn_up_right'),'forward.shareImage',()=>this.share())
              Divider().color(Colors.divider)
              this.option(tr('chat_forward_select'),'', $r('sys.symbol.checkmark_circle'),'forward.selectMessages',()=>this.state.selectInstead(this.source))
              Divider().color(Colors.divider)
            }
            Column() {
              Row({space:7}) {
                Rect().width(3).alignSelf(ItemAlign.Stretch).fill(Colors.interfaceAccent).borderRadius(1.5)
                Column({space:3}) {
                  Row({space:4}) {
                    SymbolGlyph($r('sys.symbol.arrowshape_turn_up_right')).fontSize(13).fontColor([Colors.interfaceAccent])
                    Text(heading(this.refs)).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.interfaceAccent).maxLines(2)
                  }
                  if(this.previewMeta){Text(this.previewMeta).fontSize(12).fontColor(Colors.secondary)}
                  Text(this.refs.map((r:Record<string,Object>)=>summary(r)).join('\n')).fontSize(14).fontColor(Colors.secondary).maxLines(this.refs.length>1?3:2).textOverflow({overflow:TextOverflow.Ellipsis})
                }.layoutWeight(1).alignItems(HorizontalAlign.Start)
              }.width('100%').padding({left:8,right:12,top:6,bottom:6}).backgroundColor(Colors.accentWell).borderRadius(9)
            }.width('100%').padding({left:16,right:16,top:8,bottom:8}).backgroundColor(Colors.sheetCard).id('forward.preview')
          }.width('100%').borderRadius(14).clip(true)
          if(!this.state.query.trim()){
            Column(){
              if(this.state.main(this.source)){
                this.option('Persona',tr('main_chat'),$r('sys.symbol.text_bubble_left_2'),'forward.persona',()=>{const r=this.state.main(this.source);if(r)this.pick(r);})
                Divider().color(Colors.divider)
              }
              this.option(tr('chat_forward_new'),tr('chat_forward_new_detail'),$r('sys.symbol.plus'),'forward.newAgent',()=>this.newAgent())
            }.width('100%').borderRadius(14).clip(true)
          }
          ForEach(this.groups,(section:ForwardSection)=>{
            Column({space:6}){
              if(section.title){Text(section.title).fontSize(13).fontColor(Colors.secondary).padding({left:12})}
              Column(){
                ForEach(section.rows,(r:ResourceRow,i:number)=>{
                  if(i>0){Divider().color(Colors.divider)}
                  this.target(r,section.id==='results'||section.id==='recent')
                },(r:ResourceRow)=>r.key())
              }.width('100%').borderRadius(14).clip(true)
            }.width('100%').alignItems(HorizontalAlign.Start)
          },(section:ForwardSection)=>JSON.stringify([section.id,section.rows.map((row:ResourceRow)=>row.key())]))
          if(this.state.query.trim() && !this.groups.length){Text(tr('chat_forward_no_matches')).fontColor(Colors.secondary).id('forward.noMatch')}
          if(this.state.error){Text(trError(this.state.error)).fontColor(Colors.danger).id('forward.error')}
        }.width('100%').padding({top:this.editingSearch?35:18,bottom:24}).enabled(!this.state.busy)
      }.layoutWeight(1).scrollable(ScrollDirection.Vertical).align(Alignment.TopStart)
    }.width('100%').height('100%').padding({left:this.compactSheet&&!this.editingSearch?24:16,right:this.compactSheet&&!this.editingSearch?24:16,top:this.editingSearch?16:8})
      .backgroundColor(Colors.sheetBackground).onAreaChange((_old:Area,area:Area)=>this.measureSheet(area)).id('forward.sheet')
  }
}

@ComponentV2
export struct PhoneChatForwardTray {
  @Require @Param model:ClientModel;
  @Computed get groups():Record<string,Object>[][] {return this.model.forwardDraftGroups();}
  @Builder card(refs:Record<string,Object>[],group:number) {
    Column({space:4}){
      Text(heading(refs)).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.ink).id('forward.draft.title')
      ForEach(refs,(r:Record<string,Object>,i:number)=>{
        Row({space:8}){
          Text((refs.length>1?Wire.text(r,'author')+tr('chat_forward_byline')+' ':'')+summary(r))
            .fontSize(13).fontColor(Colors.secondary).maxLines(refs.length>1?1:2).textOverflow({overflow:TextOverflow.Ellipsis}).layoutWeight(1)
          Button({type:ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.xmark')).fontSize(14).fontColor([Colors.secondary]) }
            .width(34).height(34).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('chat_forward_remove')).onClick(()=>this.model.removeForward(Wire.text(r,'msg'),Wire.text(r,'url'))).id('forward.draft.remove.'+group+'.'+i)
        }.width('100%')
      },(r:Record<string,Object>,i:number)=>Wire.text(r,'url')+':'+i)
      Text(forwardRoute(this.model,refs[0])).fontSize(13).fontColor(Colors.secondary).maxLines(2).id('composer.forwardRoute')
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left:12,right:8,top:6,bottom:4}).backgroundColor(Colors.sheetCard).borderRadius(12)
  }
  build(){
    if(this.groups.length){
      Column({space:4}){
        ForEach(this.groups,(group:Record<string,Object>[],index:number)=>{this.card(group,index)},(group:Record<string,Object>[],i:number)=>JSON.stringify([i,group.map((ref:Record<string,Object>)=>[Wire.text(ref,'url'),Wire.text(ref,'msg'),Wire.text(ref,'local_transfer')])]))
      }.width('100%').id('forward.draft')
    }
  }
}

/** The quoted source is resolved in the receiving model's paired directory before a chat or item is opened. */
@ComponentV2
export struct PhoneForwardSentCard {
  @Require @Param model:ClientModel;
  @Require @Param message:ChatMessage;
  @Event onOpen:(row:ResourceRow,item:string)=>void=()=>{};
  @Computed get groups():Record<string,Object>[][] {return this.model.sentForwardGroups(this.message);}
  private open(ref:Record<string,Object>):void {
    const row=this.model.forwardedOrigin(ref),link=forwardOrigin(ref);
    if(row && link)this.onOpen(row,link.item);
  }
  @Builder card(refs:Record<string,Object>[],g:number) {
      Column({space:4}){
        Text(heading(refs)).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        ForEach(refs,(r:Record<string,Object>,i:number)=>{
          Column({space:2}){
            Text([Wire.text(r,'author'),Wire.text(r,'machine')].filter((s:string)=>!!s).join(' · ')).fontSize(12).fontColor(Colors.secondary)
            Text(summary(r)).fontSize(14).fontColor(Colors.ink).maxLines(3).textOverflow({overflow:TextOverflow.Ellipsis})
          }.alignItems(HorizontalAlign.Start).width('100%').padding({top:4,bottom:4}).accessibilityGroup(true)
          .accessibilityRole(AccessibilityRoleType.BUTTON).onClick(()=>this.open(r)).id('forward.sent.'+g+'.'+i)
        },(r:Record<string,Object>,i:number)=>Wire.text(r,'url')+':'+i)
      }.width('100%').alignItems(HorizontalAlign.Start).padding(10).borderRadius(10).backgroundColor(Colors.sheetCard)
  }
  build(){
    if(this.groups.length){
      Column({space:4}){
        ForEach(this.groups,(group:Record<string,Object>[],i:number)=>{this.card(group,i)},(group:Record<string,Object>[],i:number)=>JSON.stringify([i,group.map((ref:Record<string,Object>)=>[Wire.text(ref,'url'),Wire.text(ref,'msg'),Wire.text(ref,'local_transfer')])]))
      }.width('100%')
    }
  }
}

@ComponentV2
export struct PhoneForwardNewTray {
  @Require @Param model:ClientModel;
  @Require @Param state:PhoneChatForward;
  build(){
    if(this.state.newFor(this.model)){
      Column({space:4}){
        Text(heading(this.state.newFor(this.model)!.refs)).fontSize(14).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        Text(this.state.newFor(this.model)!.refs.map((r:Record<string,Object>)=>summary(r)).join('\n'))
          .fontSize(13).fontColor(Colors.secondary).maxLines(3).textOverflow({overflow:TextOverflow.Ellipsis})
        if(this.state.newFor(this.model)!.images.length){
          Row(){
            SymbolGlyph($r('sys.symbol.picture_on_square')).fontSize(18).fontColor([Colors.link])
            Text(String(this.state.newFor(this.model)!.images.length)).fontColor(Colors.secondary)
            Blank()
            Button(tr('remove')).fontSize(13).fontColor(Colors.link).backgroundColor(Color.Transparent)
              .onClick(()=>this.state.removeNewPhotos(this.model)).id('forward.new.removePhotos')
          }.width('100%')
        }
      }.width('100%').alignItems(HorizontalAlign.Start).padding(12).borderRadius(12).backgroundColor(Colors.sheetCard).id('forward.new.preview')
    }
  }
}
