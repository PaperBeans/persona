import { describe, it, expect } from '@ohos/hypium';
import { util } from '@kit.ArkTS';
import { reminderAgentManager } from '@kit.BackgroundTasksKit';
import { PhoneAlarm, nativeAlarmPlan, nativeAlarmRows, cancelNativeAlarm, phoneAlarmGranted } from '../../../main/ets/platform/NativePhoneAlarms';
import { Wire } from '../../../main/ets/models/Wire';
import { nativeTestContext } from './NativeContext';
import { fixtureArgument } from './NativeTransport.test';

export default function nativeAlarmsTests():void {
  describe('NativePersonaAlarms',():void=>{
    it('reads_a_real_isolated_timer_and_alarm_or_the_exact_OS_publish_refusal_then_restores_the_native_set',0,async():Promise<void>=>{
      if (fixtureArgument('alarmFixture')!=='synthetic') throw new Error('isolated_fixture_required');
      const context=nativeTestContext();
      if (!context.abilityInfo.bundleName.endsWith('.visualqa')) throw new Error('isolated_bundle_required');
      const label='Persona QA '+util.generateRandomUUID();
      const before=(await reminderAgentManager.getAllValidReminders()).map((r:reminderAgentManager.ReminderInfo)=>r.reminderId).sort();
      const observed=fixtureArgument('alarmPolicy');
      if (!['allowed','quota','notifications-off'].includes(observed)) throw new Error('explicit_platform_policy_required');
      const granted=await phoneAlarmGranted();expect(granted).assertEqual(observed!=='notifications-off');
      const idTag=(row:PhoneAlarm):boolean=>row.label===label+' timer' || row.label===label+' alarm';
      try {
        const timer=await nativeAlarmPlan(context,'phone_timer_start',Wire.payload(['seconds','label'],[3600,label+' timer']));
        let denied=false;try {await timer.execute(()=>false);} catch (_) {denied=true;}
        expect(denied).assertTrue();expect((await nativeAlarmRows()).some(idTag)).assertFalse();
        if (observed==='notifications-off') {
          let error='';try {await timer.execute(()=>true);} catch (e) {error=(e as Error).message;}
          expect(error).assertEqual('notifications_denied');return;
        }
        if (observed==='quota') {
          let code=0;try {await timer.execute(()=>true);} catch (e) {code=Wire.number(e as Object as Record<string,Object>,'code');}
          expect(code).assertEqual(1700002);return;
        }
        const made=await timer.execute(()=>true);
        const later=new Date(Date.now()+2*3600000).toISOString();
        const plan=await nativeAlarmPlan(context,'phone_alarm_create',Wire.payload(['time','label'],[later,label+' alarm']));
        const clock=await plan.execute(()=>true);
        const rows=(await nativeAlarmRows()).filter(idTag);
        expect(rows.length).assertEqual(2);
        const storedTimer=rows.find((row:PhoneAlarm)=>row.id===Wire.text(made,'id'));
        const storedClock=rows.find((row:PhoneAlarm)=>row.id===Wire.text(clock,'id'));
        if (!storedTimer || !storedClock) throw new Error('native_alarm_identity_mismatch');
        expect(storedTimer.kind).assertEqual('timer');expect(storedTimer.seconds).assertEqual(3600);
        expect(storedClock.kind).assertEqual('alarm');expect(Math.abs(Date.parse(storedClock.time)-Date.parse(later))<1000).assertTrue();
        const remove=await nativeAlarmPlan(context,'phone_alarm_cancel',Wire.payload(['id'],[storedTimer.id]));
        let cancelled=false;try {await remove.execute(()=>false);} catch (_) {cancelled=true;}
        expect(cancelled).assertTrue();expect((await nativeAlarmRows()).filter(idTag).length).assertEqual(2);
        await remove.execute(()=>true);expect((await nativeAlarmRows()).filter(idTag).length).assertEqual(1);
      } finally {
        for (const row of (await nativeAlarmRows()).filter(idTag)) await cancelNativeAlarm(row,()=>true);
        const after=(await reminderAgentManager.getAllValidReminders()).map((r:reminderAgentManager.ReminderInfo)=>r.reminderId).sort();
        expect(JSON.stringify(after)).assertEqual(JSON.stringify(before));
      }
    });
  });
}
