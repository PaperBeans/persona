import { pasteboard } from '@kit.BasicServicesKit';
import { deviceInfo } from '@kit.BasicServicesKit';
import { isPairingAddress } from '../core/WireCompatibility';
import { Pairing } from '../platform/Pairing';
import { normalizeBaseUrl } from '../platform/HubTransport';
import { SshDeviceIdentity, SshPublicIdentity } from '../platform/SshDeviceIdentity';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { PairingSsh } from '../models/PairingSsh';
import { SshConfiguration } from '../models/SshTunnel';
import { SshRouteFields } from './SshRouteFields';
import { SshEndpointRow } from './SshEndpointRow';

interface SshForwardTarget { host: string; port: number; }

@ComponentV2
export struct PairingSshForm {
  @Require @Param draft: PairingSsh;
  @Param hubAddress: string = '';
  @Event onDone: () => void = () => {};
  @Event onClose: () => void = () => {};
  @Local private host: string = '';
  @Local private port: string = '22';
  @Local private username: string = '';
  @Local private fingerprint: string = '';
  @Local private pin: string = '';
  @Local private privateKeyText: string = '';
  @Local private phrase: string = '';
  @Local private error: string = '';
  @Local private identity: SshPublicIdentity | undefined = undefined;
  @Local private importing: boolean = false;
  @Local private checking: boolean = false;
  @Local private targetHost: string = '';
  @Local private targetPort: string = '';
  @Local private always: boolean = false;
  private viewEpoch: number = 0;
  private visible: boolean = false;

