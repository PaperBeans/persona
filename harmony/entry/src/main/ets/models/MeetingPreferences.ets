import type { ClientModel } from './ClientModel';
import { Wire } from './Wire';

export const MEETING_ROLES:string[]=['off','listener','participant'];
export function meetingRoleLabel(role:string):string {
  return role==='participant' ? 'phone_meeting_aloud' : role==='listener' ? 'phone_meeting_onscreen' : 'phone_meeting_off';
}
export function settingsMeetingRoleLabel(role:string):string {
  return role==='participant' ? 'phone_meeting_role_participant' : role==='listener' ? 'phone_meeting_role_listener' : 'phone_meeting_off';
}

/** The current assistant owns the default, just as it owns the meeting room. */
@ObservedV2
export class MeetingPreferences {
  @Trace role:string='off';
  @Trace ready:boolean=false;
  @Trace saving:boolean=false;
  @Trace error:string='';
  private version:number=0;
  private owner:ClientModel|undefined;

  close():void { this.version++; this.owner=undefined; this.role='off'; this.ready=false; this.saving=false; this.error=''; }
  async open(owner:ClientModel):Promise<void> {
    this.close(); this.owner=owner;
    if (!owner.connected || !owner.features.includes('meeting_attendance')) return;
    const n=this.version,generation=owner.connectionGeneration(),scope=owner.scope(),manager=owner.profile.manager;
    try {
      const data=await owner.repo().call('/api/v1/assistant');
      if (this.version!==n || !owner.owns(generation,scope) || owner.profile.manager!==manager) return;
      const role=Wire.text(data,'meeting_attendance_default');
      if (MEETING_ROLES.includes(role)) { this.role=role; this.ready=true; }
      else this.error='unsupported';
    } catch (_) { if (this.version===n && owner.owns(generation,scope)) this.error='failed'; }
  }
  async choose(role:string):Promise<void> {
    const owner=this.owner;
    if (!owner || !this.ready || this.saving || this.role===role || !MEETING_ROLES.includes(role)) return;
    const n=this.version,generation=owner.connectionGeneration(),scope=owner.scope(),manager=owner.profile.manager;
    const repo=owner.repo(),path='/api/v1/assistant',current=():boolean =>
      this.owner===owner && this.version===n && owner.owns(generation,scope) && owner.profile.manager===manager && owner.repo()===repo;
    if (owner.operationBlocked(path)) { this.error='pending_action'; return; }
    this.saving=true; this.error='';
    try {
      await repo.call(path,'PUT',Wire.payload(['meeting_attendance_default'],[role]),'',current);
      if (!current()) return;
      const confirmed=await repo.call(path);
      if (!current()) return;
      const actual=Wire.text(confirmed,'meeting_attendance_default');
      if (MEETING_ROLES.includes(actual)) this.role=actual;
      if (actual!==role) this.error='request_updated';
    } catch (failure) {
      if (current()) { const e=failure as Object as Record<string,Object>; this.error=Wire.bool(e,'resultUnknown') ? 'result_unknown' : 'failed'; }
    } finally { if (this.version===n) this.saving=false; }
  }
}
