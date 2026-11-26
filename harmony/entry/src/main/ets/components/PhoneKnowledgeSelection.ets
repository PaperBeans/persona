import { ClientModel } from '../models/ClientModel';
import { Wire } from '../models/Wire';
import { PhoneKnowledgeSelectionModel, selectionItemKey, selectionItemMode, selectionNumber } from '../models/PhoneKnowledgeSelection';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneStyle } from '../design/PhoneStyle';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';

type Fields=Record<string,Object>;

@ComponentV2
export struct PhoneKnowledgeSelection {
  @Require @Param model:ClientModel;
  @Local form:PhoneKnowledgeSelectionModel=new PhoneKnowledgeSelectionModel();
  @Local shown:boolean=false;
  @Local modal:string='';
  @Local part:string='messages';
  @Local ruleName:string='';
  @Local kind:string='chat';
  @Local opened:Fields={};
  @Local originalItem:Fields|undefined=undefined;
  @Local ruleOn:boolean=false;
  @Local days:string='';
  @Local attachmentDays:string='';
  @Local context:string='';
  @Local includeAll:boolean=true;
  @Local mode:string='rules';
  @Local fromPicker:boolean=false;
  @Local query:string='';
  @Local removed:Fields|undefined=undefined;
  @Local expanded:string='';
  @Local point:Fields={};
  private view:number=0;

  aboutToAppear():void { this.begin(); }
  aboutToDisappear():void { this.view++; this.form.close(); }
  @Monitor('model.profile','model.connected') ownerChanged():void { this.begin(); }
  private begin():void {
    this.view++; this.shown=false; this.modal=''; this.removed=undefined; this.query=''; this.originalItem=undefined;
    this.form.open(this.model);
  }

  private title(name:string):string { return tr('ks_rule_'+name); }
  private detail(part:string,name:string):string { return tr('ks_detail_'+(part==='docs' && name==='mentions' ? 'doc_mentions' : name)); }
  private optionTitle(mode:string,kind:string):string { return tr(mode==='rules' ? 'ks_rules_'+kind : 'ks_'+mode); }
  private numberText(value:Fields,key:string):string { return typeof value[key]==='number' ? String(value[key]) : ''; }
  private daysText(days:number):string { return tr('ks_day_count').replace('{days}',String(days)); }
  private ruleValue(rule:Fields):string { return !Wire.bool(rule,'on') ? tr('ks_off') : Wire.number(rule,'days') ? this.daysText(Wire.number(rule,'days')) : tr('ks_on'); }
  private itemValue(item:Fields):string {
    const mode=selectionItemMode(item);
    if (mode==='all' && Wire.number(item,'days')) return this.daysText(Wire.number(item,'days'));
    return tr(mode==='skip' ? 'ks_not_read' : mode==='all' ? 'ks_everything' : Wire.text(item,'kind')==='doc' ? 'ks_by_rules' : 'ks_personal');
  }

  private editRule(part:string,name:string):void {
    this.view++; this.part=part; this.ruleName=name; this.opened=Wire.parse(JSON.stringify(this.form.rule(part,name)));
    this.ruleOn=Wire.bool(this.opened,'on'); this.days=this.numberText(this.opened,'days');
    this.attachmentDays=this.numberText(this.opened,'attachment_days'); this.context=this.numberText(this.opened,'context');
    this.includeAll=Wire.bool(this.opened,'include_all',true); this.form.problem=''; this.fromPicker=false; this.modal='rule'; this.shown=true;
  }
  private editItem(item:Fields,fromPicker:boolean):void {
    this.view++; this.kind=Wire.text(item,'kind'); this.fromPicker=fromPicker; this.opened=Wire.parse(JSON.stringify(item));
    const saved=this.form.items().find((row:Fields) => selectionItemKey(row)===selectionItemKey(item));
    this.originalItem=saved ? Wire.parse(JSON.stringify(saved)) : undefined;
    this.mode=selectionItemMode(item); this.days=this.numberText(item,'days'); this.form.problem=''; this.modal='item'; this.shown=true;
  }
  private pick(kind:string):void {
    this.view++; this.kind=kind; this.fromPicker=false; this.query=''; this.modal='picker'; this.shown=true; this.form.listEntries(kind);
  }
  private selectEntry(entry:Fields):void {
    const view=this.view;
    this.form.pick(this.kind,entry).then((item:Fields|undefined) => {
      if (item && this.view===view && this.shown && this.modal==='picker') this.editItem(item,true);
    });
  }
  private openMap():void {
    this.view++; this.expanded=''; this.modal='map'; this.shown=true; this.form.map('topics');
  }

