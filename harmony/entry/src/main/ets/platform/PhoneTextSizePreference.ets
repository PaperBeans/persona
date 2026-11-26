import { common } from '@kit.AbilityKit';
import { preferences } from '@kit.ArkData';
import { settings } from '@kit.BasicServicesKit';
import { phoneTextScale } from '../core/PhoneTextSize';

@ObservedV2
export class PhoneTextSizePreference {
  @Trace choice: string = 'standard';
  @Trace ready: boolean = false;
  @Trace saving: boolean = false;
  private context: common.UIAbilityContext | undefined;
  private store: preferences.Preferences | undefined;
  private baseline: number = 1;
  private systemValue: number = 0;
  private adjusted: boolean = false;
  private observing: boolean = false;

  private readSystem(context: common.UIAbilityContext): number {
    try {
      const value = Number(settings.getValueSync(context,settings.display.FONT_SCALE,'',settings.domainName.USER_PROPERTY));
      return Number.isFinite(value) && value>0 ? value : 0;
    } catch (_) { return 0; }
  }
  private apply(context: common.UIAbilityContext, value: string): void {
    context.getApplicationContext().setFontSizeScale(phoneTextScale(this.baseline,value));
  }
  attach(context: common.UIAbilityContext): void {
    if (this.context === context) return;
    this.detach();
    this.context = context;
    this.systemValue = 0;
    this.baseline = context.config.fontSizeScale || 1;
    try {
      const store=preferences.getPreferencesSync(context,{name:'persona_phone_text_size'});
      const saved=String(store.getSync('choice','standard'));
      this.choice=['small','large'].includes(saved) ? saved : 'standard';
      if (this.choice!=='standard') {
        this.systemValue=this.readSystem(context);
        this.baseline=this.systemValue || this.baseline;
        this.adjusted=true;
        this.apply(context,this.choice);
      }
      this.store=store;
      this.ready=true;
      if (this.adjusted) this.observe(context);
    } catch (_) { this.ready=false; }
  }
  private observe(context: common.UIAbilityContext): void {
    if (this.observing) return;
    try {
      this.observing=settings.registerKeyObserver(context,settings.display.FONT_SCALE,settings.domainName.USER_PROPERTY,() => {
        if (this.context === context) this.refresh();
      });
    } catch (_) { this.observing=false; }
  }
  refresh(configurationScale: number = 0): void {
    const context=this.context;
    if (!context || !this.ready) return;
    if (!this.adjusted) {
      if (configurationScale>0) this.baseline=configurationScale;
      return;
    }
    const value=this.readSystem(context);
    if (value>0 && Math.abs(this.systemValue-value)>.001) {
      this.systemValue=value;
      this.baseline=value;
      if (this.adjusted) {
        try { this.apply(context,this.choice); } catch (_) {}
      }
    }
  }
  async choose(value: string): Promise<boolean> {
    const context=this.context, store=this.store;
    if (!context || !store || !this.ready || this.saving || !['small','standard','large'].includes(value)) return false;
    if (this.choice===value) return true;
    this.refresh();
    const original=this.choice;
    this.saving=true;
    try {
      // Persist the local choice before changing native text or publishing its selection.
      store.putSync('choice',value);
      await store.flush();
      if (this.context!==context || this.store!==store) return false;
      if (!this.adjusted) this.systemValue=this.readSystem(context);
      this.adjusted=true;
      this.apply(context,value);
      this.observe(context);
      this.choice=value;
      return true;
    } catch (_) {
      try { store.putSync('choice',original); await store.flush(); } catch (_) {}
      if (this.context===context && this.adjusted) {
        try { this.apply(context,original); } catch (_) {}
      }
      return false;
    } finally { if (this.context===context) this.saving=false; }
  }
  detach(): void {
    if (this.observing && this.context) {
      try { settings.unregisterKeyObserver(this.context,settings.display.FONT_SCALE,settings.domainName.USER_PROPERTY); } catch (_) {}
    }
    this.observing=false;this.adjusted=false;this.context=undefined;this.store=undefined;
    this.choice='standard';this.ready=false;this.saving=false;
  }
}
export const phoneTextSize = new PhoneTextSizePreference();
