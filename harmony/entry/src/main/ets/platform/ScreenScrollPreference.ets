import { common } from '@kit.AbilityKit';
import { preferences } from '@kit.ArkData';
import { screenScrollChoices } from '../core/ScreenWheel';

@ObservedV2
export class ScreenScrollPreference {
  @Trace choice:string='default';
  @Trace saving:boolean=false;
  @Trace ready:boolean=false;
  private store:preferences.Preferences|undefined;
  load(context:common.Context|undefined):void {
    if (this.store || !context) return;
    try {
      const store=preferences.getPreferencesSync(context,{name:'persona_live_screen'});
      const saved=String(store.getSync('screen.scroll.speed','default'));
      this.choice=screenScrollChoices.includes(saved) ? saved : 'default';
      this.store=store;this.ready=true;
    } catch (_) { this.ready=false; }
  }
  async choose(value:string):Promise<boolean> {
    const store=this.store;
    if (!store || this.saving || !screenScrollChoices.includes(value)) return false;
    if (value===this.choice) return true;
    this.saving=true;
    try {
      store.putSync('screen.scroll.speed',value);await store.flush();this.choice=value;return true;
    } catch (_) {
      try { store.putSync('screen.scroll.speed',this.choice); } catch (_) {}
      return false;
    } finally { this.saving=false; }
  }
}
export const screenScrollPreference=new ScreenScrollPreference();
