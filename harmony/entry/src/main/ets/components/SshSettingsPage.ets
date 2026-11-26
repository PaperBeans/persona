import { IconActions } from '../design/IconActions';
import { Colors } from '../design/Colors';
import { ButtonStyles } from '../design/Controls';
import { SshConfiguration, SshDestination, SshTunnel } from '../models/SshTunnel';
import { SshRouteFields } from './SshRouteFields';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';

@ComponentV2
export struct SshSettingsPage {
  @Require @Param tunnel: SshTunnel;
  @Param active: boolean = true;
  @Event onSaved: () => void = () => {};
  @Local sshHost: string = '';
  @Local sshPort: string = '22';
  @Local username: string = '';
  @Local hostKey: string = '';
  @Local targetHost: string = '';
  @Local targetPort: string = '';
  @Local always: boolean = false;
  @Local privateKey: string = '';
  @Local passphrase: string = '';
  @Local busy: boolean = false;
  @Local error: string = '';
  private mounted: boolean = false;
  private visit: number = 0;

  aboutToAppear(): void { this.mounted = true; this.visit++; this.restore(); }
  aboutToDisappear(): void { this.mounted = false; this.clearSecrets(); this.visit++; }
  @Monitor('active') visibilityChanged(): void {
    this.visit++; this.clearSecrets(); if (this.active && this.mounted) this.restore();
  }
  @Monitor('tunnel.loaded') loadedChanged(): void {
    this.visit++; this.clearSecrets(); if (this.active && this.mounted) this.restore();
  }
  private clearSecrets(): void { this.privateKey = ''; this.passphrase = ''; }
  private restore(): void {
    const config = this.tunnel.configuration;
    this.sshHost = config.sshHost; this.sshPort = String(config.sshPort); this.username = config.username;
    this.hostKey = config.hostKey; this.error = ''; this.busy = false;
    const target: SshDestination = config.enabled ? config.destination(this.tunnel.profileAddress()) : {host:'127.0.0.1',port:18660};
    this.targetHost = target.host === '127.0.0.1' ? '' : target.host; this.targetPort = target.port === 18660 ? '' : String(target.port);
    this.always = config.enabled && config.routePolicy !== 'fallback';
  }
  private applySettings(config: SshConfiguration): void {
    const old = this.tunnel.configuration, target = old.destination(this.tunnel.profileAddress());
    const host = this.targetHost.trim().replace(/^\[|\]$/g,'') || '127.0.0.1', port = this.targetPort.trim() ? Number(this.targetPort) : 18660;
    if (old.enabled && old.routePolicy === 'legacy' && this.always && target.host === host && target.port === port) return;
    config.routePolicy = this.always ? 'always' : 'fallback'; config.forwardHost = host; config.forwardPort = port;
  }
  private alwaysChanged(value: boolean): void {
    this.always = value;
    const old = this.tunnel.configuration;
    if (this.busy || !old.enabled || old.sshHost !== this.sshHost.trim() || old.sshPort !== Number(this.sshPort) ||
      old.username !== this.username.trim() || old.hostKey !== this.hostKey.trim() || this.privateKey || this.passphrase) return;
    const kept = old.destination(this.tunnel.profileAddress()), next = old.copy(); this.applySettings(next);
    const draft = next.destination(this.tunnel.profileAddress());
    if (next.routePolicy === old.routePolicy || kept.host !== draft.host || kept.port !== draft.port) return;
    this.save(next,this.visit,this.tunnel);
  }
  private current(visit: number, tunnel: SshTunnel): boolean { return this.mounted && this.active && this.visit === visit && this.tunnel === tunnel; }
  private confirmSave(): void {
    if (this.busy || !this.tunnel.loaded || !this.mounted || !this.active) return;
    const visit = this.visit, tunnel = this.tunnel;
    const config = tunnel.configuration.copy(); config.enabled = true; config.sshHost = this.sshHost.trim();
    config.sshPort = Number(this.sshPort); config.username = this.username.trim(); config.hostKey = this.hostKey.trim();
    this.applySettings(config);
    this.getUIContext().showAlertDialog({
      title: tr('ssh_verify_host'),
      message: tr('ssh_verify_message') + '\n\n' + config.sshHost + ':' + String(config.sshPort) + '\n' + config.hostKey,
      primaryButton: { value: tr('cancel'), action: (): void => {} },
      secondaryButton: { value: tr('ssh_verified_enable'), action: (): void => {
        if (this.current(visit, tunnel)) this.save(config, visit, tunnel);
      } }
    });
  }
  private async save(config: SshConfiguration, visit: number, tunnel: SshTunnel): Promise<void> {
    const key = this.privateKey, phrase = this.passphrase; this.clearSecrets(); this.busy = true; this.error = '';
    try {
      await tunnel.save(config, key, phrase, true, (): boolean => this.current(visit, tunnel));
      if (this.current(visit, tunnel)) this.onSaved();
    } catch (error) { if (this.current(visit, tunnel)) this.error = (error as Error).message; }
    finally { if (this.current(visit, tunnel)) this.busy = false; }
  }
  private async disable(): Promise<void> {
    const visit = this.visit, tunnel = this.tunnel; this.busy = true; this.error = ''; this.clearSecrets();
    try { await tunnel.disable(); if (this.current(visit, tunnel)) this.onSaved(); }
    catch (_) { if (this.current(visit, tunnel)) this.error = 'ssh_storage_failed'; }
    finally { if (this.current(visit, tunnel)) this.busy = false; }
  }
  private status(): string {
    if (!this.tunnel.loaded) return this.tunnel.error || 'ssh_loading';
    if (this.tunnel.phase === 'connecting') return 'ssh_connecting';
    if (this.tunnel.phase === 'connected') return 'ssh_connected';
    if (this.tunnel.phase === 'direct') return 'phone_pair_ssh_direct_active';
    if (this.tunnel.phase === 'saving') return 'ssh_saving';
    if (this.tunnel.error) return this.tunnel.error;
    return this.tunnel.configuration.enabled ? 'ssh_enabled' : 'ssh_disabled';
  }
  build() {
    Scroll() {
      Column({ space: Tokens.page }) {
        Text(tr(this.status())).fontSize(Tokens.body).fontColor($r('sys.color.font_secondary')).width('100%')
        if (this.error) Text(tr(this.error)).fontSize(Tokens.body).fontColor($r('app.color.persona_danger')).width('100%')
        Column({ space: Tokens.gap }) {
          Text(tr('ssh_host')).fontSize(Tokens.meta).width('100%')
          TextInput({ text: this.sshHost, placeholder: tr('ssh_host') }).maxLength(253)
            .onChange((value: string) => this.sshHost = value).height(Tokens.input)
          Text(tr('ssh_port')).fontSize(Tokens.meta).width('100%')
          TextInput({ text: this.sshPort }).type(InputType.Number).maxLength(5)
            .onChange((value: string) => this.sshPort = value).height(Tokens.input)
          Text(tr('ssh_username')).fontSize(Tokens.meta).width('100%')
          TextInput({ text: this.username, placeholder: tr('ssh_username') }).maxLength(128)
            .onChange((value: string) => this.username = value).height(Tokens.input)
          Text(tr('ssh_host_key')).fontSize(Tokens.meta).width('100%')
          TextInput({ text: this.hostKey, placeholder: 'SHA256:' }).maxLength(50)
            .onChange((value: string) => this.hostKey = value).height(Tokens.input)
          SshRouteFields({host:this.targetHost,port:this.targetPort,always:this.always,
            onHost:(value: string): void => {this.targetHost=value;},onPort:(value: string): void => {this.targetPort=value;},
            onAlways:(value: boolean): void => this.alwaysChanged(value)})
          Text(tr(this.tunnel.configuration.keyRef ? 'ssh_replace_key' : 'ssh_private_key')).fontSize(Tokens.meta).width('100%')
          TextArea({ text: this.privateKey, placeholder: tr('ssh_private_key') }).maxLength(32768)
            .onChange((value: string) => this.privateKey = value).height(Tokens.inputExpanded)
          Text(tr('ssh_passphrase')).fontSize(Tokens.meta).width('100%')
          TextInput({ text: this.passphrase, placeholder: tr('ssh_passphrase') }).type(InputType.Password).maxLength(4096)
            .onChange((value: string) => this.passphrase = value).height(Tokens.input)
        }.width('100%').enabled(!this.busy && this.tunnel.loaded)
        Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('ssh_save_enable')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('ssh_save_enable', true)]) }.attributeModifier(ButtonStyles.iconPrimary).accessibilityText(tr('ssh_save_enable')).bindTips(tr('ssh_save_enable'))
          .enabled(!this.busy && this.tunnel.loaded && !!this.sshHost.trim() && !!this.username.trim() && !!this.hostKey.trim())
          .onClick(() => this.confirmSave())
        if (this.tunnel.configuration.enabled) Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('ssh_disable')).fontSize(Tokens.symbolFont).fontColor([IconActions.color('ssh_disable', false)]) }.attributeModifier(ButtonStyles.icon).accessibilityText(tr('ssh_disable')).bindTips(tr('ssh_disable'))
          .enabled(!this.busy).onClick(() => this.disable())
      }.padding(Tokens.page).width('100%')
    }.width('100%').height('100%').backgroundColor($r('app.color.persona_canvas'))
  }
}
