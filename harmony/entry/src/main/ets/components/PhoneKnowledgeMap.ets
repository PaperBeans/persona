import { pasteboard } from '@kit.BasicServicesKit';
import { i18n } from '@kit.LocalizationKit';
import { ClientModel } from '../models/ClientModel';
import { KnowledgeMapData, PhoneKnowledgeSelectionModel } from '../models/PhoneKnowledgeSelection';
import { ResourceRow } from '../models/Wire';
import { flattenMap, initialMapExpansion, KnowledgeMapLine, KnowledgeMapNode, KnowledgeMapStat,
  knowledgeMapStats, formatMapDay, MapFields, mapText, pointSources, projectOutline, topicOutline } from '../core/KnowledgeMapOutline';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';

class MapScene {
  roots:KnowledgeMapNode[]=[];
  expanded:Set<string>=new Set<string>();
  stats:KnowledgeMapStat[]=[];
  failed:number=0;
  curating:boolean|undefined=undefined;
}

@ComponentV2
export struct PhoneKnowledgeMap {
  @Require @Param model:ClientModel;
  @Event onEntry:(row:ResourceRow)=>void=()=>{};
  @Event onSelection:()=>void=()=>{};
  @Local mode:string='topics';
  @Local lines:KnowledgeMapLine[]=[];
  @Local stats:KnowledgeMapStat[]=[];
  @Local busy:boolean=true;
  @Local problem:boolean=false;
  @Local failed:number=0;
  @Local curating:boolean|undefined=undefined;
  @Local point:KnowledgeMapNode|undefined=undefined;
  @Local pointShown:boolean=false;
  private reader:PhoneKnowledgeSelectionModel=new PhoneKnowledgeSelectionModel();
  private scenes:Map<string,MapScene>=new Map<string,MapScene>();
  private revision:number=0;
  private epoch:number=0;
  private scope:string='';
  private generation:number=0;

  aboutToAppear():void { this.begin(); }
  aboutToDisappear():void { this.epoch++;this.revision++;this.reader.close(); }
  @Monitor('model.profile','model.connected') ownerChanged():void { this.begin(); }

