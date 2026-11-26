import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Tokens } from '../design/Tokens';
import { AgentLaunchChoices, LaunchChoice, typedAgentDirectory } from '../models/AgentLaunchChoices';
import { modelIDProblem } from '../models/ModelID';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneNavigationIcon, PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';

@ComponentV2
export struct AgentStartPicker {
  @Require @Param kind:string;
  @Require @Param choices:AgentLaunchChoices;
  @Param options: LaunchChoice[] = [];
  @Param selected:string = '';
  @Param heading:string = '';
  @Param manual:boolean = false;
  @Local query:string = '';
  @Local address:string = '';
  @Local typingModel:boolean = false;
  @Local typedModel:string = '';
  private confirmedModel:string = '';
  private searchField:SearchController = new SearchController();
  private folderField:TextInputController = new TextInputController();
  @Event back:() => void = () => {};
  @Event choose:(id:string) => void = () => {};
  @Event browse:(path:string) => void = () => {};
  aboutToAppear():void { this.address = this.choices.path; }
  @Monitor('choices.path','choices.loading') resolved():void {
    if (!this.choices.loading) this.address = this.choices.path;
  }
  private goToTypedFolder():void {
    const target = typedAgentDirectory(this.address,this.choices.path);
    if (!target) return;
    this.folderField.stopEditing();
    this.browse(target);
  }
  @Computed get filtered():LaunchChoice[] {
    const term = this.query.trim().toLowerCase();
    return this.options.filter((o:LaunchChoice) => !term || (this.kind !== 'model' || !!o.id) &&
      [o.title,o.detail,o.description,o.id].concat(o.aliases).some((word:string) => word.toLowerCase().includes(term)));
  }
  private canTypeModel():boolean { return this.kind === 'model' && this.manual && !this.choices.locked; }
  private openTypedModel():void {
    if (!this.canTypeModel()) return;
    this.typedModel = this.selected && !this.choices.listsModel(this.selected) ? this.selected : '';
    this.confirmedModel = ''; this.typingModel = true;
  }
  private confirmTypedModel():void {
    const id=this.typedModel.trim();
    if (!this.typingModel || !this.canTypeModel() || modelIDProblem(id,tr)) return;
    this.confirmedModel = id; this.typingModel = false;
  }
  private finishTypedModel():void {
    const id=this.confirmedModel; this.confirmedModel = '';
    if (id && this.canTypeModel()) this.choose(id);
  }
  @Builder typedModelSheet() {
    Column({space:16}) {
      Row() {
        Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.link)
          .onClick(() => { this.typingModel = false; }).id('agent.typed_model.cancel')
        Text(tr('flow_typed_model_title')).layoutWeight(1).textAlign(TextAlign.Center)
          .fontColor(Colors.ink).fontSize(17).maxFontScale(Tokens.chromeFontMaxScale).fontWeight(600)
        Button(tr('flow_typed_model_use'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.link)
          .fontWeight(600).enabled(this.canTypeModel() && !modelIDProblem(this.typedModel.trim(),tr))
          .onClick(() => this.confirmTypedModel()).id('agent.typed_model.use')
      }.width('100%')
      Scroll() {
        Column({space:8}) {
          TextInput({text:this.typedModel,placeholder:'vendor/model-name'})
            .fontFamily('monospace').fontSize(Tokens.interfaceFontVp(17,nativeEnvironment.fontScale))
            .height(PhoneStyle.groupedRowHeight(nativeEnvironment.fontScale)).padding({left:20,right:20})
            .fontColor(Colors.ink).backgroundColor(Colors.sheetCard).borderRadius(PhoneStyle.cardRadius)
            .accessibilityText(tr('model_id')).enterKeyType(EnterKeyType.Done).defaultFocus(true)
            .onChange((value:string) => { this.typedModel = value; }).onSubmit(() => this.confirmTypedModel()).id('agent.typed_model.field')
          if (this.typedModel.trim() && modelIDProblem(this.typedModel.trim(),tr)) {
            Text(modelIDProblem(this.typedModel.trim(),tr)).fontColor($r('app.color.persona_dock_warning')).id('agent.typed_model.problem')
              .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).padding({left:20,right:20})
          } else {
            Text(tr('flow_model_id_rule')+'\n'+tr('flow_model_id_note')).fontColor(Colors.secondary)
              .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).padding({left:20,right:20})
          }
        }.alignItems(HorizontalAlign.Start).width('100%')
      }.width('100%').layoutWeight(1).align(Alignment.TopStart)
    }.width('100%').height('100%').padding({left:16,right:16,top:12,bottom:12})
      .backgroundColor(Colors.sheetBackground).id('agent.typed_model')
  }
  private folderName():string {
    const path=this.choices.path.replace(/\/+$/,'');
    return path.split('/').pop() || this.choices.path;
  }
  @Builder divider(inset:number) { Divider().strokeWidth(0.5).color(Colors.divider).margin({left:inset}) }
  private optionHeight(choice: LaunchChoice, folder: boolean): number {
    const scale = nativeEnvironment.fontScale;
    if (this.kind !== 'model') return folder ? PhoneStyle.groupedRowHeight(scale) : 48;
    if (choice.description) return PhoneStyle.formSize(83, 105, scale);
    if (choice.detail || !choice.id && this.choices.defaultModelName()) return PhoneStyle.formSize(66.7, 81.3, scale);
    return PhoneStyle.groupedRowHeight(scale);
  }
  @Builder item(o:LaunchChoice,folder:boolean) {
    Row({space:11}) {
      if (this.kind !== 'model') SymbolGlyph(folder || this.kind === 'workspace' ? Symbols.folder : this.kind === 'launcher' ? Symbols.agent : Symbols.apps)
        .fontSize('18vp').fontColor([folder && o.detail !== 'Git' ? Colors.secondary : Colors.link])
      Column({space:this.kind === 'model' ? 2 : 3}) {
        Text(o.title).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontWeight(folder ? FontWeight.Normal : FontWeight.Medium).fontColor(folder ? Colors.link : Colors.ink).maxLines(2)
        if (o.detail || this.kind === 'model' && !o.id && this.choices.defaultModelName()) Text(o.detail || this.choices.defaultModelName()).fontFamily(this.kind === 'model' ? 'monospace' : 'sans-serif')
          .fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
        if (!folder && o.description) Text(o.description).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).fontColor(Colors.secondary).maxLines(2).textOverflow({overflow:TextOverflow.Ellipsis})
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      if (!folder && o.id === this.selected) SymbolGlyph(Symbols.check).fontSize('18vp').fontColor([Colors.link])
    }.width('100%').constraintSize({minHeight:this.optionHeight(o,folder)}).padding({left:20,right:20,top:this.kind === 'model' ? 12 : 8,bottom:this.kind === 'model' ? 12 : 8})
      .backgroundColor(Colors.sheetCard).borderRadius(folder || this.kind === 'model' ? 0 : 10)
      .enabled(!o.disabled).opacity(o.disabled ? 0.65 : 1).onClick(() => { if (folder) this.browse(o.id); else this.choose(o.id); })
      .accessibilityGroup(true).accessibilitySelected(!folder && o.id === this.selected).id(folder ? 'agent.folder.item' : 'agent.option')
  }
  @Builder pickerSearch() {
    Search({value:this.query,placeholder:this.kind === 'model' && this.manual ? tr('flow_model_id_search') :
      tr(this.kind === 'workspace' ? 'flow_workspace_search' : 'search'),controller:this.searchField}).searchIcon({size:Tokens.interfaceFontVp(17,nativeEnvironment.fontScale)})
      .textFont({size:Tokens.interfaceFontVp(17,nativeEnvironment.fontScale)}).placeholderFont({size:Tokens.interfaceFontVp(17,nativeEnvironment.fontScale)})
      .height(this.kind === 'model' ? 48 : PhoneStyle.formSize(44,58,nativeEnvironment.fontScale))
      .onChange((v:string) => this.query = v).margin({left:this.kind === 'model' ? 28 : 14,right:this.kind === 'model' ? 28 : 14,bottom:this.kind === 'model' && !nativeEnvironment.keyboardHeightPx ? 28 : 8}).id('agent.picker.search')
  }
  @Builder chooseFolder() {
    Column() {
      Row({space:6}) {
        TextInput({text:this.address,placeholder:tr('flow_folder_search'),controller:this.folderField}).layoutWeight(1).height(PhoneStyle.groupedRowHeight(nativeEnvironment.fontScale)).fontSize(Tokens.interfaceFontVp(13,nativeEnvironment.fontScale))
          .placeholderFont({size:Tokens.interfaceFontVp(13,nativeEnvironment.fontScale)})
          .fontFamily('monospace').backgroundColor(Color.Transparent).padding(0)
          .enterKeyType(EnterKeyType.Go).onChange((v:string) => this.address = v)
          .onSubmit(() => this.goToTypedFolder()).id('agent.folder.address')
        if (this.address.trim() && typedAgentDirectory(this.address,this.choices.path) !== this.choices.path) Button(tr('open'),{buttonStyle:ButtonStyleMode.TEXTUAL})
          .fontSize(15).maxFontScale(Tokens.navigationTextMaxScale).onClick(() => this.goToTypedFolder()).id('agent.folder.go')
      }.width('100%').padding({left:20,right:20})
      this.divider(20)
      Button({type:ButtonType.Normal}) {
        Row({space:8}) {
          Stack() { SymbolGlyph(Symbols.check).fontSize('13vp').fontColor([Colors.onAction]) }.width(21).height(21).borderRadius(11).backgroundColor(Colors.action)
          Text(this.folderName() ? tr('flow_use_named_folder').replace('{name}',this.folderName()) : tr('flow_use_folder'))
            .fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontWeight(FontWeight.Medium).fontColor(Colors.link).layoutWeight(1).textAlign(TextAlign.Start)
        }.width('100%').constraintSize({minHeight:PhoneStyle.groupedRowHeight(nativeEnvironment.fontScale)}).padding({top:7,bottom:7})
      }.width('100%').padding({left:14,right:14}).backgroundColor(Color.Transparent)
        .enabled(!this.choices.loading && !!this.choices.path)
        .onClick(() => this.choose(this.choices.path)).id('agent.folder.use')
    }.width('100%').backgroundColor(Colors.sheetCard).borderRadius(PhoneStyle.cardRadius).clip(true)
  }
  private groupItems(group:string):LaunchChoice[] {
    if (group === 'folder') return this.choices.directories;
    if (group === 'default' && !this.query.trim() && this.canTypeModel() && this.selected && !this.choices.listsModel(this.selected)) {
      return this.options.filter((o:LaunchChoice) => !o.id).concat(
        new LaunchChoice(this.selected,this.selected,tr('phone_start_typed_model_id').replace('{id}',this.selected)));
    }
    return this.filtered.filter((o:LaunchChoice) => group === 'default' ? !o.id : !!o.id);
  }
  @Builder optionGroup(group:string,folder:boolean) {
    Column() {
      if (folder && this.choices.parent && this.choices.parent !== this.choices.path) {
        Row({space:11}) {
          SymbolGlyph(Symbols.up).fontSize('17vp').fontColor([Colors.link])
          Text(tr('flow_folder_up')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontColor(Colors.link)
        }.width('100%').constraintSize({minHeight:PhoneStyle.groupedRowHeight(nativeEnvironment.fontScale)}).padding({left:20,right:20}).accessibilityGroup(true)
          .onClick(() => this.browse(this.choices.parent)).id('agent.folder.up')
        if (this.choices.directories.length) this.divider(41)
      }
      ForEach(this.groupItems(group),(o:LaunchChoice,i:number) => {
        if (i) this.divider(folder ? 41 : 14)
        this.item(o,folder)
      },(o:LaunchChoice,i:number) => JSON.stringify([i,o.id,o.title,o.detail,o.description,o.disabled]))
    }.width('100%').backgroundColor(Colors.sheetCard).borderRadius(PhoneStyle.cardRadius).clip(true)
  }
  build() {
    Column() {
      Row({space:9}) {
        Button({type:ButtonType.Circle}) { PhoneNavigationIcon({mark:'navigationBack'}) }
          .width(PhoneStyle.bar).height(PhoneStyle.bar).padding(0).backgroundColor(Colors.sheetCard)
          .accessibilityText(tr('back')).onClick(() => this.back()).id('agent.picker.back')
        Text(this.kind === 'folder' && this.folderName() ? this.folderName() : this.heading).layoutWeight(1).textAlign(TextAlign.Center)
          .fontSize(18).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Bold).fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
        Blank().width(PhoneStyle.bar)
      }.width('100%').padding({left:PhoneStyle.page,right:PhoneStyle.page,top:16,bottom:10})
      if (this.kind !== 'folder' && this.kind !== 'model' && (this.kind === 'workspace' || this.options.length > 5 || this.manual)) this.pickerSearch()
      List({space:this.kind === 'folder' ? 25 : this.kind === 'model' ? 9 : 3}) {
        if (this.kind === 'folder' && this.choices.path) ListItem() {
          Column({space:7}) {
            this.chooseFolder()
            Text(tr('flow_directory_address_hint')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).padding({left:16,right:16})
          }.alignItems(HorizontalAlign.Start).width('100%')
        }
        if (this.choices.loading && (this.kind === 'folder' || this.kind === 'model'))
          ListItem() { LoadingProgress().width(25).height(25).margin(12) }
        if (this.choices.error && (this.kind === 'folder' || this.kind === 'model')) ListItem() {
          Text(trError(this.choices.error)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).padding(12).id('agent.picker.error')
        }
        if (this.kind === 'folder') {
          if (this.choices.parent || this.choices.directories.length) ListItem() { this.optionGroup('folder',true) }
          if (this.choices.error && !this.choices.loading && !this.choices.path) ListItem() {
            Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(() => this.browse(this.choices.attemptedFolder)).id('agent.folder.retry')
          }
          if (this.choices.path && !this.choices.directories.length) ListItem() { Text(tr('flow_folder_empty')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).padding(14) }
        } else {
          if (this.choices.locked && this.kind === 'model') ListItem() { Text(this.choices.locked).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).padding(12) }
          if (this.kind === 'model') {
            if (this.filtered.some((o:LaunchChoice) => !o.id)) ListItem() {
              Column({space:7}) {
                this.optionGroup('default',false)
                Text(tr('flow_model_default_hint')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).padding({left:16,right:16})
              }.width('100%').alignItems(HorizontalAlign.Start)
            }
            if (this.filtered.some((o:LaunchChoice) => !!o.id)) ListItem() {
              Column() {
                PhoneSectionTitle({text:tr('flow_model_count').replace('{count}',String(this.groupItems('models').length))})
                this.optionGroup('models',false)
              }.width('100%')
            }
          } else ForEach(this.filtered,(o:LaunchChoice) => { ListItem() { this.item(o,false) } },(o:LaunchChoice) => JSON.stringify([o.id,o.title,o.detail,o.disabled]))
          if (this.kind === 'model' && this.manual && !this.query.trim() && !this.choices.locked) ListItem() {
            Column({space:7}) {
              Row({space:10}) {
                SymbolGlyph($r('sys.symbol.keyboard')).fontSize('18vp').fontColor([Colors.link])
                Text(tr(this.choices.hasCatalogModel() ? 'flow_other_model' : 'flow_enter_model_id'))
                  .fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontColor(Colors.link)
              }.width('100%').constraintSize({minHeight:PhoneStyle.formSize(52.33,59.33,nativeEnvironment.fontScale)})
                .padding({left:20,right:20}).backgroundColor(Colors.sheetCard).borderRadius(PhoneStyle.cardRadius)
                .accessibilityGroup(true).onClick(() => this.openTypedModel()).id('agent.model.other')
              Text(tr('flow_model_manual_hint')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).padding({left:16,right:16})
            }.width('100%').alignItems(HorizontalAlign.Start).margin({top:26})
          }
          if (this.manual && this.query.trim() && !this.options.some((o:LaunchChoice) => o.id === this.query.trim()) && !this.choices.locked) {
            if (this.kind !== 'model') {
              ListItem() { this.item(new LaunchChoice(this.query.trim(),this.query.trim()),false) }
            } else if (!this.choices.listsModel(this.query.trim()) && !modelIDProblem(this.query.trim(),tr)) {
              ListItem() {
                this.item(new LaunchChoice(this.query.trim(),tr('flow_use_model_id').replace('{id}',this.query.trim()),'',false,tr('flow_unlisted_model_start')),false)
              }
            } else if (!this.filtered.length && modelIDProblem(this.query.trim(),tr)) {
              ListItem() {
                Text(modelIDProblem(this.query.trim(),tr)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14))
                  .fontColor($r('app.color.persona_dock_warning')).padding(12).id('agent.model.problem')
              }
            }
          }
          if (!this.filtered.length && !this.manual && !this.choices.loading) ListItem() { Text(tr('no_results')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).padding(12) }
        }
      }.width('100%').layoutWeight(1).padding({left:16,right:16,top:this.kind === 'model' || this.kind === 'folder' ? 35 : 7,bottom:10})
      if (this.kind === 'model' && (this.options.length > 5 || this.manual)) this.pickerSearch()
    }.width('100%').height('100%').backgroundColor(Colors.sheetBackground).id('agent.picker')
      .bindSheet($$this.typingModel,this.typedModelSheet(),{height:SheetSize.MEDIUM,detents:[SheetSize.MEDIUM,SheetSize.LARGE],
        showClose:false,backgroundColor:Colors.sheetBackground,onDisappear:() => this.finishTypedModel()})
  }
}
