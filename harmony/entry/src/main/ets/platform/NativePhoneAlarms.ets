import { common } from '@kit.AbilityKit';
import { util } from '@kit.ArkTS';
import { notificationManager } from '@kit.NotificationKit';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { Wire } from '../models/Wire';
import { tr } from '../design/Strings';
import type { NativePhoneWritePlan } from './NativePhoneWrites';

const groupPrefix='persona.phone.alarm.';
const weekdays:string[]=['mon','tue','wed','thu','fri','sat','sun'];
const alarmType=reminderAgentManager.ReminderType.REMINDER_TYPE_ALARM;
const timerType=reminderAgentManager.ReminderType.REMINDER_TYPE_TIMER;
const calendarType=reminderAgentManager.ReminderType.REMINDER_TYPE_CALENDAR;
@ObservedV2
export class PhoneAlarmChanges { @Trace revision:number=0; }
export const phoneAlarmChanges=new PhoneAlarmChanges();
export async function phoneAlarmGranted():Promise<boolean> {
  try { return canIUse('SystemCapability.Notification.ReminderAgent') && await notificationManager.isNotificationEnabled(); }
  catch (_) { return false; }
}

export class PhoneAlarm {
  id:string='';
  kind:string='';
  label:string='';
  time:string='';
  days:string[]=[];
  seconds:number=0;
  endsAt:string='';
  nativeId:number=-1;
  group:string='';
  identity():string {
    return JSON.stringify([this.nativeId,this.id,this.group,this.kind,this.label,this.time,this.days,this.seconds,this.endsAt]);
  }
  wire():Record<string,Object> {
    const data=Wire.payload(['id','label','state'],[this.id,this.label,this.kind==='timer' ? 'countdown':'scheduled']);
    if (this.kind==='timer') {
      data['timer_seconds']=this.seconds;
      if (this.endsAt) data['ends_at']=this.endsAt;
    } else {
      const schedule=this.time.includes('T') ? Wire.payload(['at'],[this.time]) : Wire.payload(['time'],[this.time]);
      if (this.days.length) schedule['repeat']=this.days;
      data['schedule']=schedule;
    }
    return data;
  }
}

function pad(value:number):string { return String(value).padStart(2,'0'); }
function checkCurrent(admit:()=>boolean):void { if (!admit()) throw new Error('phone_action_cancelled'); }

function fromReminder(item:reminderAgentManager.ReminderInfo):PhoneAlarm|undefined {
  const req=item.reminderReq,group=req.groupId || '',owned=group.startsWith(groupPrefix);
  if (req.reminderType!==alarmType && req.reminderType!==timerType && !(req.reminderType===calendarType && owned)) return undefined;
  const alarm=new PhoneAlarm();alarm.nativeId=item.reminderId;alarm.group=group;
  alarm.id=owned ? group.slice(groupPrefix.length) : 'native-'+String(item.reminderId);
  alarm.kind=req.reminderType===timerType ? 'timer':'alarm';
  alarm.label=req.title || tr(alarm.kind==='timer' ? 'phone_native_timer':'phone_native_alarm');
  if (req.reminderType===timerType) {
    alarm.seconds=(req as reminderAgentManager.ReminderRequestTimer).triggerTimeInSeconds;
    const params=req.wantAgent?.parameters;
    if (params) alarm.endsAt=Wire.text(params,'ends_at');
  } else if (req.reminderType===alarmType) {
    const clock=req as reminderAgentManager.ReminderRequestAlarm;
    alarm.time=pad(clock.hour)+':'+pad(clock.minute);
    alarm.days=(clock.daysOfWeek || []).filter((d:number)=>d>=1 && d<=7).map((d:number)=>weekdays[d-1]);
  } else {
    const date=(req as reminderAgentManager.ReminderRequestCalendar).dateTime;
    if (!date) return undefined;
    alarm.time=new Date(date.year,date.month-1,date.day,date.hour,date.minute,date.second || 0).toISOString();
  }
  return alarm;
}

export async function nativeAlarmRows():Promise<PhoneAlarm[]> {
  const reminders=await reminderAgentManager.getAllValidReminders();
  const rows:PhoneAlarm[]=[];
  for (const item of reminders) { const row=fromReminder(item);if (row) rows.push(row); }
  return rows;
}

export async function cancelNativeAlarm(shown:PhoneAlarm,admit:()=>boolean):Promise<void> {
  const current=(await nativeAlarmRows()).find((row:PhoneAlarm)=>row.id===shown.id);
  checkCurrent(admit);
  if (!current || current.identity()!==shown.identity()) throw new Error('phone_alarm_changed');
  await reminderAgentManager.cancelReminder(current.nativeId);phoneAlarmChanges.revision++;
}