  private numberValue(result:Fields,key:string,n:number):Fields {
    if (n>=0) { result[key]=n; return result; }
    if (result[key]==null) return result;
    const kept:Fields={}; Object.keys(result).forEach((field:string) => { if (field!==key) kept[field]=result[field]; });
    return kept;
  }
  private fields():Fields|undefined {
    let result=Wire.parse(JSON.stringify(this.opened));
    const d=selectionNumber(this.days,1,36500);
    if (this.modal==='rule') {
      const attachment=selectionNumber(this.attachmentDays,1,36500), context=selectionNumber(this.context,0,200);
      if (d===-2 || attachment===-2 || context===-2) return undefined;
      result['on']=this.ruleOn;
      result=this.numberValue(result,'days',d);
      if (this.part==='messages') {
        result=this.numberValue(result,'attachment_days',attachment);
        if (this.ruleName==='mine') result=this.numberValue(result,'context',context);
        if (this.ruleName==='mentions' && this.includeAll!==Wire.bool(this.opened,'include_all',true)) result['include_all']=this.includeAll;
      }
    } else {
      if (this.mode==='all' && d===-2) return undefined;
      result['read']=this.mode!=='skip'; result['store']=this.mode==='all';
      if (this.mode==='all') result=this.numberValue(result,'days',d);
      else if (this.mode!==selectionItemMode(this.opened)) result=this.numberValue(result,'days',-1);
    }
    return result;
  }
  private dirty():boolean {
    if (this.modal!=='rule' && this.modal!=='item') return false;
    const value=this.fields();
    return !value || JSON.stringify(value)!==JSON.stringify(this.opened);
  }
  private save():void {
    const desired=this.fields(), view=this.view;
    if (!desired || this.form.saving) return;
    const saved=this.modal==='rule' ? this.form.saveRule(this.part,this.ruleName,this.opened,desired) : this.form.saveItem(this.originalItem,desired);
    saved.then((ok:boolean) => { if (ok && view===this.view) this.closeModal(); });
  }
  private closeModal():void {
    this.view++; this.shown=false; this.form.cancelPicker(); this.modal=''; this.form.problem='';
  }
  private cancel():void {
    if (this.form.saving) return;
    if (this.dirty()) {
      this.getUIContext().showAlertDialog({title:tr('ks_discard'),message:this.editorTitle(),primaryButton:{value:tr('cancel'),action:()=>{}},
        secondaryButton:{value:tr('ks_discard'),action:()=>this.leave()}}); return;
    }
    this.leave();
  }
  private leave():void {
    if (this.modal==='item' && this.fromPicker) { this.view++; this.modal='picker'; this.fromPicker=false; this.form.problem=''; return; }
    if (this.modal==='point') { this.modal='map'; return; }
    this.closeModal();
  }
  private remove(row:Fields):void {
    const view=this.view;
    this.form.removeItem(row).then((ok:boolean) => {
      if (ok && view===this.view) { this.removed=Wire.parse(JSON.stringify(row)); this.closeModal(); }
    });
  }
  private undo():void {
    const item=this.removed;
    if (item) this.form.saveItem(undefined,item).then((ok:boolean) => { if (ok && this.removed===item) this.removed=undefined; });
  }
  private editorTitle():string {
    if (this.modal==='rule') return this.title(this.ruleName);
    if (this.modal==='item') return tr(this.kind==='doc' ? 'ks_doc_editor' : 'ks_chat_editor');
    if (this.modal==='picker') return tr(this.kind==='doc' ? 'ks_add_doc' : 'ks_add_chat').replace(/…$/,'');
    return tr('ks_map_title');
  }
  private levelTitle(part:string,index:number):string {
    return index ? tr('ks_priority').replace('{level}',String(index+1)) : tr(part==='messages' ? 'ks_messages' : 'ks_docs');
  }
  private modeHint():string { return tr(this.mode==='skip' ? 'ks_skip_hint' : 'ks_'+this.mode+'_'+this.kind+'_hint'); }

