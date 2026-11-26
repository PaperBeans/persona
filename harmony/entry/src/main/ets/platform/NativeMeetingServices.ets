import { abilityAccessCtrl, common, Permissions } from '@kit.AbilityKit';
import { calendarManager } from '@kit.CalendarKit';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { notificationManager } from '@kit.NotificationKit';
import { LocalStore } from './LocalStore';
import { MeetingOccurrence, meetingReminderFailure, MeetingReminderQueue, MeetingReminderSystem, PublishedMeetingReminder } from '../models/MeetingCalendarReminders';
import { Wire } from '../models/Wire';
import { tr } from '../design/Strings';

const group='persona.meeting.reminder';
const readCalendar:Permissions='ohos.permission.READ_CALENDAR';
const pref='phone.meeting.reminders';

export class HarmonyMeetingCalendar implements MeetingReminderSystem {
  private context:common.UIAbilityContext;
  constructor(context:common.UIAbilityContext) { this.context=context; }
  async calendar():Promise<MeetingOccurrence[]> { const now=Date.now();return this.occurrences(now,now+36*3600000); }
  async occurrences(now:number,end:number,calendarId?:number):Promise<MeetingOccurrence[]> {
    const calendars=await calendarManager.getCalendarManager(this.context).getAllCalendars();
    const selected=calendarId===undefined ? calendars : calendars.filter((c:calendarManager.Calendar) => c.id===calendarId);
    const lists=await Promise.all(selected.map(async (cal:calendarManager.Calendar):Promise<MeetingOccurrence[]> => {
      const user=cal.getAccount().name.trim().toLowerCase();
      const events=await cal.queryEventInstances(now,end,undefined,['id','identifier','title','startTime','endTime','instanceStartTime','instanceEndTime','isAllDay','attendee']);
      return events.map((e:calendarManager.Event):MeetingOccurrence => {
        const item=new MeetingOccurrence();item.title=(e.title || '').trim();
        item.id=e.id!==undefined ? String(cal.id)+'/'+String(e.id) : e.identifier || '';
        item.start=e.instanceStartTime || e.startTime;item.end=e.instanceEndTime || e.endTime;
        item.allDay=e.isAllDay===true;item.declined=!!user && (e.attendee || []).some((a:calendarManager.Attendee) =>
          a.email.trim().toLowerCase()===user && a.status===calendarManager.AttendeeStatus.DECLINED);
        return item;
      });
    }));return lists.flat();
  }
  async existing():Promise<PublishedMeetingReminder[]> {
    const reminders=await reminderAgentManager.getAllValidReminders(), result:PublishedMeetingReminder[]=[];
    for (const r of reminders) {
      if (!(r.reminderReq.groupId || '').startsWith(group+'.')) continue;
      const params=r.reminderReq.wantAgent?.parameters || {};
      if (Wire.text(params,'personaMeetingType')!==group) continue;
      result.push(new PublishedMeetingReminder(r.reminderId,Wire.text(params,'occurrence'),r.reminderReq.title || ''));
    }
    return result;
  }
  async cancel(id:number):Promise<void> {
    try { await reminderAgentManager.cancelReminder(id); }
    catch (error) { if (Wire.number(error as Object as Record<string,Object>,'code')!==1700003) throw error as Error; }
  }
  async publish(event:MeetingOccurrence):Promise<void> {
    const when=new Date(event.start-60000), key=event.key();
    const clock=new Date(event.start).toLocaleTimeString(undefined,{hour:'2-digit',minute:'2-digit'});
    let hash=0; for (let i=0;i<key.length;i++) hash=(Math.imul(hash,31)+key.charCodeAt(i))|0;
    const used=new Set<number>();
    for (const item of await reminderAgentManager.getAllValidReminders()) used.add(item.reminderReq.notificationId || 0);
    // A reminder group is cancelled as a unit by the OS. Each occurrence owns its own group and notification.
    let id=0x60000000+(hash>>>0)%0x0fffffff;while (used.has(id)) id=id===0x6fffffff ? 0x60000000 : id+1;
    const parameters=Wire.payload(['personaMeetingType','occurrence'],[group,key]);
    const request:reminderAgentManager.ReminderRequestCalendar={
      reminderType:reminderAgentManager.ReminderType.REMINDER_TYPE_CALENDAR,
      dateTime:{year:when.getFullYear(),month:when.getMonth()+1,day:when.getDate(),hour:when.getHours(),minute:when.getMinutes(),second:when.getSeconds()},
      groupId:group+'.'+String(id),notificationId:id,title:event.title,content:tr('meeting_reminder_body').replace('{time}',clock),
      slotType:notificationManager.SlotType.SERVICE_INFORMATION,tapDismissed:true,ringDuration:0,
      wantAgent:{pkgName:this.context.abilityInfo.bundleName,abilityName:this.context.abilityInfo.name,parameters:parameters}
    };
    await reminderAgentManager.publishReminder(request);
  }
}

