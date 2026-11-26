import { common } from '@kit.AbilityKit';
import { preferences } from '@kit.ArkData';
import { notificationManager } from '@kit.NotificationKit';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { Colors } from '../design/Colors';
import { PhoneStyle, PhoneSectionTitle } from '../design/PhoneStyle';
import { tr, trError } from '../design/Strings';
import { DeviceFeatures, deviceFeatures } from '../models/DeviceFeatures';
import { PhoneAlarm, PhoneAlarmChanges, cancelNativeAlarm, nativeAlarmRows, phoneAlarmChanges, phoneAlarmGranted } from '../platform/NativePhoneAlarms';

@ComponentV2
export struct PhoneAlarmsSettings {
  @Param active:boolean=true;
  @Param elevated:boolean=false;
  @Local changes:PhoneAlarmChanges=phoneAlarmChanges;
  @Local features:DeviceFeatures=deviceFeatures;
  @Local timers:PhoneAlarm[]=[];
  @Local alarms:PhoneAlarm[]=[];
  @Local permission:string='loading';
  @Local error:string='';
  @Local busy:boolean=false;
  private context:common.UIAbilityContext|undefined;
  private store:preferences.Preferences|undefined;
  private visible:boolean=false;
  private serial:number=0;
  private expiryTimer:number=-1;
  private listened:boolean=false;
  private listening:boolean=false;
  private events:(states:reminderAgentManager.ReminderState[])=>void=()=>{this.refresh();};

