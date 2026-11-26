import { Tokens } from '../design/Tokens';
import { ClientModel } from '../models/ClientModel';
import { MEETING_ROLES, MeetingPreferences, settingsMeetingRoleLabel } from '../models/MeetingPreferences';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { PhoneSectionTitle, PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { meetingEcho, MeetingEchoPreference } from '../platform/MeetingEchoPreference';
import { PhoneMeetingServices } from './PhoneMeetingServices';

@ComponentV2
export struct PhoneMeetingSettings {
  @Require @Param model:ClientModel;
  @Local preferences:MeetingPreferences=new MeetingPreferences();
  @Local echo:MeetingEchoPreference=meetingEcho;
  aboutToAppear():void { this.echo.load(this.getUIContext().getHostContext()); this.preferences.open(this.model).catch(() => {}); }
  aboutToDisappear():void { this.preferences.close(); }
  @Monitor('model.profile','model.connected','model.features') ownerChanged():void { this.preferences.open(this.model).catch(() => {}); }

  private conversation():boolean { return this.model.features.includes('meeting_conversation'); }

  @Builder limit(key:string,glyph:Resource) {
    Row({space:12}) {
      SymbolGlyph(glyph).fontSize('18vp').fontColor([Colors.link]).width(22)
      Text(tr(key)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).layoutWeight(1)
    }.width('100%').constraintSize({minHeight:44}).padding({left:15,right:15,top:8,bottom:8}).accessibilityGroup(true)
  }

  build() {
    Scroll() { Column({space:6}) {
      PhoneSectionTitle({text:tr('phone_meeting_role')})
      if (this.preferences.ready) {
        if (this.conversation()) {
          Row({space:12}) {
            Text(tr('phone_meeting_auto_invite')).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.ink).layoutWeight(1)
            Toggle({type:ToggleType.Switch,isOn:this.preferences.role!=='off'}).enabled(!this.preferences.saving)
              .accessibilityText(tr('phone_meeting_auto_invite')).onChange((on:boolean)=>this.preferences.choose(on ? 'listener' : 'off')).id('settings.meetings.invite')
          }.width('100%').constraintSize({minHeight:48}).padding({left:16,right:16}).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
          Text(tr(this.preferences.role==='off' ? 'phone_meeting_auto_off_note' : 'phone_meeting_auto_on_note')).fontSize(13)
            .maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
        } else {
          Column() {
          ForEach(MEETING_ROLES,(role:string,i:number) => {
            Button({type:ButtonType.Normal}) {
              Row({space:8}) {
                Text(tr(settingsMeetingRoleLabel(role))).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.ink).layoutWeight(1)
                if (this.preferences.role===role) SymbolGlyph(Symbols.check).fontSize('17vp').fontColor([Colors.link])
              }.width('100%')
            }.width('100%').constraintSize({minHeight:48}).padding({left:16,right:16,top:7,bottom:7}).backgroundColor(Color.Transparent)
              .enabled(!this.preferences.saving).accessibilitySelected(this.preferences.role===role)
              .onClick(() => this.preferences.choose(role)).id('settings.meetings.role.'+role)
            if (i<MEETING_ROLES.length-1) Divider().strokeWidth(0.5).color(Colors.divider).margin({left:16})
          },(role:string) => role)
        }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
          Text(tr('phone_meeting_role_note_'+this.preferences.role)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
            .fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
          Text(tr('phone_meeting_default_note')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
        }
      } else if (!this.model.connected || !this.model.features.includes('meeting_attendance') || this.preferences.error) {
        Text(tr(!this.model.connected ? 'offline' : this.preferences.error || 'phone_meeting_unavailable')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.link).onClick(() => this.preferences.open(this.model))
      } else LoadingProgress().width(26).height(26)
      if (this.preferences.ready && this.preferences.error) Text(tr(this.preferences.error)).fontSize(14).maxFontScale(Tokens.interfaceScaleFor(14)).fontColor(Colors.danger)
      PhoneMeetingServices({model:this.model})
      if (this.model.features.includes('meeting_attendance')) {
        PhoneSectionTitle({text:tr('phone_meeting_in_front')})
        Column() {
          this.limit('phone_meeting_material',Symbols.calendar)
          Divider().strokeWidth(0.5).color(Colors.divider).margin({left:49})
          this.limit('phone_meeting_privacy',$r('sys.symbol.lock_shield'))
          Divider().strokeWidth(0.5).color(Colors.divider).margin({left:49})
          this.limit('phone_meeting_explicit_voice',$r('sys.symbol.questionmark_circle'))
        }.width('100%').backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius).id('settings.meetings.limits')
        Text(tr('phone_meeting_limits_note')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
          .fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
      }
      if (this.conversation()) {
        PhoneSectionTitle({text:tr('phone_meeting_experimental')})
        Row({space:12}) {
          Text(tr('phone_meeting_echo')).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.ink).layoutWeight(1)
          Toggle({type:ToggleType.Switch,isOn:this.echo.enabled && this.echo.supported}).enabled(this.echo.supported && !this.echo.saving)
            .accessibilityText(tr('phone_meeting_echo')).onChange((on:boolean)=>this.echo.choose(on)).id('settings.meetings.echo')
        }.width('100%').constraintSize({minHeight:48}).padding({left:16,right:16}).backgroundColor(Colors.surface).borderRadius(PhoneStyle.cardRadius)
        Text(tr(this.echo.failed ? 'draft_save_failed' : this.echo.supported ? 'phone_meeting_echo_note' : 'phone_meeting_echo_unavailable'))
          .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%').padding({left:12,right:12})
      }
    }.width('100%').alignItems(HorizontalAlign.Start).padding(16) }.width('100%').height('100%').align(Alignment.TopStart)
      .backgroundColor(Colors.grouped).id('settings.meetings.options')
  }
}