@ObservedV2
export class NativeMeetingServices {
  @Trace enabled:boolean=true;
  @Trace calendar:string='unknown';
  @Trace notifications:boolean=false;
  @Trace scheduled:number=-1;
  @Trace error:string='';
  @Trace pendingOpen:boolean=false;
  @Trace busy:boolean=false;
  private context:common.UIAbilityContext|undefined;
  private store:LocalStore=new LocalStore();
  private initialized:Promise<void>|undefined;
  private system:HarmonyMeetingCalendar|undefined;
  private queue:MeetingReminderQueue=new MeetingReminderQueue();
  private recording:string='';
  private update:number=0;

  async initialize(context:common.UIAbilityContext):Promise<void> {
    if (this.context!==context || !this.initialized) {
      this.context=context;this.system=new HarmonyMeetingCalendar(context);
      this.initialized=this.store.open(context).then(async ():Promise<void> => {
        this.enabled=(await this.store.get(pref))!=='false';
      }).catch((e:Error):void => { this.initialized=undefined; throw e; });
    }
    await this.initialized;await this.refresh();
  }
  private consent():string {
    const state=abilityAccessCtrl.createAtManager().getSelfPermissionStatus(readCalendar);
    if (state===abilityAccessCtrl.PermissionStatus.GRANTED) return 'granted';
    if (state===abilityAccessCtrl.PermissionStatus.NOT_DETERMINED) return 'ask';
    return 'denied';
  }
  async refresh():Promise<void> {
    if (!this.initialized || !this.system) return;
    await this.initialized;
    const rev=++this.update; let state='unknown',enabled=false;
    try { state=this.consent();enabled=await notificationManager.isNotificationEnabled(); } catch (_) {}
    if (rev!==this.update) return;
    this.calendar=state;this.notifications=enabled;this.error='';
    try { await this.queue.refresh(this.system,() => this.enabled && this.calendar==='granted' && this.notifications,() => this.recording,() => Date.now()); }
    catch (error) {
      if (rev===this.update) {
        const code=Wire.number(error as Object as Record<string,Object>,'code');
        if (code===1700001) this.notifications=false;
        this.error=meetingReminderFailure(code);
      }
    }
    try { const active=await this.system.existing(); if (rev===this.update) this.scheduled=active.length; }
    catch (_) { if (rev===this.update) this.scheduled=-1; }
  }
  async today(context:common.UIAbilityContext):Promise<MeetingOccurrence[]> {
    await this.initialize(context);
    if (this.calendar==='ask') await this.request('calendar');
    if (this.calendar!=='granted' || !this.system) throw new Error('permission_denied');
    const now=Date.now(), end=new Date(now);end.setHours(24,0,0,0);
    const items=await this.system.occurrences(now,end.getTime());
    return items.filter((e:MeetingOccurrence) => !!e.title && !e.allDay && !e.declined && e.end>now && e.start<end.getTime())
      .sort((a:MeetingOccurrence,b:MeetingOccurrence) => a.start-b.start);
  }
  async setEnabled(value:boolean):Promise<void> {
    if (this.busy || !this.initialized) return;
    this.busy=true; try { await this.store.put(pref,String(value));this.enabled=value;await this.refresh(); }
    catch (_) { this.error='meeting_reminder_unavailable'; } finally { this.busy=false; }
  }
  async request(kind:string):Promise<void> {
    if (!this.context || this.busy) return;
    this.busy=true;
    try {
      if (kind==='calendar') {
        const manager=abilityAccessCtrl.createAtManager();
        if (this.consent()==='ask') await manager.requestPermissionsFromUser(this.context,[readCalendar]);
        else if (this.consent()!=='granted') await manager.requestPermissionOnSetting(this.context,[readCalendar]);
      } else if (kind==='notifications') {
        try { await notificationManager.requestEnableNotification(this.context); }
        catch (_) { await notificationManager.openNotificationSettings(this.context); }
      }
    } catch (_) { this.error='permission_denied'; }
    finally { this.busy=false; await this.refresh(); }
  }
  setRecordingEvent(id:string):void { if (id!==this.recording) { this.recording=id;this.refresh().catch(() => {}); } }
  receive(parameters:Record<string,Object>):void {
    if (Wire.text(parameters,'personaMeetingType')===group && Wire.text(parameters,'occurrence')) this.pendingOpen=true;
  }
}

export const nativeMeetingServices:NativeMeetingServices=new NativeMeetingServices();