function nativeAlarmSpec(args:Record<string,Object>,timer:boolean):PhoneAlarm {
  const keys=timer ? ['seconds','label'] : ['time','repeat','label'];
  if (Object.keys(args).some((key:string)=>!keys.includes(key))) throw new Error('invalid_phone_argument');
  const raw=args['label'];
  if (Wire.has(args,'label') && (typeof raw!=='string' || raw.length>160 || raw.includes('\u0000'))) throw new Error('invalid_phone_argument');
  const spec=new PhoneAlarm();spec.id=util.generateRandomUUID();spec.group=groupPrefix+spec.id;
  spec.kind=timer ? 'timer' : 'alarm';spec.label=Wire.text(args,'label').trim() || tr(timer ? 'phone_native_timer':'phone_native_alarm');
  if (timer) {
    spec.seconds=Wire.number(args,'seconds',-1);
    if (!Number.isInteger(spec.seconds) || spec.seconds<1 || spec.seconds>86400) throw new Error('invalid_phone_argument');
    return spec;
  }
  const time=Wire.text(args,'time'),days=args['repeat'];
  if (days!==undefined && (!Array.isArray(days) || days.some((day:Object)=>typeof day!=='string' || !weekdays.includes(day as string)))) throw new Error('invalid_phone_argument');
  spec.days=weekdays.filter((day:string)=>Array.isArray(days) && days.includes(day));
  if (/^\d{1,2}:\d{2}$/.test(time)) {
    const parts=time.split(':'),h=Number(parts[0]),m=Number(parts[1]);
    if (h>23 || m>59) throw new Error('invalid_phone_argument');
    spec.time=pad(h)+':'+pad(m);return spec;
  }
  const epoch=Date.parse(time);
  if (spec.days.length || !/^\d{4}-\d{2}-\d{2}T.*(?:Z|[+-]\d{2}:\d{2})$/i.test(time) || !Number.isFinite(epoch) || epoch<=Date.now()) throw new Error('invalid_phone_argument');
  spec.time=new Date(epoch).toISOString();return spec;
}

async function publish(context:common.UIAbilityContext,spec:PhoneAlarm,admit:()=>boolean):Promise<Record<string,Object>> {
  if (!await notificationManager.isNotificationEnabled()) throw new Error('notifications_denied');
  checkCurrent(admit);
  if (spec.kind==='timer') spec.endsAt=new Date(Date.now()+spec.seconds*1000).toISOString();
  const params=Wire.payload(['personaAlarm','ends_at'],[spec.id,spec.endsAt]);
  const want:reminderAgentManager.WantAgent={pkgName:context.abilityInfo.bundleName,abilityName:context.abilityInfo.name,parameters:params};
  let request:reminderAgentManager.ReminderRequest;
  if (spec.kind==='timer') {
    const timer:reminderAgentManager.ReminderRequestTimer={reminderType:timerType,triggerTimeInSeconds:spec.seconds};
    request=timer;
  } else if (spec.time.includes('T')) {
    const t=new Date(spec.time);
    if (t.getTime()<=Date.now()) throw new Error('invalid_phone_argument');
    const fixed:reminderAgentManager.ReminderRequestCalendar={reminderType:calendarType,dateTime:{
      year:t.getFullYear(),month:t.getMonth()+1,day:t.getDate(),hour:t.getHours(),minute:t.getMinutes(),second:t.getSeconds()}};
    request=fixed;
  } else {
    const clock:reminderAgentManager.ReminderRequestAlarm={reminderType:alarmType,hour:Number(spec.time.slice(0,2)),minute:Number(spec.time.slice(3)),
      daysOfWeek:spec.days.map((day:string)=>weekdays.indexOf(day)+1)};
    request=clock;
  }
  request.groupId=spec.group;request.title=spec.label;request.content=spec.label;request.wantAgent=want;
  request.ringDuration=30;request.slotType=notificationManager.SlotType.SERVICE_INFORMATION;
  const live=await reminderAgentManager.getAllValidReminders();
  const used=new Set<number>();for (const item of live) used.add(item.reminderReq.notificationId || 0);
  let notification=0x50000000+parseInt(spec.id.slice(0,7),16);
  while(used.has(notification)) notification=notification===0x5fffffff ? 0x50000000 : notification+1;
  request.notificationId=notification;checkCurrent(admit);
  spec.nativeId=await reminderAgentManager.publishReminder(request);phoneAlarmChanges.revision++;
  const result=spec.wire();if (spec.kind==='timer') result['seconds']=spec.seconds;
  return result;
}

export async function nativeAlarmPlan(context:common.UIAbilityContext,name:string,args:Record<string,Object>):Promise<NativePhoneWritePlan> {
  const empty:Record<string,Object>={};
  if (name==='phone_alarm_cancel') {
    const id=Wire.text(args,'id');
    if (!id || Object.keys(args).length!==1) throw new Error('invalid_phone_argument');
    const shown=(await nativeAlarmRows()).find((row:PhoneAlarm)=>row.id===id);
    if (!shown) throw new Error('phone_alarm_changed');
    const result=Wire.payload(['id','cancelled'],[id,true]);
    return {before:shown.wire(),after:result,execute:async(admit:()=>boolean):Promise<Record<string,Object>>=>{
      await cancelNativeAlarm(shown,admit);return result;
    }};
  }
  if (name!=='phone_alarm_create' && name!=='phone_timer_start') throw new Error('no_capability');
  const spec=nativeAlarmSpec(args,name==='phone_timer_start');
  return {before:empty,after:spec.wire(),execute:(admit:()=>boolean):Promise<Record<string,Object>>=>publish(context,spec,admit)};
}
