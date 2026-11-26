import { common } from '@kit.AbilityKit';
import { ClientModel } from '../models/ClientModel';
import { PhoneMeetingTranscription, MeetingSpeechService } from '../models/PhoneMeetingTranscription';
import { meetingAudioNetwork } from '../platform/MeetingAudioNetwork';
import { nativeMeetingServices, NativeMeetingServices } from '../platform/NativeMeetingServices';
import { Tokens } from '../design/Tokens';
import { Symbols } from '../design/Symbols';
import { Colors } from '../design/Colors';
import { PhoneSectionTitle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';

@ComponentV2
export struct PhoneMeetingServices {
  @Require @Param model:ClientModel;
  @Local local:NativeMeetingServices=nativeMeetingServices;
  @Local speech:PhoneMeetingTranscription=new PhoneMeetingTranscription();
  @Local modal:boolean=false;
  @Local panel:string='';
  @Local draft:string='';
  @Local capturedScope:string='';
  @Local networkReady:boolean=false;
  @Local networkBusy:boolean=false;
  @Local networkProblem:boolean=false;
  private networkRevision:number=0;
  private supportsAudio():boolean {
    return this.model.connected && !this.model.demo && this.speech.offered &&
      ['meeting_conversation','meeting_live_view','meeting_transcription'].every((key:string)=>this.model.features.includes(key));
  }
  private async loadNetwork():Promise<void> {
    const n=++this.networkRevision,ctx=this.getUIContext().getHostContext() as common.UIAbilityContext;
    this.networkReady=false;this.networkProblem=false;this.networkBusy=false;
    if (!ctx) { this.networkProblem=true;return; }
    try { await meetingAudioNetwork.initialize(ctx);if (n===this.networkRevision) this.networkReady=true; }
    catch (_) { if (n===this.networkRevision) this.networkProblem=true; }
  }
  private async setWifi(value:boolean):Promise<void> {
    if (!this.networkReady || this.networkBusy || !this.supportsAudio()) return;
    const n=this.networkRevision;this.networkBusy=true;this.networkProblem=false;
    try { await meetingAudioNetwork.setWifiOnly(value); }
    catch (_) { if (n===this.networkRevision) this.networkProblem=true; }
    finally { if (n===this.networkRevision) this.networkBusy=false; }
  }
  aboutToAppear():void {
    const ctx=this.getUIContext().getHostContext() as common.UIAbilityContext;
    if (ctx) this.local.initialize(ctx).catch(() => {});
    this.speech.open(this.model);this.loadNetwork();
  }
  aboutToDisappear():void { this.networkRevision++;this.speech.close();this.modal=false;this.panel=''; }
  @Monitor('model.profile','model.connected','model.features') ownerChanged():void {
    this.modal=false;this.panel='';this.draft='';this.speech.open(this.model);this.loadNetwork();
  }
  private editTerms():void {
    this.draft=this.speech.glossary.join('\n');this.capturedScope=this.model.scope();this.panel='glossary';this.modal=true;
  }
  private async saveTerms():Promise<void> {
    if (!this.modal || this.panel!=='glossary' || this.capturedScope!==this.model.scope()) return;
    if (await this.speech.choose('meeting_glossary',this.draft.split('\n')) && this.capturedScope===this.model.scope()) this.modal=false;
  }
  private serviceLabel(s:MeetingSpeechService):string {
    if (!s.ref) return s.name || tr('meeting_service_builtin');
    return this.speech.choices.filter((c:MeetingSpeechService) => c.name===s.name && !!c.ref).length>1 ? s.name+' · '+s.contribution : s.name;
  }
  @Builder note(value:string) {
    Text(value).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
      .padding({left:14,right:14,top:5,bottom:8})
  }
  @Builder line(label:string,value:string,id:string,action:()=>void) {
    Button({type:ButtonType.Normal}) {
      Row({space:8}) {
        Text(label).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1).textAlign(TextAlign.Start)
        if (value) Text(value).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.secondary).constraintSize({maxWidth:'45%'}).maxLines(2)
        SymbolGlyph(Symbols.expand).fontSize('11vp').fontColor([Colors.secondary])
      }.width('100%')
    }.width('100%').constraintSize({minHeight:46}).padding({left:15,right:15,top:5,bottom:5}).backgroundColor(Color.Transparent)
      .id(id).onClick(action)
  }
  @Builder calendar() {
    PhoneSectionTitle({text:tr('phone_meeting_calendar')})
    Column() {
      Row({space:10}) {
        Text(tr('meeting_reminders')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
        Toggle({type:ToggleType.Switch,isOn:this.local.enabled}).enabled(!this.local.busy).id('settings.meeting.reminder.toggle')
          .accessibilityText(tr('meeting_reminders')).onChange((v:boolean) => this.local.setEnabled(v))
      }.width('100%').constraintSize({minHeight:48}).padding({left:15,right:15,top:5,bottom:5})
      if (this.local.enabled && this.local.calendar!=='granted') this.line(tr(this.local.calendar==='denied' ? 'open_settings' : 'meeting_calendar_allow'),'',
        'settings.meeting.reminder.calendar',() => this.local.request('calendar'))
      if (this.local.enabled && !this.local.notifications) this.line(tr('meeting_notifications_allow'),'','settings.meeting.reminder.notification',() => this.local.request('notifications'))
    }.width('100%').backgroundColor(Colors.surface).borderRadius(12)
    this.note(tr(this.local.enabled && this.local.calendar!=='granted' ? 'meeting_calendar_needed' : 'meeting_reminder_footer'))
    if (this.local.error) Text(tr(this.local.error)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.danger)
      .width('100%').padding({left:14,right:14,top:3}).id('settings.meeting.reminder.status')
    if (this.local.enabled && this.local.scheduled>=0) this.note(tr(this.local.scheduled===0 ? 'meeting_reminder_none' : 'meeting_reminder_count').replace('{count}',String(this.local.scheduled)))
  }
  @Builder glossary() {
    Column({space:10}) {
      Text(tr('meeting_glossary_hint')).fontSize(14).fontColor(Colors.secondary).width('100%')
      TextArea({text:this.draft,placeholder:tr('meeting_glossary')}).layoutWeight(1).width('100%').onChange((v:string) => this.draft=v)
        .id('settings.meeting.glossary.input')
      if (this.speech.error) this.note(tr(this.speech.error))
      Button(tr('save')).enabled(!this.speech.saving && this.capturedScope===this.model.scope()).onClick(() => this.saveTerms())
        .id('settings.meeting.glossary.save')
    }.padding(16).width('100%').height('100%')
  }
  @Builder providers() {
    List() {
      ForEach(this.speech.choices,(service:MeetingSpeechService) => {
        ListItem() { this.line(this.serviceLabel(service),this.speech.extensionRef===service.ref ? '✓':'','settings.meeting.service.'+service.ref,
          () => { this.speech.choose('meeting_transcription_extension',service.ref).then((ok:boolean) => { if (ok) this.modal=false; }); }) }
      },(service:MeetingSpeechService)=>service.ref)
    }.padding(12).width('100%')
  }
  @Builder transcription() {
    PhoneSectionTitle({text:tr('meeting_transcription')})
    Column() {
      ForEach(['device','hub'],(mode:string) => {
        Button({type:ButtonType.Normal}) {
          Row() {
            Text(tr('meeting_mode_'+mode)).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
            if (this.speech.mode===mode) SymbolGlyph(Symbols.check).fontSize('16vp').fontColor([Colors.link])
          }.width('100%')
        }.width('100%').constraintSize({minHeight:46}).padding({left:15,right:15,top:5,bottom:5}).backgroundColor(Color.Transparent)
          .id('settings.meeting.transcription.'+mode).accessibilitySelected(this.speech.mode===mode).enabled(!this.speech.saving)
          .onClick(() => this.speech.choose('meeting_transcription_default',mode))
        Divider().strokeWidth(0.5).color(Colors.divider).margin({left:15})
      },(m:string)=>m)
      if (this.speech.choices.length>1) this.line(tr('meeting_service'),this.speech.serviceName() || tr('meeting_service_builtin'),
        'settings.meeting.service',()=>{ this.panel='providers';this.modal=true; })
      this.line(tr('meeting_glossary'),String(this.speech.glossary.length),'settings.meeting.glossary',() => this.editTerms())
    }.width('100%').backgroundColor(Colors.surface).borderRadius(12)
    this.note(tr(this.speech.mode==='device' ? 'meeting_privacy_device' : 'meeting_privacy_hub').replace('{service}',this.speech.serviceName() || tr('meeting_service_builtin')))
    if (this.speech.mode==='hub') this.note(tr(this.supportsAudio() ? 'meeting_start_opt_in' : 'meeting_privacy_this_phone'))
    if (this.speech.mode==='hub' && !this.speech.ready) this.note(tr('meeting_speech_not_ready'))
    if (this.speech.error) this.note(tr(this.speech.error))
  }
  @Builder networkPolicy() {
    Row({space:10}) {
      SymbolGlyph($r('sys.symbol.wifi')).fontSize('18vp').fontColor([Colors.link])
      Text(tr('meeting_wifi_only')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.ink).layoutWeight(1)
      if (this.networkReady) {
        Toggle({type:ToggleType.Switch,isOn:meetingAudioNetwork.wifiOnly}).id('settings.meeting.transcription.wifi')
          .enabled(!this.networkBusy).accessibilityText(tr('meeting_wifi_only')).onChange((value:boolean) => this.setWifi(value))
      } else if (!this.networkProblem) LoadingProgress().width(18).height(18)
    }.width('100%').constraintSize({minHeight:48}).padding({left:15,right:15,top:5,bottom:5}).backgroundColor(Colors.surface).borderRadius(12)
    this.note(tr('meeting_wifi_note'))
    if (this.networkProblem) this.line(tr('knowledge_storage_unavailable'),tr('retry'),'settings.meeting.transcription.wifi.retry',() => this.loadNetwork())
  }
  @Builder sheet() {
    if (this.panel==='glossary') this.glossary()
    else this.providers()
  }
  build() {
    Column({space:5}) {
      this.calendar()
      if (this.speech.offered) this.transcription()
      if (this.supportsAudio()) this.networkPolicy()
    }.width('100%').alignItems(HorizontalAlign.Start).id('settings.meeting.services')
    .bindSheet($$this.modal,this.sheet(),{title:{title:tr(this.panel==='glossary' ? 'meeting_glossary' : 'meeting_service')},
      height:this.panel==='glossary' ? SheetSize.LARGE : SheetSize.MEDIUM,showClose:true,onDisappear:() => { this.panel='';this.draft=''; }})
  }
}