  aboutToAppear(): void {
    this.visible = true;
    const config = this.draft.configuration;
    this.host = config.sshHost; this.port = String(config.sshPort); this.username = config.username;
    this.fingerprint = config.hostKey; this.pin = this.draft.hubPin;
    this.importing = this.draft.configured && !config.deviceIdentity;
    if (this.draft.configured) {
      const target = config.destination(this.previousAddress()); this.always = config.routePolicy !== 'fallback';
      this.targetHost = target.host === '127.0.0.1' ? '' : target.host; this.targetPort = target.port === 18660 ? '' : String(target.port);
    }
    const epoch = ++this.viewEpoch;
    SshDeviceIdentity.publicIdentity('persona-' + (deviceInfo.productModel || 'HarmonyOS')).then((identity: SshPublicIdentity) => {
      if (this.visible && this.viewEpoch === epoch) this.identity = identity;
    }).catch(() => { if (this.visible && this.viewEpoch === epoch) this.error = 'ssh_storage_failed'; });
  }
  aboutToDisappear(): void {
    this.visible = false; this.viewEpoch++; this.draft.cancelServerCheck();
    this.identity = undefined; this.privateKeyText = ''; this.phrase = '';
  }
  private serverChanged(): void {
    const old = this.draft.configuration;
    this.fingerprint = this.draft.configured && old.sshHost === this.host.trim() && old.sshPort === Number(this.port) ? old.hostKey : '';
    this.error = '';
  }
  private previousAddress(): string {
    const raw = this.hubAddress.trim();
    try { return raw ? isPairingAddress(raw) ? Pairing.parse(raw).initialAddress : normalizeBaseUrl(raw) : ''; }
    catch (_) { return ''; }
  }
  private applyPolicy(config: SshConfiguration, target: SshForwardTarget): void {
    const old = this.draft.configuration;
    if (this.draft.configured && old.routePolicy === 'legacy' && this.always) {
      const prior = old.destination(this.previousAddress());
      if (prior.host === target.host && prior.port === target.port) return;
    }
    config.routePolicy = this.always ? 'always' : 'fallback';
    config.forwardHost = target.host; config.forwardPort = target.port;
  }
  private alwaysChanged(value: boolean): void {
    this.always = value;
    const old = this.draft.configuration;
    if (!this.draft.configured || old.sshHost !== this.host.trim() || old.sshPort !== Number(this.port) ||
      old.username !== this.username.trim() || old.hostKey !== this.fingerprint.trim() || this.pin.trim() !== this.draft.hubPin ||
      !!this.privateKeyText || !!this.phrase || this.importing === old.deviceIdentity) return;
    const prior = old.destination(this.previousAddress()), target = this.forwardTarget();
    if (prior.host !== target.host || prior.port !== target.port) return;
    const next = old.copy(); this.applyPolicy(next,target);
    if (next.routePolicy !== old.routePolicy) this.draft.configure(next,'','',this.pin,true,old.deviceIdentity);
  }
  private showProblem(error: Error): void {
    const codes = ['ssh_invalid_config','ssh_invalid_key','ssh_key_required','ssh_storage_failed','ssh_busy','ssh_timeout',
      'ssh_connect_failed','ssh_host_key_mismatch','ssh_auth_failed','ssh_proxy_failed','ssh_native_failed'];
    if (this.visible && error.message !== 'ssh_cancelled') this.error = codes.includes(error.message) ? error.message : 'ssh_invalid_config';
  }
  private removeRoute(): void {
    this.getUIContext().showAlertDialog({
      title: tr('phone_pair_ssh_remove_title'), message: tr('phone_pair_ssh_remove_help'),
      primaryButton: {value:tr('cancel'),action:():void=>{}},
      secondaryButton: {value:tr('ssh_disable'),action:():void=>{ if (this.visible) {this.draft.clear();this.onDone();} }}
    });
  }
  private async testAndSave(config: SshConfiguration, target: SshForwardTarget, epoch: number, revision: number): Promise<void> {
    if (!this.visible || epoch !== this.viewEpoch || revision !== this.draft.serverRevision()) return;
    this.checking = true;
    try {
      const privateKey = this.importing ? this.privateKeyText : '', phrase = this.importing ? this.phrase : '';
      await this.draft.testForward(config, privateKey, phrase, !this.importing, target.host, target.port);
      if (!this.visible || epoch !== this.viewEpoch) return;
      this.draft.configure(config, privateKey, phrase, this.pin, true, !this.importing);
      this.privateKeyText = ''; this.phrase = ''; this.onDone();
    } catch (error) { this.showProblem(error as Error); }
    finally { if (this.visible && epoch === this.viewEpoch) this.checking = false; }
  }
  private async verify(): Promise<void> {
    if (!this.ready()) return;
    const epoch = this.viewEpoch;
    const config = new SshConfiguration();
    config.sshHost = this.host.trim(); config.sshPort = Number(this.port); config.username = this.username.trim();
    const previous = this.fingerprint.trim();
    this.error = ''; this.checking = true;
    try {
      const target = this.forwardTarget(); this.applyPolicy(config,target);
      const identity = await this.draft.observeServer(config.sshHost, config.sshPort);
      if (!this.visible || epoch !== this.viewEpoch) return;
      const revision = this.draft.serverRevision();
      config.hostKey = identity.fingerprint;
      const old = this.draft.configuration;
      if (this.draft.configured && old.sshHost === config.sshHost && old.sshPort === config.sshPort && old.hostKey === config.hostKey) {
        await this.testAndSave(config, target, epoch, revision); return;
      }
      const changed = !!previous && previous !== config.hostKey;
      const server = config.sshHost + ':' + config.sshPort + '\n';
      const message = changed ? tr('phone_pair_ssh_saved_key') + '\n' + previous + '\n' + tr('phone_pair_ssh_presented_key') + '\n' + config.hostKey :
        identity.algorithm + ' ' + config.hostKey;
      this.getUIContext().showAlertDialog({
        title: tr(changed ? 'phone_pair_ssh_key_changed' : 'ssh_verify_host'),
        message: server + message + '\n\n' + tr('phone_pair_ssh_confirm_peer'),
        primaryButton: { value: tr('cancel'), action: (): void => {} },
        secondaryButton: { value: tr(changed ? 'phone_pair_ssh_trust_replacement' : 'phone_pair_ssh_trust'), action: (): void => {
          this.testAndSave(config, target, epoch, revision);
        }}
      });
    } catch (error) { this.showProblem(error as Error); }
    finally { if (this.visible && epoch === this.viewEpoch) this.checking = false; }
  }
  private ready(): boolean {
    return !this.checking && !!this.host.trim() && !!this.username.trim() && /^\d{1,5}$/.test(this.port) && Number(this.port) > 0 && Number(this.port) <= 65535 &&
      (!this.targetPort.trim() || /^\d{1,5}$/.test(this.targetPort) && Number(this.targetPort) > 0 && Number(this.targetPort) <= 65535) &&
      (!this.fingerprint.trim() || /^SHA256:[A-Za-z0-9+/]{43}$/.test(this.fingerprint.trim())) && (!this.pin.trim() || /^[A-Za-z0-9_-]{43}$/.test(this.pin.trim())) &&
      (this.importing ? !!this.privateKeyText || this.draft.configured && !this.draft.configuration.deviceIdentity && !this.phrase : !!this.identity);
  }
  private forwardTarget(): SshForwardTarget {
    return {host:this.targetHost.trim().replace(/^\[|\]$/g,'') || '127.0.0.1',port:this.targetPort.trim() ? Number(this.targetPort) : 18660};
  }
  private async copy(restricted: boolean): Promise<void> {
    const identity = this.identity; if (!identity) return;
    try {
      let line = identity.line;
      if (restricted) { const target = this.forwardTarget(); line = identity.restrictedLine(target.host, target.port); }
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, line));
      if (this.visible) this.getUIContext().getPromptAction().showToast({message: tr('phone_pair_ssh_copied')});
    } catch (_) { if (this.visible) this.error = 'ssh_invalid_config'; }
  }
  @Builder devicePublicKey() {
    this.subtitle(tr('phone_pair_ssh_this_key'))
    if (this.identity) Column({space:0}) {
      Column({space:3}) {
        Text(this.identity.line).fontSize(13).fontFamily('monospace').maxFontScale(Tokens.interfaceScaleFor(13)).maxLines(1)
          .textOverflow({overflow:TextOverflow.Ellipsis}).fontColor(Colors.ink)
        Text(this.identity.fingerprint).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12)).maxLines(1)
          .textOverflow({overflow:TextOverflow.Ellipsis}).fontColor(Colors.secondary)
      }.width('100%').alignItems(HorizontalAlign.Start).padding(14).accessibilityGroup(true).id('ssh.publicKey')
      Divider().color(Colors.divider).strokeWidth(0.5)
      Button(tr('phone_pair_ssh_copy_key'),{buttonStyle:ButtonStyleMode.TEXTUAL}).width('100%').height(48)
        .fontColor(Colors.link).onClick(()=>this.copy(false)).id('ssh.copyKey')
      Divider().color(Colors.divider).strokeWidth(0.5)
      Button(tr('phone_pair_ssh_copy_restricted'),{buttonStyle:ButtonStyleMode.TEXTUAL}).width('100%').height(48)
        .fontColor(Colors.link).onClick(()=>this.copy(true)).id('ssh.copyAuthorizedKeys')
    }.width('100%').backgroundColor(Colors.surface).borderRadius(14)
    this.subtitle(tr('phone_pair_ssh_public_help'))
  }
  @Builder subtitle(text: string) {
    Text(text).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).width('100%')
  }
  @Builder field(label: string, value: string, id: string, limit: number, kind: InputType, change: (v: string) => void) {
    TextInput({text:value,placeholder:label}).type(kind).maxLength(limit).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17))
      .height(50).backgroundColor(Colors.surface).onChange(change).accessibilityText(label).id(id)
  }
  build() {
    Column() {
      Row({space:8}) {
        Button(tr('cancel'),{buttonStyle:ButtonStyleMode.TEXTUAL}).onClick(()=>this.onClose()).id('ssh.pair.cancel')
        Text(tr('phone_pair_ssh_title')).fontSize(17).maxFontScale(Tokens.interfaceScaleFor(17)).fontWeight(600).textAlign(TextAlign.Center).layoutWeight(1)
        Button(tr(this.checking ? 'phone_pair_ssh_testing' : 'save'),{buttonStyle:ButtonStyleMode.TEXTUAL}).enabled(this.ready()).onClick(()=>this.verify()).id('ssh.pair.save')
      }.width('100%').height(56).padding({left:12,right:12})
      Scroll() {
        Column({space:12}) {
          if (this.error) Text(tr(this.error)).fontSize(14).fontColor(Colors.danger)
          this.devicePublicKey()
          this.subtitle(tr('phone_pair_ssh_server'))
          Column() {
            SshEndpointRow({label:tr('phone_pair_ssh_host'),value:this.host,prompt:'ssh.example.com',fieldId:'ssh.host',
              onValue:(v:string):void=>{ this.host=v;this.serverChanged(); }})
            Divider().color(Colors.divider).strokeWidth(0.5).margin({left:18})
            SshEndpointRow({label:tr('phone_pair_ssh_port'),spokenLabel:tr('phone_pair_ssh_server_port'),value:this.port,prompt:'22',fieldId:'ssh.port',numeric:true,maxLength:5,
              onValue:(v:string):void=>{ this.port=v;this.serverChanged(); }})
            Divider().color(Colors.divider).strokeWidth(0.5).margin({left:18})
            SshEndpointRow({label:tr('phone_pair_ssh_user'),value:this.username,prompt:'user',fieldId:'ssh.user',maxLength:128,
              onValue:(v:string):void=>{ this.username=v; }})
            if (this.fingerprint && this.draft.configured) Column({space:3}) {
              Text(tr('phone_pair_ssh_saved_key')).fontSize(13).fontColor(Colors.secondary)
              Text(this.fingerprint).fontSize(13).fontFamily('monospace').fontColor(Colors.ink)
            }.width('100%').alignItems(HorizontalAlign.Start).padding(12).backgroundColor(Colors.surface).accessibilityGroup(true).id('ssh.hostKey')
          }.width('100%').backgroundColor(Colors.surface).borderRadius(14).clip(true)
          SshRouteFields({host:this.targetHost,port:this.targetPort,always:this.always,
            onHost:(value: string): void => {this.targetHost=value;},onPort:(value: string): void => {this.targetPort=value;},
            onAlways:(value: boolean): void => this.alwaysChanged(value)})
          Button(tr(this.importing ? 'phone_pair_ssh_use_device' : 'phone_pair_ssh_import'),{buttonStyle:ButtonStyleMode.TEXTUAL})
            .fontColor(Colors.link).onClick(()=>{this.importing=!this.importing;this.privateKeyText='';this.phrase='';}).id('ssh.chooseIdentity')
          if (this.importing) Column({space:6}) {
            this.subtitle(tr('phone_pair_ssh_key_help'))
            if (!this.draft.configured) this.field(tr('ssh_host_key'),this.fingerprint,'ssh.expectedKey',50,InputType.Normal,(v:string)=>{ this.fingerprint=v; })
            TextArea({text:this.privateKeyText,placeholder:tr(this.draft.configured ? 'ssh_replace_key' : 'ssh_private_key')})
              .maxLength(32768).height(100).fontSize(14).backgroundColor(Colors.surface).onChange((v:string)=>{this.privateKeyText=v;}).id('ssh.privateKey')
            this.field(tr('ssh_passphrase'),this.phrase,'ssh.passphrase',4096,InputType.Password,(v:string)=>{ this.phrase=v; })
          }.borderRadius(14).clip(true)
          this.subtitle(tr('phone_pair_ssh_tls_help'))
          this.field(tr('phone_pair_ssh_tls_pin'),this.pin,'ssh.hubPin',43,InputType.Normal,(v:string)=>{ this.pin=v; })
          if (this.draft.configured) Button(tr('ssh_disable'),{buttonStyle:ButtonStyleMode.TEXTUAL})
            .fontColor(Colors.danger).onClick(()=>this.removeRoute()).id('ssh.pair.remove')
        }.width('100%').alignItems(HorizontalAlign.Start).padding({left:16,right:16,top:16,bottom:32})
      }.width('100%').layoutWeight(1).enabled(!this.checking)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('ssh.pair.form')
  }
}
