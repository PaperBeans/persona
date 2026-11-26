import { common } from '@kit.AbilityKit';
import { preferences } from '@kit.ArkData';
import { NativeRecording } from './NativeRecording';

@ObservedV2
export class MeetingEchoPreference {
  @Trace enabled:boolean=false;
  @Trace supported:boolean=false;
  @Trace saving:boolean=false;
  @Trace failed:boolean=false;
  private store:preferences.Preferences|undefined;

  load(context:common.Context|undefined):void {
    this.supported=NativeRecording.supportsEchoCancellation();
    if (this.store || !context) return;
    try {
      this.store=preferences.getPreferencesSync(context,{name:'persona_meeting_audio'});
      this.enabled=this.store.getSync('echo_with_speaker',false)===true;
    } catch (_) { this.failed=true; }
  }

  async choose(enabled:boolean):Promise<void> {
    const store=this.store;
    if (!store || !this.supported || this.saving || enabled===this.enabled) return;
    this.saving=true; this.failed=false;
    try { store.putSync('echo_with_speaker',enabled); await store.flush(); this.enabled=enabled; }
    catch (_) { this.failed=true; try { store.putSync('echo_with_speaker',this.enabled); } catch (_) {} }
    finally { this.saving=false; }
  }
}

export const meetingEcho=new MeetingEchoPreference();
