import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';
import { conversationPermissionModes } from '../core/PhoneSettingsPermissions';

const MACHINE_PAGES: Record<string,string> = {
  'phone_agent_folders':'agent-dirs/hidden', 'phone_claude_config':'claude-settings',
  'phone_codex_config':'codex-settings', 'phone_agy_config':'agy-settings'
};

export const FOLLOW_FIELDS: Record<string,string> = {
  'start_requested_tasks':'phone_follow_start_requested', 'main_supervises_own_tasks':'phone_follow_supervise',
  'agent_tasks':'phone_follow_agent_tasks', 'close_ended_terminals':'phone_follow_close_ended', 'agent_auto_naming':'phone_follow_naming'
};

export function agentPreferencePath(page:string, machine:string):string {
  if (page === 'phone_agent_followup' || page === 'phone_default_permissions') return '/api/v1/assistant';
  const suffix = MACHINE_PAGES[page];
  return suffix && machine ? '/api/v1/machines/' + encodeURIComponent(machine) + '/' + suffix : '';
}

export function agentSettingValue(setting:Record<string,Object>):Object | undefined {
  return setting['value'] ?? setting['default'];
}

export function validAgentSetting(setting:Record<string,Object>,value:Object):boolean {
  const type = Wire.text(setting,'type');
  if (type === 'bool') return typeof value === 'boolean';
  if (type === 'enum') return typeof value === 'string' && Wire.strings(setting,'options').includes(value as string);
  return type === 'string' && typeof value === 'string';
}

export function cleanAgentFolder(input:string):string {
  const path=input.trim().replace(/\/+$/,'');
  return path.length>1 && (path.startsWith('/') || path.startsWith('~/') || path.startsWith('*')) ? path : '';
}

@ObservedV2
export class PhoneAgentSettings {
  @Trace page:string = '';
  @Trace machine:string = '';
  @Trace data:Record<string,Object> = {};
  @Trace loading:boolean = false;
  @Trace saving:boolean = false;
  @Trace error:string = '';
  @Trace notice:string = '';
  private model?:ClientModel;
  private scope:string = '';
  private generation:number = -1;
  private serial:number = 0;

  machines(model:ClientModel):ResourceRow[] {
    return model.machines.filter((row:ResourceRow) => (!row.group || row.group === model.profile.id) &&
      (!row.manager || row.manager === model.profile.manager) && row.state !== 'offline' &&
      !Wire.bool(row.data(),'pending') && !Wire.bool(row.data(),'pending_confirm'));
  }

  async open(model:ClientModel,page:string,machine:string = ''):Promise<void> {
    this.close();
    this.model = model; this.page = page; this.scope = model.scope(); this.generation = model.connectionGeneration();
    this.machine = this.machines(model).find((row:ResourceRow) => row.id === machine)?.id || this.machines(model)[0]?.id || '';
    await this.read();
  }

  close():void {
    this.serial++; this.model = undefined; this.scope = ''; this.data = {}; this.error = ''; this.notice='';
    this.loading = false; this.saving = false;
  }

  private current(serial:number):boolean {
    return this.serial === serial && !!this.model && this.model.owns(this.generation,this.scope);
  }

  async read():Promise<void> {
    if (this.saving) return;
    const model = this.model, path = agentPreferencePath(this.page,this.machine);
    const serial = ++this.serial;
    if (!model || !this.current(serial) || !path || !model.connected) {
      this.data = {}; this.error = model?.connected ? 'empty' : 'offline'; return;
    }
    this.loading = true; this.error = '';
    try {
      const result = await model.repo().call(path);
      if (this.current(serial)) {
        const manager=Wire.text(result,'manager');
        if (this.page==='phone_default_permissions' && Wire.has(result,'manager') && model.profile.manager && manager!==model.profile.manager) {
          this.data={};this.error='request_updated';
        }
        else this.data = result;
      }
    } catch (_) { if (this.current(serial)) this.error='failed'; }
    finally { if (this.current(serial)) this.loading=false; }
  }

  async setOption(key:string,value:Object):Promise<boolean> {
    if (!this.model?.connected || !this.current(this.serial) || this.loading || this.saving || this.error) return false;
    if (this.page === 'phone_agent_followup') {
      if (!FOLLOW_FIELDS[key] || typeof this.data[key] !== 'boolean' || typeof value !== 'boolean') return false;
      return this.data[key] === value || await this.write(Wire.payload([key],[value]));
    }
    const option = Wire.rows(this.data,'settings').find((row:Record<string,Object>) => Wire.text(row,'key') === key);
    if (!option || !validAgentSetting(option,value)) return false;
    return agentSettingValue(option) === value || await this.write(Wire.payload(['key','value'],[key,value]));
  }

