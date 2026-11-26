import { Tokens } from '../design/Tokens';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from '../design/Colors';
import { PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { PhoneAgentSettings, AgentSettingTextDraft, agentSettingValue, cleanAgentFolder, FOLLOW_FIELDS } from '../models/PhoneAgentSettings';
import { ResourceRow, Wire } from '../models/Wire';

@ComponentV2
export struct PhoneAgentSettingsPage {
  @Require @Param model:ClientModel;
  @Require @Param page:string;
  @Local form:PhoneAgentSettings = new PhoneAgentSettings();
  @Local textDraft:AgentSettingTextDraft = new AgentSettingTextDraft();
  private ownerContext:string = '';
  @Local draft:string = '';
  @Local folderAction:string = 'block';

  aboutToAppear():void { this.open(); }
  aboutToDisappear():void { this.textDraft.close(); this.form.close(); }
  @Monitor('page','model.profile','model.connected') reset():void { this.open(); }
  private textTarget():string { return JSON.stringify([this.model.scope(),this.page,this.form.machine]); }
  private open(machine:string=''):void {
    const current=JSON.stringify([this.model.scope(),this.page]), sameOwner=this.ownerContext===current;
    if (!sameOwner || (machine && machine!==this.form.machine)) this.textDraft.close();
    this.ownerContext=current; this.draft=''; this.folderAction='block';
    if (!this.model.connected) { this.form.close(); this.form.error='offline'; return; }
    this.form.open(this.model,this.page,machine || (sameOwner ? this.form.machine : '')).catch(() => undefined);
  }
  @Computed get machines():ResourceRow[] { return this.form.machines(this.model); }
  @Computed get options():Record<string,Object>[] { return Wire.rows(this.form.data,'settings'); }
  @Computed get rules():Record<string,Object>[] { return Wire.rows(this.form.data,'rules'); }
  @Computed get follow():string[] { return Object.keys(FOLLOW_FIELDS).filter((key:string) => typeof this.form.data[key] === 'boolean'); }
  private canEdit():boolean { return this.model.connected && !this.form.saving && !this.form.loading && !this.form.error && (this.page!=='phone_agent_folders' || Wire.bool(this.form.data,'supports_expected_rules')); }
  private save(key:string,value:Object):void { this.form.setOption(key,value).catch(() => undefined); }

  @Builder separator() { Divider().strokeWidth(0.5).color(Colors.divider).margin({left:15}) }
  private inputHeight():number { return Math.max(40,Math.ceil(24*Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17)))+10); }
  @Builder machinePicker() {
    Select(this.machines.map((r:ResourceRow) => ({value:r.title || r.id} as SelectOption)))
      .value(this.machines.find((r:ResourceRow) => r.id===this.form.machine)?.title || this.form.machine)
      .constraintSize({maxWidth:'100%',minHeight:this.inputHeight()}).enabled(!this.form.saving && this.machines.length>0)
      .onSelect((index:number) => { if(this.machines[index]) this.open(this.machines[index].id); }).id('settings.agent.machine')
  }
  @Builder machineSelector() {
    if (Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17))>=1.4) Column({space:5}) {
      Text(tr('machines')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink)
      this.machinePicker()
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left:15,right:12,top:8,bottom:8}).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
    else Row({space:10}) {
      Text(tr('machines')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
      this.machinePicker()
    }.width('100%').constraintSize({minHeight:48}).padding({left:15,right:12,top:5,bottom:5})
      .backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
  }
  @Builder optionAction(row:Record<string,Object>) {
    if (Wire.text(row,'type')==='bool') Toggle({type:ToggleType.Switch,isOn:agentSettingValue(row)===true})
      .selectedColor(Colors.action).enabled(this.canEdit())
      .onChange((v:boolean) => this.save(Wire.text(row,'key'),v)).id('settings.agent.option.'+Wire.text(row,'key'))
    else if (Wire.text(row,'type')==='enum') Select(Wire.strings(row,'options').map((v:string) => ({value:v} as SelectOption)))
      .constraintSize({maxWidth:'100%',minHeight:this.inputHeight()}).value(String(agentSettingValue(row)??'')).enabled(this.canEdit())
      .onSelect((_index:number,v:string) => this.save(Wire.text(row,'key'),v)).id('settings.agent.option.'+Wire.text(row,'key'))
    else if (Wire.text(row,'type')==='string' && !this.textDraft.isEditing(this.textTarget(),Wire.text(row,'key')))
      Button(String(agentSettingValue(row)??'') || tr('edit'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).constraintSize({maxWidth:'100%',minHeight:this.inputHeight()})
        .fontColor(Colors.link).enabled(this.canEdit()).onClick(() => { this.textDraft.begin(this.textTarget(),Wire.text(row,'key'),String(agentSettingValue(row)??'')); })
        .id('settings.agent.option.'+Wire.text(row,'key'))
  }
  @Builder option(row:Record<string,Object>) {
    Column({space:5}) {
      if (Math.min(nativeEnvironment.fontScale,Tokens.interfaceScaleFor(17))>=1.4 && Wire.text(row,'type')!=='bool') Column({space:5}) {
        Text(Wire.text(row,'title') || Wire.text(row,'key')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink)
        this.optionAction(row)
      }.width('100%').alignItems(HorizontalAlign.Start)
      else Row({space:8}) {
        Text(Wire.text(row,'title') || Wire.text(row,'key')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
        this.optionAction(row)
      }.width('100%')
      if (this.textDraft.isEditing(this.textTarget(),Wire.text(row,'key'))) Row({space:6}) {
        TextInput({text:this.textDraft.text}).height(this.inputHeight()).layoutWeight(1).onChange((value:string) => this.textDraft.edit(value))
          .id('settings.agent.option.editor')
        Button(tr('save'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).constraintSize({minHeight:this.inputHeight()}).enabled(this.canEdit())
          .onClick(() => this.textDraft.save(this.form,this.textTarget())).id('settings.agent.option.save')
      }.width('100%')
      if (Wire.text(row,'detail')) Text(Wire.text(row,'detail')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
    }.width('100%').alignItems(HorizontalAlign.Start).padding({left:15,right:12,top:8,bottom:8})
  }

  private folderChange(rule:Record<string,Object>,enabled:boolean,action:string):void {
    const next=Wire.parse(JSON.stringify(rule));next['enabled']=enabled;next['action']=action;
    this.form.changeFolder(Wire.text(rule,'pattern'),next).catch(() => undefined);
  }
  @Builder actionPicker(value:string,action:(s:string)=>void) {
    Select([{value:tr('phone_folder_block')},{value:tr('phone_folder_hide')}]).value(tr(value==='block'?'phone_folder_block':'phone_folder_hide'))
      .enabled(this.canEdit()).onSelect((i:number) => action(i===0?'block':'hide'))
  }
  @Builder folderRules() {
    Column() {
      ForEach(this.rules,(rule:Record<string,Object>) => {
        Column() {
          Row({space:8}) {
            Text(Wire.text(rule,'pattern')).fontFamily('monospace').fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).layoutWeight(1).fontColor(Colors.ink)
            Toggle({type:ToggleType.Switch,isOn:Wire.bool(rule,'enabled')}).selectedColor(Colors.action).enabled(this.canEdit())
              .onChange((v:boolean) => this.folderChange(rule,v,Wire.text(rule,'action','hide')))
          }.width('100%')
          Row() {
            this.actionPicker(Wire.text(rule,'action','hide'),(s:string) => this.folderChange(rule,Wire.bool(rule,'enabled'),s))
            Blank()
            Button(tr('delete'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.danger).enabled(this.canEdit())
              .onClick(() => this.form.changeFolder(Wire.text(rule,'pattern'),undefined))
          }.width('100%')
        }.padding({left:15,right:12,top:7,bottom:7}).width('100%')
        this.separator()
      },(r:Record<string,Object>) => JSON.stringify(r))
      TextInput({text:this.draft,placeholder:tr('phone_folder_pattern')}).height(this.inputHeight()).fontFamily('monospace').onChange((s:string) => this.draft=s)
        .enabled(this.canEdit()).id('settings.agent.folder.pattern')
      Row() {
        this.actionPicker(this.folderAction,(s:string) => this.folderAction=s)
        Blank()
        Button(tr('add'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).enabled(this.canEdit() && !!cleanAgentFolder(this.draft) &&
          !this.rules.some((r:Record<string,Object>) => Wire.text(r,'pattern')===cleanAgentFolder(this.draft)))
          .onClick(() => {
            const pattern=cleanAgentFolder(this.draft);
            this.form.changeFolder(pattern,Wire.payload(['pattern','action','enabled'],[pattern,this.folderAction,true]),true)
              .then((saved:boolean) => { if (saved && cleanAgentFolder(this.draft)===pattern) this.draft=''; });
          }).id('settings.agent.folder.add')
      }.width('100%').padding({left:15,right:12,top:4,bottom:6})
    }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).clip(true)
  }

  build() {
    Scroll() {
      Column({space:12}) {
        if (this.page!=='phone_agent_followup') this.machineSelector()
        if (this.form.loading || this.form.saving) LoadingProgress().height(23).width(23)
        if (this.page==='phone_agent_folders' && !this.form.loading && Array.isArray(this.form.data['rules']) && !Wire.bool(this.form.data,'supports_expected_rules'))
          Text(tr('phone_conditional_read_only')).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).width('100%').id('settings.agent.folder.read_only')
        if (this.form.notice) Text(tr(this.form.notice)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.needs).width('100%').id('settings.agent.folder.notice')
        if (this.form.error) Row({space:8}) {
          Text(tr(this.form.error)).maxFontScale(Tokens.interfaceFontMaxScale).layoutWeight(1).fontColor(Colors.secondary)
          Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).onClick(() => this.form.read()).id('settings.agent.retry')
        }.width('100%')
        if (this.page==='phone_agent_folders' && Array.isArray(this.form.data['rules'])) this.folderRules()
        else if (this.page==='phone_agent_followup') Column() {
          ForEach(this.follow,(key:string,index:number) => {
            Row({space:8}) {
              Text(tr(FOLLOW_FIELDS[key])).layoutWeight(1).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink)
              Toggle({type:ToggleType.Switch,isOn:Wire.bool(this.form.data,key)}).enabled(this.canEdit()).selectedColor(Colors.action)
                .onChange((v:boolean) => this.save(key,v)).id('settings.agent.option.'+key)
            }.width('100%').constraintSize({minHeight:48}).padding({left:15,right:12,top:5,bottom:5})
            if (index<this.follow.length-1) this.separator()
          },(key:string) => JSON.stringify([key,this.form.data[key]]))
        }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        else if (this.options.length) Column() {
          ForEach(this.options,(row:Record<string,Object>,index:number) => {
            this.option(row)
            if(index<this.options.length-1) this.separator()
          },(row:Record<string,Object>) => JSON.stringify(row))
        }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        if (this.canEdit() && !this.options.length && !this.follow.length && this.page!=='phone_agent_folders')
          Text(tr('empty')).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.secondary).width('100%')
      }.width('100%').alignItems(HorizontalAlign.Start).padding(PhoneStyle.page)
    }.width('100%').height('100%').align(Alignment.TopStart).backgroundColor(Colors.grouped).id('settings.agent.configuration')
  }
}
