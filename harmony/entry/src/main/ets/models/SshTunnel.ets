import { common } from '@kit.AbilityKit';
import { url, util } from '@kit.ArkTS';
import { connection as netConnection } from '@kit.NetworkKit';
import nativeSsh from 'libpersona_ssh.so';
import type { NativeSshConfig, NativeSshLease } from 'libpersona_ssh.so';
import { ClientModel } from './ClientModel';
import type { ClientViewLease } from './ClientModel';
import type { TransportProxy } from '../platform/HubTransport';
import { Wire } from './Wire';
import { SshDeviceIdentity } from '../platform/SshDeviceIdentity';
import { SshCredentialVault, SshSecrets } from '../platform/SshCredentialVault';

const STORAGE_KEY: string = 'ssh-tunnel';
const ERROR_KEYS: string[] = ['ssh_invalid_config', 'ssh_invalid_key', 'ssh_key_required', 'ssh_storage_failed', 'ssh_cancelled',
  'ssh_busy', 'ssh_timeout', 'ssh_dns_failed', 'ssh_connect_failed', 'ssh_host_key_mismatch', 'ssh_auth_failed', 'ssh_proxy_failed', 'ssh_native_failed'];
const DNS_TIMEOUT_MS: number = 10000;
const MAX_RESOLVED_HUB_IPS: number = 16;
export interface SshDestination { host: string; port: number; }

export class SshConfiguration {
  enabled: boolean = false;
  sshHost: string = '';
  sshPort: number = 22;
  username: string = '';
  hostKey: string = '';
  keyRef: string = '';
  deviceIdentity: boolean = false;
  // Earlier clients always used SSH and forwarded to the logical Hub address.
  routePolicy: string = 'legacy';
  forwardHost: string = '';
  forwardPort: number = 0;
  static from(value: string): SshConfiguration {
    const data = Wire.parse(value), result = new SshConfiguration();
    if (value.trim() && typeof data['enabled'] !== 'boolean') throw new Error('ssh_invalid_config');
    result.enabled = Wire.bool(data, 'enabled'); result.sshHost = Wire.text(data, 'sshHost');
    result.sshPort = Wire.number(data, 'sshPort', 22); result.username = Wire.text(data, 'username');
    result.hostKey = Wire.text(data, 'hostKey'); result.keyRef = Wire.text(data, 'keyRef'); result.deviceIdentity = Wire.bool(data, 'deviceIdentity');
    result.routePolicy = Wire.text(data,'routePolicy','legacy'); result.forwardHost = Wire.text(data,'forwardHost');
    result.forwardPort = Wire.number(data,'forwardPort',0);
    if (!['legacy','always','fallback'].includes(result.routePolicy)) throw new Error('ssh_invalid_config');
    return result;
  }
  copy(): SshConfiguration { return SshConfiguration.from(JSON.stringify(this)); }
  destination(logicalAddress: string): SshDestination {
    if (this.routePolicy !== 'legacy') return {host:this.forwardHost,port:this.forwardPort};
    if (!logicalAddress) return {host:'127.0.0.1',port:18660};
    const parsed = url.URL.parseURL(logicalAddress);
    return {host:parsed.hostname.replace(/^\[|\]$/g,''),port:parsed.port ? Number(parsed.port) : parsed.protocol === 'https:' ? 443 : 80};
  }
}

class SshOwner {
  readonly client: ClientModel;
  readonly profile: string;
  readonly scope: string;
  readonly generation: number;
  readonly baseUrl: string;
  readonly pin: string;
  readonly epoch: number;
  readonly pairing: number;
  readonly admit: () => boolean;
  constructor(client: ClientModel, admit: () => boolean) {
    this.client = client; this.profile = client.profile.id; this.scope = client.scope(); this.generation = client.connectionGeneration();
    this.baseUrl = client.profile.baseUrl; this.pin = client.profile.pin; this.epoch = client.viewRevision(STORAGE_KEY); this.admit = admit;
    this.pairing = client.pairingRevision();
  }
  current(): boolean {
    return this.admit() && !this.client.demo && !!this.profile && this.client.owns(this.generation, this.scope) &&
      this.client.profile.id === this.profile && this.client.profile.baseUrl === this.baseUrl && this.client.profile.pin === this.pin &&
      this.client.viewRevision(STORAGE_KEY) === this.epoch && this.client.pairingRevision() === this.pairing;
  }
}