  @Builder note(label:string) {
    Text(label).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').padding({left:12,right:10,top:5,bottom:7})
  }
  @Builder groupLabel(label:string) {
    Text(label).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).width('100%')
      .padding({left:14,right:8,top:19,bottom:6})
  }
  @Builder row(title:string,detail:string,value:string,identity:string,action:()=>void) {
    Row({space:8}) {
      Column({space:3}) {
        Text(title).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD)
        if (detail) Text(detail).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary).wordBreak(WordBreak.BREAK_WORD)
        if (value && nativeEnvironment.fontScale>=1.5) Text(value).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (value && nativeEnvironment.fontScale<1.5) Text(value).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).constraintSize({maxWidth:'42%'})
      SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.secondary])
    }.width('100%').constraintSize({minHeight:PhoneStyle.rowHeight}).padding({left:14,right:12,top:8,bottom:8})
      .backgroundColor(Colors.surface).accessibilityGroup(true).accessibilityText([title,detail,value].filter((v:string) => !!v).join(' · '))
      .onClick(action).id(identity)
  }
  @Builder ruleMenu(part:string,level:string[],i:number,n:number) {
    Menu() {
      MenuItem({content:tr('ks_up')}).enabled(i>0 && !this.form.saving).onClick(() => this.form.reorder(part,level[0],'up'))
      MenuItem({content:tr('ks_down')}).enabled(i<n-1 && !this.form.saving).onClick(() => this.form.reorder(part,level[0],'down'))
      ForEach(level,(name:string) => {
        if (i>0) MenuItem({content:tr('ks_join').replace('{name}',this.title(name)).replace('{level}',String(i))})
          .enabled(!this.form.saving).onClick(() => this.form.reorder(part,name,'join'))
        if (level.length>1) MenuItem({content:tr('ks_split').replace('{name}',this.title(name))}).enabled(!this.form.saving)
          .onClick(() => this.form.reorder(part,name,'split'))
      },(name:string) => name)
    }
  }
  @Builder ruler(part:string,level:string[],i:number,n:number) {
    Row({space:8}) {
      Text(this.levelTitle(part,i)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).layoutWeight(1)
      Button({type:ButtonType.Circle}) { Text('•••').fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.link) }.width(44).height(44).padding(0)
        .backgroundColor(Color.Transparent).bindMenu(this.ruleMenu(part,level,i,n)).accessibilityText(this.levelTitle(part,i)+' · '+tr('more'))
        .id('settings.knowledge.level.'+part+'.'+String(i+1))
    }.width('100%').padding({left:14,right:1,top:7})
    Column() {
      ForEach(level,(name:string,index:number) => {
        if (index>0) Divider().color(Colors.divider).strokeWidth(Tokens.divider).margin({left:14})
        this.row(this.title(name),this.detail(part,name),this.ruleValue(this.form.rule(part,name)),'settings.knowledge.rule.'+part+'.'+name,()=>this.editRule(part,name))
      },(name:string) => name)
    }.width('100%').borderRadius(Tokens.card).clip(true)
  }
  @Builder toggle(label:string,on:boolean,identity:string,valid:boolean,change:(enabled:boolean)=>void) {
    Row({space:8}) {
      Text(label).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1).wordBreak(WordBreak.BREAK_WORD)
      Toggle({type:ToggleType.Switch,isOn:on}).accessibilityText(label).enabled(valid).onChange(change).id(identity)
    }.width('100%').constraintSize({minHeight:PhoneStyle.rowHeight}).padding({left:14,right:10,top:5,bottom:5}).backgroundColor(Colors.surface)
  }
  @Builder addItem(kind:string) {
    Button({type:ButtonType.Normal}) {
      Row({space:9}) {
        SymbolGlyph(Symbols.plus).fontSize('16vp').fontColor([Colors.link])
        Text(tr(kind==='chat' ? 'ks_add_chat' : 'ks_add_doc')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.link)
      }.width('100%')
    }.width('100%').constraintSize({minHeight:44}).padding({left:14,right:12}).backgroundColor(Colors.surface)
      .onClick(()=>this.pick(kind)).id('settings.knowledge.add.'+kind)
  }
  @Builder landing() {
    List() {
      ListItem() {
        Column() {
          this.toggle(tr('ks_topics'),this.form.topicsOn===true,'settings.knowledge.topics',this.form.topicsOn!==undefined && !this.form.topicSaving,(on:boolean)=>this.form.setTopics(on))
          Divider().color(Colors.divider).strokeWidth(Tokens.divider).margin({left:14})
          this.row(tr('ks_map'),'','','settings.knowledge.map',()=>this.openMap())
        }.borderRadius(Tokens.card).clip(true)
      }
      if (this.form.topicProblem) ListItem() { this.note(tr(this.form.topicProblem)) }
      if (this.form.problem) ListItem() {
        Row() {
          Text(tr(this.form.problem)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.danger).layoutWeight(1)
          Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).enabled(!this.form.saving).onClick(()=>this.form.load())
        }.width('100%').padding(Tokens.gap)
      }
      if (this.form.loading || this.form.saving) ListItem() { LoadingProgress().width(26).height(26).margin(5) }
      if (Object.keys(this.form.data).length) {
        ListItem() {
          Column() {
            this.groupLabel(tr('ks_individual'))
            Column() {
              ForEach(this.form.items(),(row:Fields)=>{
                this.row(Wire.text(row,'name') || Wire.text(row,'id'),Wire.text(row,'source'),this.itemValue(row),'settings.knowledge.item.'+Wire.text(row,'id'),()=>this.editItem(row,false))
                Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({left:14})
              },(row:Fields)=>selectionItemKey(row))
              this.addItem('chat')
              Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({left:14})
              this.addItem('doc')
            }.width('100%').borderRadius(Tokens.card).clip(true)
            this.note(tr('ks_individual_hint'))
            if (this.removed) Row() {
              Text(tr('ks_removed')).layoutWeight(1).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary)
              Button(tr('ks_undo'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.undo())
            }.width('100%')
          }
        }
        ForEach(['messages','docs'],(part:string)=>{
          ForEach(this.form.levels(part),(level:string[],index:number)=>{
            ListItem() { this.ruler(part,level,index,this.form.levels(part).length) }
          },(level:string[],index:number)=>part+String(index)+level.join(','))
          ListItem() { this.note(tr(part==='messages' ? 'ks_priority_hint' : 'ks_docs_hint')) }
        },(part:string)=>part)
      }
    }.width('100%').height('100%').padding({left:PhoneStyle.page,right:PhoneStyle.page,top:8,bottom:Tokens.page})
      .backgroundColor(Colors.grouped).id('settings.knowledge.list')
  }

  @Builder numField(name:string,field:string,key:string) {
    Column({space:4}) {
      Text(tr(name)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink)
      TextInput({text:field,placeholder:key==='context' ? '50' : tr('default')}).type(InputType.Number).accessibilityText(tr(name))
        .onChange((v:string)=>{ if (key==='days') this.days=v; else if (key==='attachments') this.attachmentDays=v; else this.context=v; })
        .id('settings.knowledge.'+key)
    }.width('100%').alignItems(HorizontalAlign.Start).padding(11).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
  }
  @Builder ruleEditor() {
    Scroll() {
      Column({space:8}) {
        this.toggle(tr('ks_store'),this.ruleOn,'settings.knowledge.rule.on',!this.form.saving,(on:boolean)=>this.ruleOn=on)
        this.note(this.detail(this.part,this.ruleName))
        this.numField('ks_text_days',this.days,'days')
        if (this.part==='messages') this.numField('ks_attachment_days',this.attachmentDays,'attachments')
        this.note(tr(this.part==='messages' ? 'ks_days_hint' : 'ks_doc_days_hint'))
        if (this.part==='messages' && this.ruleName==='mentions') this.toggle(tr('ks_include_all'),this.includeAll,'settings.knowledge.include_all',true,(on:boolean)=>this.includeAll=on)
        if (this.part==='messages' && this.ruleName==='mine') { this.numField('ks_context',this.context,'context'); this.note(tr('ks_context_hint')) }
        if (!this.fields()) this.note(tr('ks_invalid'))
        if (this.form.problem) this.note(tr(this.form.problem))
      }.alignItems(HorizontalAlign.Start).padding(PhoneStyle.page).width('100%')
    }.width('100%').height('100%').align(Alignment.TopStart)
  }
  @Builder itemEditor() {
    Scroll() {
      Column({space:8}) {
        this.groupLabel(Wire.text(this.opened,'name') || Wire.text(this.opened,'id'))
        this.note(tr('ks_source')+' · '+Wire.text(this.opened,'source'))
        this.groupLabel(tr('ks_mode'))
        ForEach(['skip','rules','all'],(m:string)=>{
          Row({space:8}) {
            Text(this.optionTitle(m,this.kind)).layoutWeight(1).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink)
            if (m===this.mode) SymbolGlyph(Symbols.check).fontSize('15vp').fontColor([Colors.link])
          }.width('100%').constraintSize({minHeight:44}).backgroundColor(Colors.surface).padding({left:14,right:14})
            .accessibilityText(this.optionTitle(m,this.kind)).accessibilitySelected(this.mode===m).onClick(()=>{this.mode=m; if (m!=='all') this.days='';}).id('settings.knowledge.mode.'+m)
        },(m:string)=>m)
        this.note(this.modeHint())
        if (this.mode==='all') { this.numField('ks_days',this.days,'days'); this.note(tr('ks_doc_days_hint')) }
        if (!this.fields()) this.note(tr('ks_invalid'))
        if (this.form.problem) this.note(tr(this.form.problem))
        if (this.originalItem) Button(tr('ks_remove'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.danger)
          .enabled(!this.form.saving).onClick(()=>{ if (this.originalItem) this.remove(this.originalItem); }).id('settings.knowledge.remove')
      }.width('100%').alignItems(HorizontalAlign.Start).padding(PhoneStyle.page)
    }.width('100%').height('100%').align(Alignment.TopStart)
  }
  @Computed get filtered():Fields[] {
    const q=this.query.trim().toLowerCase();
    return this.form.entries.filter((e:Fields)=>!q || [Wire.text(e,'name'),Wire.text(e,'id'),Wire.text(e,'one_line')].join(' ').toLowerCase().includes(q));
  }
  @Builder picker() {
    Column({space:8}) {
      Search({value:this.query,placeholder:tr('search')}).onChange((v:string)=>this.query=v).id('settings.knowledge.search')
      this.note(tr(this.kind==='chat' ? 'ks_list_chats' : 'ks_list_docs'))
      if (this.form.pickerBusy) LoadingProgress().width(24).height(24)
      if (this.form.pickerError) {
        this.note(tr(this.form.pickerError))
        if (this.form.pickerError==='ks_load_error') Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.form.listEntries(this.kind))
      }
      List() {
        ForEach(this.filtered,(entry:Fields)=>{
          ListItem() { this.row(Wire.text(entry,'name') || Wire.text(entry,'id'),Wire.text(entry,'one_line'),'','settings.knowledge.pick.'+Wire.text(entry,'id'),()=>this.selectEntry(entry)) }
        },(entry:Fields)=>Wire.text(entry,'id'))
        if (!this.form.pickerBusy && !this.filtered.length) ListItem() { this.note(tr(this.query ? 'no_results' : this.kind==='chat' ? 'ks_empty_chat' : 'ks_empty_doc')) }
      }.layoutWeight(1).width('100%').divider({strokeWidth:Tokens.divider,startMargin:14,color:Colors.divider})
    }.width('100%').height('100%').padding(PhoneStyle.page)
  }

  @Builder mapDetail(data:Fields) {
    Column({space:5}) {
      if (Wire.text(data,'summary')) this.note(Wire.text(data,'summary'))
      if (Wire.text(data,'recent')) this.note(Wire.text(data,'recent'))
      ForEach(Wire.rows(data,'points'),(point:Fields)=>{
        this.row(Wire.text(point,'text'),Wire.strings(point,'path').join(' › '),'','settings.knowledge.point.'+Wire.text(point,'id'),()=>{
          this.point=point; this.modal='point';
        })
      },(point:Fields,index:number)=>Wire.text(point,'id') || String(index))
      ForEach(Wire.strings(data,'log'),(info:string)=>{ this.note(info) },(info:string,index:number)=>String(index)+info)
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left:10,right:6})
  }
  @Builder knowledgeMap() {
    Column() {
      Row({space:5}) {
        ForEach(['topics','projects'],(mode:string)=>{
          Button(tr(mode==='topics' ? 'ks_topic' : 'projects'),{buttonStyle:ButtonStyleMode.TEXTUAL}).layoutWeight(1)
            .fontColor(this.form.mapMode===mode ? Colors.link : Colors.secondary).accessibilitySelected(this.form.mapMode===mode)
            .onClick(()=>{ if (mode!==this.form.mapMode) { this.expanded=''; this.form.map(mode); } }).id('settings.knowledge.map.'+mode)
        },(mode:string)=>mode)
      }.width('100%')
      if (this.form.mapBusy) LoadingProgress().width(24).height(24)
      if (this.form.mapError) Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.form.map(this.form.mapMode,this.expanded))
      List() {
        ForEach(this.form.mapRows,(entry:Fields)=>{
          ListItem() {
            Column() {
              this.row(Wire.text(entry,'name') || Wire.text(entry,'id'),Wire.text(entry,'one_line'),Wire.text(entry,'extra'),'settings.knowledge.node.'+Wire.text(entry,'id'),()=>{
                const id=Wire.text(entry,'id'); this.expanded=this.expanded===id ? '' : id;
                if (this.expanded && !this.form.mapDetails[id]) this.form.map(this.form.mapMode,id);
              })
              if (this.expanded===Wire.text(entry,'id') && this.form.mapDetails[this.expanded]) this.mapDetail(Wire.object(this.form.mapDetails,this.expanded))
            }.width('100%')
          }
        },(entry:Fields)=>Wire.text(entry,'id'))
        if (!this.form.mapBusy && !this.form.mapRows.length && !this.form.mapError) ListItem() { this.note(tr('empty')) }
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').padding(PhoneStyle.page)
  }
  @Builder modalContent() {
    Column() {
      Row({space:6}) {
        Button(tr(this.fromPicker || this.modal==='point' ? 'back' : 'cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL})
          .fontSize(14).maxFontScale(Tokens.navigationTextMaxScale).onClick(()=>this.cancel()).id('settings.knowledge.cancel')
        Text(this.editorTitle()).layoutWeight(1).fontSize(16).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink).textAlign(TextAlign.Center).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
        if (this.modal==='item' || this.modal==='rule') Button(tr('save'),{buttonStyle:ButtonStyleMode.TEXTUAL})
          .fontSize(14).maxFontScale(Tokens.navigationTextMaxScale).enabled(!this.form.saving && !!this.fields() && Object.keys(this.form.data).length>0)
          .onClick(()=>this.save()).id('settings.knowledge.save')
        else Blank().width(45)
      }.width('100%').constraintSize({minHeight:48}).padding({left:7,right:7,top:4})
      Column() {
        if (this.modal==='picker') this.picker()
        else if (this.modal==='rule') this.ruleEditor()
        else if (this.modal==='item') this.itemEditor()
        else if (this.modal==='map') this.knowledgeMap()
        else if (this.modal==='point') Scroll() { Column({space:8}) {
          Text(Wire.text(this.point,'text')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontColor(Colors.ink)
          if (Wire.text(this.point,'why')) this.note(Wire.text(this.point,'why'))
          if (Wire.text(this.point,'when')) this.note(Wire.text(this.point,'when'))
          ForEach(Wire.rows(this.point,'sources'),(entry:Fields)=>{this.note(Wire.text(entry,'kind')+' · '+Wire.text(entry,'id'))},(entry:Fields,i:number)=>String(i)+Wire.text(entry,'id'))
        }.width('100%').alignItems(HorizontalAlign.Start).padding(PhoneStyle.page) }.width('100%').height('100%').align(Alignment.TopStart)
      }.width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('settings.knowledge.modal.'+this.modal)
  }
  build() {
    Column() { this.landing() }.width('100%').height('100%')
      .bindSheet($$this.shown,this.modalContent(),{height:SheetSize.LARGE,showClose:false,dragBar:false,
        onWillDismiss:()=>this.cancel(),onDisappear:()=>{ if (!this.shown) this.form.cancelPicker(); }})
  }
}