  aboutToAppear():void {
    this.context=this.getUIContext().getHostContext() as common.UIAbilityContext;
    if (this.context) {try {this.store=preferences.getPreferencesSync(this.context,{name:'persona_phone_alarms'});} catch (_) {}}
    this.visible=true;this.refresh();
  }
  aboutToDisappear():void {this.visible=false;this.serial++;this.unwatch();}
  @Monitor('active','changes.revision') changed():void {
    if (this.active && this.visible) this.refresh();
    else {this.serial++;this.unwatch();}
  }
  private live(serial:number):boolean {return this.visible && this.active && this.serial===serial;}
  private unwatch():void {
    if (this.expiryTimer!==-1) clearTimeout(this.expiryTimer);
    this.expiryTimer=-1;
    if (this.listened) {this.listened=false;reminderAgentManager.unsubscribeReminderState(this.events).catch(()=>undefined);}
  }
  private async watch():Promise<void> {
    if (this.listened || this.listening) return;
    this.listening=true;
    try {
      await reminderAgentManager.subscribeReminderState(this.events);
      if (this.visible && this.active && this.permission==='granted') this.listened=true;
      else await reminderAgentManager.unsubscribeReminderState(this.events);
    } catch (_) {} finally { this.listening=false; }
  }
  private async refresh():Promise<void> {
    if (!this.active || !this.visible) return;
    const serial=++this.serial;this.error='';
    if (this.expiryTimer!==-1) clearTimeout(this.expiryTimer);
    this.expiryTimer=-1;
    try {
      const allowed=await phoneAlarmGranted();if (!this.live(serial)) return;
      if (!allowed) {
        this.permission=this.store?.getSync('asked',false)===true ? 'denied' : 'ask';
        this.timers=[];this.alarms=[];this.unwatch();return;
      }
      this.permission='granted';
      const rows=await nativeAlarmRows();if (!this.live(serial)) return;
      this.timers=rows.filter((row:PhoneAlarm)=>row.kind==='timer');
      this.alarms=rows.filter((row:PhoneAlarm)=>row.kind==='alarm');
      this.watch();
      if (rows.length) this.expiryTimer=setTimeout(()=>{if(this.live(serial))this.refresh();},5000);
    } catch (_) {if(this.live(serial))this.error='notifications_unavailable';}
  }
  private async authorize():Promise<void> {
    const ctx=this.context;
    if (!ctx || this.busy || !this.active || !this.visible) return;
    this.busy=true;
    try {
      if (this.permission==='denied') await notificationManager.openNotificationSettingsWithResult(ctx);
      else {
        this.store?.putSync('asked',true);
        if (this.store) await this.store.flush();
        await notificationManager.requestEnableNotification(ctx);
      }
    } catch (_) {} finally {
      this.busy=false;this.features.context();this.refresh();
    }
  }
  private confirmDelete(row:PhoneAlarm):void {
    if (this.busy || !this.visible || !this.active) return;
    this.getUIContext().showAlertDialog({title:row.label,message:tr(row.kind==='timer' ? 'phone_delete_timer_note' : 'phone_delete_alarm_note'),
      primaryButton:{value:tr('cancel'),action:()=>{}},secondaryButton:{value:tr(row.kind==='timer' ? 'phone_delete_timer':'phone_delete_alarm'),
        action:()=>{this.remove(row);}}});
  }
  private async remove(row:PhoneAlarm):Promise<void> {
    if (this.busy || !this.active || !this.visible) return;
    this.busy=true;
    try {
      await cancelNativeAlarm(row,()=>this.active && this.visible && this.permission==='granted');
      await this.refresh();
    } catch (_) {if(this.visible && this.active)this.error='phone_alarm_changed';}
    finally {this.busy=false;}
  }
  private value(row:PhoneAlarm):string {
    if (row.kind==='alarm') return row.time.includes('T') ? new Date(row.time).toLocaleString() : row.time+
      (row.days.length ? ' · '+row.days.map((day:string)=>tr('phone_alarm_day_'+day)).join(' ') : '');
    return row.endsAt && Number.isFinite(Date.parse(row.endsAt)) ? new Date(row.endsAt).toLocaleTimeString() :
      tr('phone_timer_seconds').replace('{seconds}',String(row.seconds));
  }
  @Builder group(title:string,items:PhoneAlarm[]) {
    ListItem() {PhoneSectionTitle({text:title})}
    ForEach(items,(row:PhoneAlarm)=>{
      ListItem() { Row({space:12}) {
        Column({space:4}) {
          Text(row.label).fontSize(17).fontColor(Colors.ink)
          Text(this.value(row)).fontSize(14).fontColor(Colors.secondary)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        Button(tr('delete'),{buttonStyle:ButtonStyleMode.TEXTUAL}).fontColor(Colors.danger).enabled(!this.busy)
          .onClick(()=>this.confirmDelete(row)).id('settings.alarms.delete.'+row.id)
      }.width('100%').padding(16).backgroundColor(Colors.card(this.elevated)).borderRadius(PhoneStyle.cardRadius) }
    },(row:PhoneAlarm)=>row.identity())
  }
  build() {
    List({space:8}) {
      if (this.error) { ListItem() {Column({space:8}) {
        Text(trError(this.error)).fontColor(Colors.danger)
        Button(tr('retry'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.refresh())
      }.width('100%')} }
      if (this.permission==='loading') {ListItem() {LoadingProgress().width(28).height(28)}}
      else if (this.permission==='ask' || this.permission==='denied') {
        ListItem() {Column({space:12}) {
          Text(tr('phone_alarm_access_note')).fontSize(16).fontColor(Colors.secondary)
          Button(tr(this.permission==='ask' ? 'phone_alarm_allow' : 'phone_alarm_open_settings'),{buttonStyle:ButtonStyleMode.TEXTUAL})
            .enabled(!this.busy).onClick(()=>this.authorize()).id('settings.alarms.access')
        }.width('100%').padding(18)}
      } else if (!this.error && !this.alarms.length && !this.timers.length) {
        ListItem() {Column({space:8}) {
          Text(tr('phone_alarm_empty')).fontSize(20).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
          Text(tr('phone_alarm_empty_note')).fontSize(15).fontColor(Colors.secondary)
        }.width('100%').padding(22).id('settings.alarms.empty')}
      } else {
        if (this.timers.length) this.group(tr('phone_native_timers'),this.timers)
        if (this.alarms.length) this.group(tr('phone_native_alarms_only'),this.alarms)
      }
    }.width('100%').height('100%').padding({left:16,right:16,top:12,bottom:24}).backgroundColor(Colors.listGroup(this.elevated)).id('settings.alarms.page')
  }
}