export class SshProxyLease implements TransportProxy {
  readonly id: string;
  readonly host: string;
  readonly port: number;
  readonly username: string;
  readonly password: string;
  private readonly current: () => boolean;
  constructor(native: NativeSshLease, current: () => boolean) {
    this.id = native.id; this.host = native.host; this.port = native.port;
    this.username = native.username; this.password = native.password; this.current = current;
  }
  isCurrent(): boolean { return this.current() && nativeSsh.isOpen(this.id); }
}

/** Root owns one instance per client. Bind after each connection generation change. */
@ObservedV2
export class SshTunnel {
  private static savingProfiles: Set<string> = new Set();
  @Trace configuration: SshConfiguration = new SshConfiguration();
  @Trace phase: string = 'idle';
  @Trace error: string = '';
  @Trace loaded: boolean = false;
  private vault: SshCredentialVault | undefined;
  private owner: SshOwner | undefined;
  private attempt: number = 0;
  private pendingId: string = '';
  private lease: SshProxyLease | undefined;
  private opening: Promise<SshProxyLease | undefined> | undefined;
  private dnsWaiters: Set<() => void> = new Set();
  private directRecheck: number = -1;

  initialize(_context: common.UIAbilityContext): void { if (!this.vault) this.vault = new SshCredentialVault(); }
  async bind(client: ClientModel, admitForeground: () => boolean = (): boolean => true): Promise<void> {
    this.stop(); const owner = new SshOwner(client, admitForeground), attempt = this.attempt;
    this.owner = owner; this.configuration = new SshConfiguration(); this.loaded = false; this.error = '';
    if (!owner.current() || !this.vault) return;
    this.vault.allow(owner.profile);
    try {
      const saved = await client.loadView(STORAGE_KEY);
      if (!this.current(owner, attempt)) return;
      const config = SshConfiguration.from(saved);
      if (config.enabled) { SshTunnel.validate(config, owner.baseUrl, owner.pin); if (!config.keyRef && !config.deviceIdentity) throw new Error('ssh_key_required'); }
      this.configuration = config; this.loaded = true;
    } catch (error) { if (this.current(owner, attempt)) { this.phase = 'failed'; this.error = this.code(error as Error); } }
  }
  private current(owner: SshOwner, attempt: number): boolean { return this.owner === owner && this.attempt === attempt && owner.current(); }
  private code(error: Error): string { return ERROR_KEYS.includes(error.message) ? error.message : 'ssh_native_failed'; }
  static validateServer(config: SshConfiguration): void {
    const validHost = (host: string): boolean => !!host && host.length <= 253 && /^[a-zA-Z0-9.:-]+$/.test(host) && !host.includes('..');
    if (!validHost(config.sshHost) || !Number.isInteger(config.sshPort) || config.sshPort < 1 || config.sshPort > 65535 ||
      !config.username || config.username.length > 128 || /[\u0000-\u0020\u007f]/.test(config.username) ||
      !/^SHA256:[A-Za-z0-9+/]{43}$/.test(config.hostKey)) throw new Error('ssh_invalid_config');
    if (config.keyRef && !/^[a-fA-F0-9-]{36}$/.test(config.keyRef)) throw new Error('ssh_invalid_config');
    if (config.routePolicy !== 'legacy' && (!['always','fallback'].includes(config.routePolicy) ||
      !validHost(config.forwardHost) || !Number.isInteger(config.forwardPort) || config.forwardPort < 1 || config.forwardPort > 65535))
      throw new Error('ssh_invalid_config');
  }
  profileAddress(): string { return this.owner?.baseUrl || ''; }
  usingDirectAddress(): void {
    if (this.owner?.current() && !this.lease && this.configuration.enabled && this.configuration.routePolicy !== 'legacy') {
      this.phase = 'direct'; this.error = '';
    }
  }
  watchDirect(check: () => Promise<string>, restore: () => void): void {
    const lease = this.lease;
    if (this.configuration.routePolicy !== 'fallback' || !lease?.isCurrent() || this.directRecheck !== -1) return;
    this.directRecheck = setTimeout(async (): Promise<void> => {
      this.directRecheck = -1;
      if (!lease.isCurrent()) return;
      try {
        await check();
        if (lease.isCurrent()) restore();
      } catch (_) { if (lease.isCurrent()) this.watchDirect(check,restore); }
    },120000);
  }
  static validate(config: SshConfiguration, baseUrl: string, pin: string): void {
    SshTunnel.validateServer(config);
    const target = url.URL.parseURL(baseUrl);
    if (target.protocol !== 'https:' || !target.hostname || target.username || target.password || !pin) throw new Error('ssh_invalid_config');
  }
  async save(input: SshConfiguration, privateKey: string, passphrase: string, fingerprintConfirmed: boolean, admit: () => boolean = (): boolean => true): Promise<void> {
    const owner = this.owner, vault = this.vault;
    if (!owner || !vault || !this.loaded || !owner.current() || !admit()) throw new Error('ssh_cancelled');
    if (this.phase === 'saving') throw new Error('ssh_busy');
    if (SshTunnel.savingProfiles.has(owner.profile)) throw new Error('ssh_busy');
    const config = input.copy(); config.sshHost = config.sshHost.trim(); config.username = config.username.trim(); config.hostKey = config.hostKey.trim();
    SshTunnel.validate(config, owner.baseUrl, owner.pin);
    if (!fingerprintConfirmed) throw new Error('ssh_invalid_config');
    const oldRef = this.configuration.keyRef; config.keyRef = oldRef;
    config.deviceIdentity = !privateKey && this.configuration.deviceIdentity;
    if (!privateKey && passphrase) throw new Error('ssh_key_required');
    if (!privateKey && !oldRef && !config.deviceIdentity) throw new Error('ssh_key_required');
    this.stop(); const attempt = this.attempt;
    const current = (): boolean => this.current(owner, attempt) && admit();
    this.phase = 'saving'; this.error = ''; let createdRef: string = ''; let committed: boolean = false;
    SshTunnel.savingProfiles.add(owner.profile);
    try {
      if (privateKey) { createdRef = await vault.put(owner.profile, privateKey, passphrase, current); config.keyRef = createdRef; }
      if (!current()) throw new Error('ssh_cancelled');
      await owner.client.saveView(STORAGE_KEY, JSON.stringify(config), owner.epoch);
      committed = true;
      if (this.current(owner, attempt)) { this.configuration = config; this.phase = 'idle'; }
      owner.client.retireHubConnections(owner.profile);
      if (createdRef && oldRef && oldRef !== createdRef) await vault.remove(owner.profile, oldRef);
    } catch (error) {
      // A saved metadata reference must not be invalidated by a route disappearing after the write.
      if (createdRef && !committed) await vault.remove(owner.profile, createdRef);
      if (this.current(owner, attempt)) { this.phase = 'failed'; this.error = this.code(error as Error); }
      throw new Error(this.code(error as Error));
    } finally { SshTunnel.savingProfiles.delete(owner.profile); }
  }
  /** The SSH route and the Hub's device credential become usable together. Retain the previous key until commit. */
  static async stagePairing(profile: string, config: SshConfiguration, material: SshSecrets | undefined,
    view: ClientViewLease, admit: () => boolean): Promise<(committed: boolean) => Promise<void>> {
    if (!admit() || !view.valid()) throw new Error('ssh_cancelled');
    if (SshTunnel.savingProfiles.has(profile)) throw new Error('ssh_busy');
    SshTunnel.savingProfiles.add(profile);
    const vault = new SshCredentialVault(), current = (): boolean => admit() && view.valid();
    let previous = '', oldRef = '', created = '', next = '', finished = false;
    const finish = async (committed: boolean): Promise<void> => {
      if (finished) return;
      finished = true;
      try {
        if (committed) {
          if (oldRef && oldRef !== created) await vault.remove(profile, oldRef);
          return;
        }
        if (next && view.valid() && await view.read() === next) {
          if (previous) await view.write(previous); else await view.clearIfUnchanged(next);
        }
        if (created) await vault.remove(profile, created);
      } finally { SshTunnel.savingProfiles.delete(profile); }
    };
    try {
      previous = await view.read(); oldRef = SshConfiguration.from(previous).keyRef;
      if (!current()) throw new Error('ssh_cancelled');
      if (!config.deviceIdentity) {
        if (!material) throw new Error('ssh_key_required');
        vault.allow(profile); created = await vault.putSecrets(profile, material, current);
      }
      if (!current()) throw new Error('ssh_cancelled');
      const stored = config.copy(); stored.keyRef = created; next = JSON.stringify(stored);
      await view.write(next);
      if (!current()) throw new Error('ssh_cancelled');
      return finish;
    } catch (error) { await finish(false); throw error as Error; }
  }
  async disable(): Promise<void> {
    const owner = this.owner; if (!owner || !owner.current() || !this.loaded) throw new Error('ssh_cancelled');
    if (this.phase === 'saving') throw new Error('ssh_busy');
    if (SshTunnel.savingProfiles.has(owner.profile)) throw new Error('ssh_busy');
    const config = this.configuration.copy(); config.enabled = false; this.stop(); const attempt = this.attempt;
    SshTunnel.savingProfiles.add(owner.profile);
    try {
      await owner.client.saveView(STORAGE_KEY, JSON.stringify(config), owner.epoch);
      if (this.current(owner, attempt)) this.configuration = config;
      owner.client.retireHubConnections(owner.profile);
    } finally { SshTunnel.savingProfiles.delete(owner.profile); }
  }
  async acquire(admitStart: () => boolean = (): boolean => true): Promise<SshProxyLease | undefined> {
    const owner = this.owner;
    if (!owner || !owner.current() || !this.loaded || !admitStart()) throw new Error('ssh_cancelled');
    if (this.phase === 'saving' || SshTunnel.savingProfiles.has(owner.profile)) throw new Error('ssh_busy');
    if (!this.configuration.enabled) return undefined;
    if (this.lease?.isCurrent()) return this.lease;
    if (this.opening) return this.opening;
    const config = this.configuration.copy(); SshTunnel.validate(config, owner.baseUrl, owner.pin);
    this.stop(); const attempt = this.attempt; this.phase = 'connecting'; this.error = '';
    const vault = this.vault; if (!vault) throw new Error('ssh_storage_failed');
    const owns = (): boolean => this.current(owner, attempt);
    const current = (): boolean => owns() && admitStart();
    const task = this.start(owner.baseUrl, config, current, owns, (): Promise<SshSecrets> => config.deviceIdentity ? SshDeviceIdentity.secrets(current) : vault.get(owner.profile, config.keyRef, current)); this.opening = task;
    try { return await task; } finally { if (this.opening === task) this.opening = undefined; }
  }
  async openPairing(baseUrl: string, pin: string, config: SshConfiguration, material: SshSecrets, admit: () => boolean): Promise<SshProxyLease> {
    if (this.owner || !admit()) throw new Error('ssh_cancelled');
    SshTunnel.validate(config, baseUrl, pin);
    this.stop(); const attempt = this.attempt; this.phase = 'connecting';
    const current = (): boolean => this.attempt === attempt && admit();
    return this.start(baseUrl, config, current, current, async (): Promise<SshSecrets> => material.copy());
  }
  private async start(baseUrl: string, config: SshConfiguration, current: () => boolean, owns: () => boolean, readKey: () => Promise<SshSecrets>): Promise<SshProxyLease> {
    let secrets: SshSecrets | undefined; let id: string = '';
    try {
      const target = url.URL.parseURL(baseUrl), targetHost = target.hostname.replace(/^\[|\]$/g, '');
      let resolvedHubIps: string[] = [];
      try { resolvedHubIps = await this.resolveHubIps(targetHost, current); }
      catch (error) {
        // An explicit tunnel destination can carry the exact logical DNS name even if it only resolves beyond SSH.
        if (config.routePolicy === 'legacy' || (error as Error).message !== 'ssh_dns_failed') throw error as Error;
      }
      if (!current()) throw new Error('ssh_cancelled');
      secrets = await readKey();
      if (!current()) throw new Error('ssh_cancelled');
      id = util.generateRandomUUID(); this.pendingId = id;
      const request: NativeSshConfig = { id: id, sshHost: config.sshHost, sshPort: config.sshPort, username: config.username,
        hostKey: config.hostKey, targetHost: targetHost, targetPort: target.port ? Number(target.port) : 443,
        resolvedHubIps: resolvedHubIps,
        privateKey: secrets.privateKey, passphrase: secrets.passphrase };
      if (config.routePolicy !== 'legacy') { request.forwardHost = config.forwardHost; request.forwardPort = config.forwardPort; }
      const work = nativeSsh.open(request); secrets.clear(); secrets = undefined;
      const native = await work;
      if (!current() || native.id !== id || native.host !== '127.0.0.1' || !Number.isInteger(native.port) || native.port < 1 || native.port > 65535 || !native.password) {
        await nativeSsh.close(id); throw new Error('ssh_cancelled');
      }
      const lease = new SshProxyLease(native, (): boolean => owns() && this.lease?.id === id);
      this.pendingId = ''; this.lease = lease; this.phase = 'connected'; return lease;
    } catch (error) {
      if (id) await nativeSsh.close(id).catch(() => {});
      if (owns()) { this.pendingId = ''; this.phase = 'failed'; this.error = this.code(error as Error); }
      throw new Error(this.code(error as Error));
    } finally { secrets?.clear(); }
  }
  private async resolveHubIps(host: string, current: () => boolean): Promise<string[]> {
    if (!current()) throw new Error('ssh_cancelled');
    if (this.isIpLiteral(host)) return [];
    let timeoutId: number | undefined;
    let cancelLookup: () => void = (): void => {};
    const cancelled = new Promise<never>((_resolve, reject) => {
      cancelLookup = (): void => reject(new Error('ssh_cancelled'));
      this.dnsWaiters.add(cancelLookup);
    });
    try {
      const lookup = netConnection.getAddressesByName(host).then((addresses): string[] => {
        if (!current()) throw new Error('ssh_cancelled');
        const result: string[] = [], seen: Set<string> = new Set();
        for (const item of addresses) {
          const address = typeof item?.address === 'string' ? item.address : '';
          if (!address || !this.isIpLiteral(address) || seen.has(address)) continue;
          seen.add(address); result.push(address);
          if (result.length >= MAX_RESOLVED_HUB_IPS) break;
        }
        if (result.length === 0) throw new Error('ssh_dns_failed');
        return result;
      }, (_error: Object): string[] => {
        throw new Error(current() ? 'ssh_dns_failed' : 'ssh_cancelled');
      });
      const timeout = new Promise<never>((_resolve, reject) => {
        timeoutId = setTimeout((): void => reject(new Error('ssh_dns_failed')), DNS_TIMEOUT_MS);
      });
      const result = await Promise.race([lookup, timeout, cancelled]);
      if (!current()) throw new Error('ssh_cancelled');
      return result;
    } catch (error) {
      const message = error instanceof Error ? error.message : '';
      if (message === 'ssh_cancelled' || message === 'ssh_dns_failed') throw error as Error;
      throw new Error(current() ? 'ssh_dns_failed' : 'ssh_cancelled');
    } finally {
      if (timeoutId !== undefined) clearTimeout(timeoutId);
      this.dnsWaiters.delete(cancelLookup);
    }
  }
  private isIpLiteral(value: string): boolean {
    if (value.length > 45 || value.includes('%')) return false;
    if (value.includes(':')) return /^[0-9a-fA-F:.]+$/.test(value) && value.split(':').length >= 3;
    const octets = value.split('.');
    return octets.length === 4 && octets.every((part: string): boolean => /^\d{1,3}$/.test(part) && Number(part) <= 255);
  }
  stop(): void {
    if (this.directRecheck !== -1) clearTimeout(this.directRecheck);
    this.directRecheck = -1;
    this.attempt++; const pending = this.pendingId, active = this.lease?.id || '';
    this.pendingId = ''; this.lease = undefined; this.opening = undefined; this.phase = 'idle';
    for (const cancel of this.dnsWaiters) cancel();
    this.dnsWaiters.clear();
    // Native close signals cancellation synchronously, including a queued open with no listener yet.
    if (pending) nativeSsh.close(pending).catch(() => {});
    if (active && active !== pending) nativeSsh.close(active).catch(() => {});
  }
  async forget(profileId: string): Promise<void> {
    if (this.owner?.profile === profileId) { this.stop(); this.owner = undefined; this.loaded = false; this.configuration = new SshConfiguration(); }
    await this.vault?.forget(profileId);
  }
}