  async setConversationPermission(value:string):Promise<boolean> {
    const model=this.model, prior=Wire.text(this.data,'default_permission_mode');
    if (!model?.connected || !this.current(this.serial) || this.page!=='phone_default_permissions' ||
      !prior || !conversationPermissionModes.includes(value) || this.loading || this.saving || !!this.error || Wire.bool(this.data,'read_only')) return false;
    if (prior===value) return true;
    const before=this.data, serial=++this.serial;
    this.saving=true; this.notice='';
    const optimistic=Wire.parse(JSON.stringify(before));optimistic['default_permission_mode']=value;this.data=optimistic;
    try {
      const confirmed=(row:Record<string,Object>):boolean => Wire.text(row,'default_permission_mode')===value &&
        (!Wire.has(row,'manager') || !model.profile.manager || Wire.text(row,'manager')===model.profile.manager);
      const reply=await model.repo().call('/api/v1/assistant','PUT',Wire.payload(['default_permission_mode'],[value]),'',
        () => this.current(serial),false,confirmed);
      if (!this.current(serial)) return false;
      if (!confirmed(reply)) throw new Error('unconfirmed');
      this.data=reply; return true;
    } catch (_) {
      if (this.current(serial)) { this.data=before;this.notice='phone_permission_unconfirmed'; }
      return false;
    } finally { if (this.current(serial)) this.saving=false; }
  }

  async changeFolder(pattern:string, replacement:Record<string,Object> | undefined, adding:boolean = false):Promise<boolean> {
    if (this.page !== 'phone_agent_folders' || !Wire.bool(this.data,'supports_expected_rules') || !pattern || !Array.isArray(this.data['rules'])) return false;
    const before = Wire.rows(this.data,'rules');
    const index = before.findIndex((r:Record<string,Object>) => Wire.text(r,'pattern') === pattern);
    if (adding ? index >= 0 || !replacement : index < 0) return false;
    if (replacement && (Wire.text(replacement,'pattern') !== pattern || !['block','hide'].includes(Wire.text(replacement,'action')) ||
      typeof replacement['enabled'] !== 'boolean')) return false;
    const next = before.slice();
    if (adding && replacement) next.push(replacement);
    else if (replacement) next.splice(index,1,replacement);
    else next.splice(index,1);
    return await this.write(Wire.payload(['rules','expected_rules'],[next,before]));
  }

  private async write(body:Record<string,Object>):Promise<boolean> {
    const model = this.model, path = agentPreferencePath(this.page,this.machine);
    if (!model || !model.connected || !path || this.loading || this.saving || this.error || !this.current(this.serial)) return false;
    const serial = ++this.serial;
    this.saving = true; this.notice='';
    try {
      const updated = await model.repo().call(path,'PUT',body,'',() => this.current(serial));
      if (!this.current(serial)) return false;
      this.data = updated;
      return true;
    } catch (failure) {
      if (!this.current(serial)) return false;
      if (this.page==='phone_agent_folders' && Wire.number(failure as Object as Record<string,Object>,'status')===409) {
        try {
          const latest=await model.repo().call(path);
          if (this.current(serial)) { this.data=latest; this.notice='phone_rules_changed'; }
        } catch (_) { if (this.current(serial)) this.error='failed'; }
      } else this.error='failed';
      return false;
    }
    finally { if (this.current(serial)) this.saving=false; }
  }
}

@ObservedV2
export class AgentSettingTextDraft {
  @Trace key: string = '';
  @Trace text: string = '';
  private target: string = '';
  private revision: number = 0;

  begin(target:string,key:string,value:string):void {
    this.revision++; this.target=target; this.key=key; this.text=value;
  }
  isEditing(target:string,key:string):boolean { return !!key && this.target===target && this.key===key; }
  edit(value:string):void { this.revision++; this.text=value; }
  close():void { this.revision++; this.target=''; this.key=''; this.text=''; }
  async save(form:PhoneAgentSettings,target:string):Promise<void> {
    if (!this.isEditing(target,this.key)) return;
    const revision=this.revision;
    if (await form.setOption(this.key,this.text) && this.revision===revision) this.close();
  }
}
