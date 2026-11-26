import { common } from '@kit.AbilityKit';
import { preferences } from '@kit.ArkData';

@ObservedV2
export class DictationPreference {
  @Trace choice: string = 'auto';
  @Trace saving: boolean = false;
  last: string = '';
  private store: preferences.Preferences | undefined;

  load(context: common.Context | undefined): void {
    if (this.store || !context) return;
    try {
      const store = preferences.getPreferencesSync(context, {name: 'persona_dictation'});
      const saved = String(store.getSync('choice', 'auto'));
      this.choice = ['auto','zh-CN','keyboard'].includes(saved) ? saved : 'auto';
      const last = String(store.getSync('lastLocale', ''));
      this.last = ['zh-CN','ja-JP','en-US'].includes(last) ? last : '';
      this.store = store;
    } catch (_) { /* The automatic choice remains usable on a read-only device. */ }
  }

  async choose(value: string): Promise<boolean> {
    const store = this.store;
    if (!store || this.saving || !['auto', 'zh-CN', 'keyboard'].includes(value)) return false;
    if (this.choice === value) return true;
    this.saving = true;
    try {
      store.putSync('choice', value);
      await store.flush();
      this.choice = value;
      return true;
    } catch (_) {
      try { store.putSync('choice', this.choice); } catch (_) {}
      return false;
    } finally { this.saving = false; }
  }

  remember(locale: string): void {
    if (this.last === locale || !['zh-CN','ja-JP','en-US'].includes(locale)) return;
    this.last = locale;
    if (this.store) {
      try { this.store.putSync('lastLocale', locale); this.store.flush().catch(() => {}); } catch (_) {}
    }
  }
}

export const dictationPreference = new DictationPreference();
