import { FrameCallback } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { SessionSpace, SessionSpaceTab } from '../models/SessionSpace';
import { ClientModel } from '../models/ClientModel';
import { SessionHeaderPermissions, headerPermissionRow } from '../models/SessionHeaderPermissions';
import { SessionHeaderPermissionChip } from './SessionHeaderPermissionChip';
import { nativeEnvironment } from '../platform/NativeEnvironment';

class TabLayoutFrame extends FrameCallback {
  private action:() => void;
  constructor(action:() => void) { super(); this.action = action; }
  onFrame(_time:number):void { this.action(); }
}

@ComponentV2
export struct SessionSpaceTabs {
  @Require @Param space:SessionSpace;
  @Require @Param model:ClientModel;
  @Local pickerShown:boolean=false;
  @Event onSheetChange:(shown:boolean) => void=() => {};
  @Local private permissionOpen:boolean=false;
  @Monitor('pickerShown','permissionOpen') pickerPresentation():void { this.onSheetChange(this.pickerShown || this.permissionOpen); }
  private pickerOwner:string='';
  @Computed get permission():SessionHeaderPermissions {
    return new SessionHeaderPermissions(headerPermissionRow(this.model.selected,this.model.sessions,this.model.profile.id,this.model.profile.manager));
  }
  @Computed get waiting():boolean {
    return this.space.tabs.some((tab:SessionSpaceTab)=>tab.id!==this.space.current && (tab.state==='needs_you' || tab.state==='blocked'));
  }
  // The 44vp touch target extends beyond the type-scaled 30vp capsule equally on both sides.
  private overhang():number { return Math.max(0,(44-30*Math.min(nativeEnvironment.fontScale,1.32))/2); }
  private stamp():string { return JSON.stringify([this.model.scope(),this.model.connectionGeneration(),this.space.key,this.space.current]); }
  private pickTab(id:string):void {
    if (!this.pickerShown || this.stamp()!==this.pickerOwner || !this.space.tabs.some((tab:SessionSpaceTab)=>tab.id===id)) return;
    this.pickerShown=false;
    if (id!==this.space.current) this.onPick(this.space.key,this.space.current,id);
  }
  @Event onPick:(key:string,from:string,id:string) => void = () => {};
  @Event onOpenSpace:(key:string) => void = () => {};
  private scroller:Scroller = new Scroller();
  private centered:string = '';
  private active:boolean = false;
  aboutToAppear():void { this.active = true; }
  aboutToDisappear():void { this.active = false; this.centered = ''; this.pickerShown=false; }
  @Monitor('space','model.profile','model.connected') dataChanged():void {
    if (this.pickerShown && this.stamp()!==this.pickerOwner) this.pickerShown=false;
    this.centerCurrent();
  }
  private centerCurrent():void {
    if (!this.active) return;
    const index = this.space.tabs.findIndex((tab:SessionSpaceTab) => tab.id === this.space.current);
    const signature = JSON.stringify([this.space.key,this.space.current,index,nativeEnvironment.fontScale,nativeEnvironment.windowWidthPx]);
    if (index < 0 || this.centered === signature) return;
    this.centered = signature;
    this.getUIContext().postFrameCallback(new TabLayoutFrame(() => {
      if (this.active && this.centered === signature) this.scroller.scrollToIndex(index,false,ScrollAlign.CENTER);
    }));
  }
  private ink(tab:SessionSpaceTab):ResourceColor {
    if (tab.state === 'needs_you' || tab.state === 'blocked') return Colors.needs;
    if (tab.state === 'working') return Colors.working;
    if (tab.state === 'unread' || tab.state === 'done') return Colors.unread;
    return Colors.ink;
  }
  private fill(tab:SessionSpaceTab):ResourceColor {
    if (tab.state === 'needs_you' || tab.state === 'blocked') return nativeEnvironment.dark ? '#44318AFF' : '#22318AFF';
    if (tab.state === 'working') return nativeEnvironment.dark ? '#49F49B37' : '#24F49B37';
    if (tab.state === 'unread' || tab.state === 'done') return nativeEnvironment.dark ? '#4148B78C' : '#2248B78C';
    return Colors.control;
  }
  @Builder chip(tab:SessionSpaceTab,spaceKey:string,from:string) {
    Button({type:ButtonType.Capsule}) {
      Row({space:4}) {
        Text(tab.label).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(tab.id === from ? FontWeight.Bold : FontWeight.Medium)
          .fontColor(this.ink(tab)).maxLines(1).constraintSize({maxWidth:nativeEnvironment.fontScale >= 1.5 ? 204 : 152})
          .textOverflow({overflow:TextOverflow.Ellipsis})
        if (tab.starter) Text(tr('session_space_starter')).fontSize(11).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary).maxLines(1)
      }.padding({left:10,right:10,top:4,bottom:4}).constraintSize({minHeight:30}).backgroundColor(this.fill(tab))
        .border({width:tab.id === from ? 1.3 : 0,color:this.ink(tab),radius:20}).borderRadius(20)
    }.height(44).constraintSize({minWidth:44}).padding(0).backgroundColor(Color.Transparent)
      .accessibilityText(tab.label+' · '+(tab.row.title || tab.id)).accessibilitySelected(tab.id === from)
      .onClick(() => { if (tab.id !== from) this.onPick(spaceKey,from,tab.id); }).id('session.sibling.'+tab.id)
  }
  @Builder picker() {
    Column() {
      Row() {
        Text(tr('project_agent_count').replace('{count}',String(this.space.tabs.length)))
          .fontSize(20).maxFontScale(Tokens.interfaceScaleFor(20)).fontWeight(FontWeight.Bold).fontColor(Colors.ink).layoutWeight(1)
        Button({type:ButtonType.Circle}) { SymbolGlyph($r('sys.symbol.xmark')).fontSize('14vp').fontColor([Colors.secondary]) }
          .width(44).height(44).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('close'))
          .onClick(()=>{this.pickerShown=false;}).id('session.siblings.close')
      }.width('100%').padding({left:16,right:12})
      List() {
        if (this.space.key && this.space.title) ListItem() {
          Button({type:ButtonType.Normal}) {
            Row({space:12}) {
              SymbolGlyph(Symbols.folder).fontSize('17vp').fontColor([Colors.link])
              Text(tr('header_space_open').replace('{space}',this.space.title)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15))
                .fontColor(Colors.ink).layoutWeight(1).textAlign(TextAlign.Start)
              SymbolGlyph(Symbols.expand).fontSize('13vp').fontColor([Colors.secondary])
            }.width('100%').constraintSize({minHeight:46}).padding({left:16,right:16})
          }.width('100%').padding(0).backgroundColor(Color.Transparent).onClick(()=>{
            if (!this.pickerShown || this.stamp()!==this.pickerOwner) return;
            this.pickerShown=false;this.onOpenSpace(this.space.key);
          }).id('session.space.open')
        }
        ForEach(this.space.tabs,(tab:SessionSpaceTab)=>{
          ListItem() {
            Button({type:ButtonType.Normal}) {
              Row({space:10}) {
                Circle().width(8).height(8).fill(this.ink(tab))
                Column({space:3}) {
                  Text(tab.label).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
                  if (tab.row.title && tab.row.title!==tab.label) Text(tab.row.title).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary)
                }.layoutWeight(1).alignItems(HorizontalAlign.Start)
                if (tab.id===this.space.current) SymbolGlyph(Symbols.check).fontSize('15vp').fontColor([Colors.link])
              }.width('100%').constraintSize({minHeight:54}).padding({left:16,right:16,top:6,bottom:6})
            }.width('100%').padding(0).backgroundColor(Color.Transparent).accessibilitySelected(tab.id===this.space.current)
              .onClick(()=>this.pickTab(tab.id)).id('session.siblings.pick.'+tab.id)
          }
        },(tab:SessionSpaceTab)=>JSON.stringify([tab.id,tab.label,tab.state,tab.row.title,this.space.current]))
      }.width('100%').layoutWeight(1).divider({strokeWidth:0.5,color:Colors.divider,startMargin:16})
    }.width('100%').height('100%').backgroundColor(Colors.surface).padding({top:12}).id('session.siblings.picker')
  }
  build() {
    if (this.space.tabs.length || this.space.key || this.permission.visible) Row({space:12}) {
      if (this.space.tabs.length) {
        List({space:6,scroller:this.scroller}) {
          ForEach(this.space.tabs,(tab:SessionSpaceTab) => {
            ListItem() { this.chip(tab,this.space.key,this.space.current) }
          },(tab:SessionSpaceTab) => JSON.stringify([this.space.key,this.space.current,tab.id,tab.label,tab.row.title,tab.state,tab.starter]))
        }.listDirection(Axis.Horizontal).scrollBar(BarState.Off).edgeEffect(EdgeEffect.None).layoutWeight(1)
          .height(44).onAreaChange(() => this.centerCurrent()).id('session.siblings')
        Button({type:ButtonType.Capsule}) {
          Stack({alignContent:Alignment.TopEnd}) {
            Row({space:4}) {
              SymbolGlyph($r('sys.symbol.square_stack_3d')).fontSize(Tokens.interfaceFontVp(14,nativeEnvironment.fontScale)).fontColor([Colors.secondary])
              Text(String(this.space.tabs.length)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            }.padding({left:8*Math.min(nativeEnvironment.fontScale,1.16),right:8*Math.min(nativeEnvironment.fontScale,1.16)}).height(30*Math.min(nativeEnvironment.fontScale,1.32)).backgroundColor(Colors.control).borderRadius(22)
            if (this.waiting) {
              Circle().width(6).height(6).fill(Colors.needs)
            }
          }
        }.height(44).constraintSize({minWidth:44}).padding(0).backgroundColor(Color.Transparent).flexShrink(0)
          .accessibilityText(tr(this.waiting ? 'header_space_needs' : this.space.tabs.length===1 ? 'header_space_one' : 'header_space_count').replace('{count}',String(this.space.tabs.length)))
          .onClick(()=>{this.pickerOwner=this.stamp();this.pickerShown=true;}).id('session.siblings.all')
          .bindSheet($$this.pickerShown,this.picker(),{height:SheetSize.MEDIUM,detents:[SheetSize.MEDIUM,SheetSize.LARGE],dragBar:true,showClose:false})
      } else {
        Blank().layoutWeight(1)
        if (this.space.key) Button({type:ButtonType.Circle}) {
          SymbolGlyph(Symbols.folder).fontSize('15vp').fontColor([Colors.secondary])
        }.width(44).height(44).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr('phone_session_workspace').replace('{space}',this.space.title))
          .onClick(()=>this.onOpenSpace(this.space.key)).id('session.space.open')
      }
      if (this.permission.visible) SessionHeaderPermissionChip({model:this.model,permission:this.permission,onMenuChange:(open:boolean) => { this.permissionOpen=open; }}).flexShrink(0)
    }.width('100%').padding({left:16,right:16}).margin({top:-this.overhang(),bottom:8-this.overhang()})
  }
}