  private begin():void {
    const epoch=++this.epoch;
    this.revision++;this.scenes.clear();this.lines=[];this.stats=[];this.mode='topics';this.point=undefined;this.pointShown=false;
    this.scope=this.model.scope();this.generation=this.model.connectionGeneration();
    this.reader.open(this.model,true).then(()=>{
      if (epoch===this.epoch) this.selectMode('topics');
    });
  }
  private live():boolean { return this.model.owns(this.generation,this.scope); }
  private display(scene:MapScene):void {
    this.lines=flattenMap(scene.roots,scene.expanded);this.stats=scene.stats;this.failed=scene.failed;this.curating=scene.curating;
  }
  private async selectMode(mode:string,refresh:boolean=false):Promise<void> {
    const rev=++this.revision;this.mode=mode;this.problem=false;this.failed=0;this.curating=undefined;
    const cached=this.scenes.get(mode);
    if (cached && !refresh) { this.busy=false;this.display(cached);return; }
    this.busy=true;this.lines=[];this.stats=[];
    try {
      const data:KnowledgeMapData|undefined=await this.reader.readOutline(mode);
      if (rev!==this.revision) return;
      if (!data || !this.live()) { this.problem=true;return; }
      const scene=new MapScene();
      scene.roots=mode==='topics' ? topicOutline(data.items,data.details,(k:string)=>tr(k)) :
        projectOutline(data.items,data.details,(k:string)=>tr(k));
      scene.expanded=initialMapExpansion(scene.roots);scene.failed=data.failed;scene.curating=data.curating;
      scene.stats=knowledgeMapStats(mode,scene.roots,tr('map_summary'));
      this.scenes.set(mode,scene);this.display(scene);
    } catch (_) { if (rev===this.revision) this.problem=true; }
    finally { if (rev===this.revision) this.busy=false; }
  }
  private toggle(n:KnowledgeMapNode):void {
    const scene=this.scenes.get(this.mode);
    if (!scene || !this.live() || !n.children.length) return;
    if (scene.expanded.has(n.id)) scene.expanded.delete(n.id);
    else scene.expanded.add(n.id);
    this.display(scene);
  }
  private expanded(id:string):boolean { return this.scenes.get(this.mode)?.expanded.has(id) || false; }
  private openEntry(kind:string,id:string,title:string):void {
    if (!kind || !id || !this.live()) return;
    const row=new ResourceRow(id,title,'knowledge');
    row.path=kind;row.manager=this.model.profile.manager;row.group=this.model.profile.id;
    this.onEntry(row);
  }
  private choose(n:KnowledgeMapNode):void {
    if (!this.live()) return;
    if (n.point) { this.point=n;this.pointShown=true; }
    else if (n.targetKind) this.openEntry(n.targetKind,n.targetID,n.title);
    else this.toggle(n);
  }
  private font(points:number):string { return Tokens.interfaceFontVp(points,nativeEnvironment.fontScale); }
  private tone(n:KnowledgeMapNode):ResourceColor { return n.tone==='green' ? Colors.positive : n.tone==='orange' ? Colors.workingMark : Colors.link; }
  private rowMin():number { return 32+20*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)); }
  private pointKind(point:MapFields):string {
    const kind=mapText(point,'kind');
    return tr('map_'+(['concept','decision','practice','pitfall','fact','question'].includes(kind) ? kind : 'point'));
  }
  @Builder modes() {
    Row({space:8}) {
      ForEach(['topics','projects'],(m:string)=>{
        Button({type:ButtonType.Capsule}) {
          Row({space:5}) {
            SymbolGlyph(m==='topics' ? $r('sys.symbol.lightbulb') : $r('sys.symbol.folder')).fontSize(this.font(15)).fontColor([this.mode===m ? Color.White : Colors.ink])
            Text(tr(m==='topics' ? 'map_topics' : 'projects')).fontSize(this.font(15)).fontWeight(FontWeight.Medium).fontColor(this.mode===m ? Color.White : Colors.ink)
          }.padding({left:12,right:12,top:5,bottom:5})
        }.padding(0).backgroundColor(this.mode===m ? Colors.link : Colors.surface).accessibilitySelected(this.mode===m)
          .accessibilityText(tr(m==='topics' ? 'map_topics' : 'projects')).id('settings.knowledge.map.'+m)
          .onClick(()=>{if(this.mode!==m)this.selectMode(m);})
      },(m:string)=>m)
    }.width('100%')
  }
  @Builder numbers() {
    Column() {
      Text(tr(this.mode==='topics' ? 'map_knowledge' : 'projects')+' · '+tr(this.mode==='topics' ? 'map_by_topic' : 'map_by_status'))
        .fontSize(this.font(15)).fontColor(Colors.secondary).width('100%').padding({left:20,top:17,bottom:10})
      Row() {
        ForEach(this.stats,(item:KnowledgeMapStat)=>{
          Column({space:3}) {
            Text(String(item.count)).fontSize(this.font(22)).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
            Text(tr(item.key)).fontSize(this.font(12)).fontColor(Colors.secondary).textAlign(TextAlign.Center)
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        },(item:KnowledgeMapStat)=>item.key)
      }.width('100%').constraintSize({minHeight:74}).padding({left:20,right:8,top:8,bottom:8}).backgroundColor(Colors.surface).borderRadius(24)
        .id('knowledge.map.stats')
    }.width('100%').alignItems(HorizontalAlign.Start)
  }
  @Builder outlineRow(line:KnowledgeMapLine) {
    Column() {
      Row() {
        Button({type:ButtonType.Normal}) {
          Row() {
            ForEach([0,1,2,3,4,5,6].slice(0,line.depth),(i:number)=>{
              Row() { Rect().width(2).height(this.rowMin()-2).fill(this.tone(line.node)).opacity(0.3) }
                .width(16).justifyContent(FlexAlign.Center)
            },(i:number)=>String(i))
            Row({space:8}) {
              if (!line.depth) Circle().width(9).height(9).fill(this.tone(line.node))
              if (line.node.point) Text(this.pointKind(line.node.point)).fontSize(this.font(11)).fontColor(Colors.secondary)
                .backgroundColor(Colors.grouped).borderRadius(8).padding({left:5,right:5,top:1,bottom:1})
              Text(line.node.title).fontSize(this.font(line.depth===0 ? 17 : line.node.point ? 15 : 17))
                .fontWeight(line.depth===0 ? FontWeight.Bold : FontWeight.Regular).fontColor(Colors.ink).layoutWeight(1)
                .maxLines(line.node.point ? 4 : 2).textOverflow({overflow:TextOverflow.Ellipsis})
              if (line.node.meta) Text(line.node.meta).fontSize(this.font(13)).fontColor(Colors.secondary).textAlign(TextAlign.End)
            }.layoutWeight(1).padding({top:8,bottom:8})
          }.width('100%').padding({left:16,right:4})
        }.layoutWeight(1).constraintSize({minHeight:this.rowMin()}).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(line.node.title+(line.node.meta ? '，'+line.node.meta : ''))
          .onClick(()=>this.choose(line.node)).id('knowledge.map.row.'+line.node.id)
        if (line.node.children.length) Button({type:ButtonType.Normal}) {
          SymbolGlyph(this.expanded(line.node.id) ? $r('sys.symbol.chevron_down') : $r('sys.symbol.chevron_right'))
            .fontSize(this.font(13)).fontColor([Colors.secondary])
        }.width(44).constraintSize({minHeight:this.rowMin()}).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(line.node.title+'，'+tr(this.expanded(line.node.id) ? 'control_expanded' : 'control_collapsed'))
          .onClick(()=>this.toggle(line.node)).id('knowledge.map.fold.'+line.node.id)
      }.width('100%')
      if (!line.depth) Divider().strokeWidth(0.5).color(Colors.divider).margin({left:20,right:20})
    }.width('100%').backgroundColor(Colors.surface)
  }
  @Builder emptyMap() {
    Column({space:8}) {
      if (this.problem) Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.selectMode(this.mode,true))
      else {
        Text(tr(this.mode==='projects' ? 'map_projects_none' : this.curating===false ? 'map_topics_off' : 'map_topics_none'))
          .fontSize(this.font(20)).fontWeight(FontWeight.Bold).fontColor(Colors.ink).textAlign(TextAlign.Center)
        Text(tr(this.mode==='projects' ? 'map_projects_none_info' : this.curating===false ? 'map_topics_off_info' : 'map_topics_none_info'))
          .fontSize(this.font(15)).fontColor(Colors.secondary).textAlign(TextAlign.Center)
        if (this.mode==='topics' && this.curating===false)
          Button(tr('map_open_selection'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>{if(this.live())this.onSelection();})
      }
    }.width('100%').padding(16)
  }
  private confirmedDay(point:MapFields):string {
    const day=mapText(point,'when');
    if (!day) return '';
    const date=new Date(day+'T12:00:00');
    if (!Number.isFinite(date.getTime())) return day;
    const locale=this.model.language==='default' ? i18n.System.getSystemLanguage() : this.model.language;
    return tr('map_confirmed').replace('%s',formatMapDay(date,locale));
  }
  private async copyPoint():Promise<void> {
    const value=this.point?.title;
    if (!value || !this.live()) return;
    try { await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN,value)); }
    catch (error) { if (this.live()) this.model.reportError(error as Error); }
  }
  @Builder pill(text:string) {
    Text(text).fontSize(this.font(12)).fontColor(Colors.secondary).backgroundColor(Colors.grouped)
      .borderRadius(8).padding({left:6,right:6,top:2,bottom:2})
  }
  @Builder sheetLabel(text:string) {
    Text(text).fontSize(this.font(15)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary)
      .width('100%').padding({left:16,right:16,top:21,bottom:8})
  }
  @Builder pointSheet() {
    Column() {
      Row() {
        Button({type:ButtonType.Circle}) {
          SymbolGlyph($r('sys.symbol.xmark')).fontSize(this.font(21)).fontColor([Colors.ink])
        }.width(44).height(44).padding(0).backgroundColor(Colors.surface).accessibilityText(tr('close'))
          .onClick(()=>this.pointShown=false).id('knowledge.map.point.close')
        Text(tr('map_point_title')).layoutWeight(1).fontSize(this.font(17)).fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink).textAlign(TextAlign.Center)
        Blank().width(44)
      }.width('100%').padding({left:20,right:20})
      if (this.point?.point) Scroll() { Column() {
        Column({space:6}) {
          Row({space:6}) {
            this.pill(this.pointKind(this.point.point))
            if (mapText(this.point.point,'when')) this.pill(this.confirmedDay(this.point.point))
          }
          Text(this.point.title).fontSize(20).fontWeight(FontWeight.Bold).fontColor(Colors.ink).copyOption(CopyOptions.LocalDevice)
          if (this.point.crumbs.length) Text(this.point.crumbs.join(' › ')).fontSize(this.font(13)).fontColor(Colors.secondary)
        }.width('100%').alignItems(HorizontalAlign.Start).padding({top:56,left:18,right:18,bottom:23})
        if (mapText(this.point.point,'why')) {
          this.sheetLabel(tr(mapText(this.point.point,'kind')==='pitfall' ? 'map_cause' : 'map_why'))
          Text(mapText(this.point.point,'why')).width('100%').padding(18).fontSize(17).fontColor(Colors.ink)
            .backgroundColor(Colors.surface).borderRadius(20).copyOption(CopyOptions.LocalDevice)
        }
        if (pointSources(this.point).length) {
          this.sheetLabel(tr('map_from'))
          Column() { ForEach(pointSources(this.point),(link:MapFields)=>{
            Button({type:ButtonType.Normal}) {
              Row({space:10}) {
                SymbolGlyph($r('sys.symbol.square_stack_3d')).fontSize(this.font(17)).fontColor([Colors.link])
                Column({space:2}) {
                  Text(mapText(link,'name')).fontSize(this.font(16)).fontColor(Colors.ink)
                  if (mapText(link,'one_line')) Text(mapText(link,'one_line')).fontSize(this.font(12)).fontColor(Colors.secondary).maxLines(2)
                }.alignItems(HorizontalAlign.Start).layoutWeight(1)
                SymbolGlyph($r('sys.symbol.chevron_right')).fontSize(this.font(13)).fontColor([Colors.secondary])
              }.width('100%').padding(14)
            }.width('100%').padding(0).backgroundColor(Colors.surface).onClick(()=>{
              this.pointShown=false;this.openEntry(mapText(link,'kind'),mapText(link,'id'),mapText(link,'name'));
            }).id('knowledge.map.source.'+mapText(link,'kind')+'.'+mapText(link,'id'))
          },(link:MapFields)=>mapText(link,'kind')+'/'+mapText(link,'id')) }.width('100%').borderRadius(20).clip(true)
        }
        Button(tr('copy'),{buttonStyle:ButtonStyleMode.TEXTUAL}).width('100%').margin({top:20}).onClick(()=>this.copyPoint()).id('knowledge.map.point.copy')
      }.width('100%').padding({left:24,right:24,bottom:24}).alignItems(HorizontalAlign.Start) }
        .width('100%').layoutWeight(1).align(Alignment.TopStart)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('knowledge.map.point')
  }
  build() {
    List({space:0}) {
      ListItem() { this.modes() }.padding({top:49,bottom:28})
      if (this.busy) ListItem() { LoadingProgress().width(28).height(28) }
      else if (this.lines.length) {
        ListItem() { this.numbers() }.padding({bottom:35})
        ListItemGroup() {
          ForEach(this.lines,(item:KnowledgeMapLine)=>{
            ListItem() { this.outlineRow(item) }
          },(item:KnowledgeMapLine)=>item.node.id+'|'+this.expanded(item.node.id))
        }.backgroundColor(Colors.surface).borderRadius(24).clip(true)
        if (this.failed) ListItem() { Text(tr('map_partial').replace('%s',String(this.failed))).fontSize(this.font(13)).fontColor(Colors.secondary) }
      } else ListItem() { this.emptyMap() }
    }.height('100%').width('100%').padding({left:16,right:16,bottom:16}).backgroundColor(Colors.grouped).id('knowledge.map.page')
      .bindSheet($$this.pointShown,this.pointSheet(),{height:'55%',detents:['55%',SheetSize.LARGE],
        showClose:false,onDisappear:()=>{this.point=undefined;}})
  }
}
