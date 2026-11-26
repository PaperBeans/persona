import type { Pairing } from '../platform/Pairing';
import { util } from '@kit.ArkTS';
import nativeSsh from 'libpersona_ssh.so';
import type { NativeSshConfig, NativeSshHostIdentity } from 'libpersona_ssh.so';
import type { TransportProxy } from '../platform/HubTransport';
import { SshDeviceIdentity } from '../platform/SshDeviceIdentity';
import { SshSecrets } from '../platform/SshCredentialVault';
import { SshConfiguration, SshProxyLease, SshTunnel } from './SshTunnel';
import type { ClientViewLease } from './ClientModel';

export interface PairingSshRoute {
  admitDirect?: (address: string, pin: string) => void;
  directCurrent?: (address: string, pin: string) => boolean;
  open: (address: string, pin: string) => Promise<TransportProxy>;
  install: (hub: string, address: string, pin: string, view: ClientViewLease, admit: () => boolean) => Promise<(committed: boolean) => Promise<void>>;
  close: () => void;
}

/** A prepared SSH connection belongs to this pairing sheet until the verified Hub redeems a device token. */
@ObservedV2
export class PairingSsh {
  @Trace configured: boolean = false;
  @Trace label: string = '';
  @Trace configuration: SshConfiguration = new SshConfiguration();
  @Trace hubPin: string = '';
  private material: SshSecrets | undefined;
  private request: PairingSshRoute | undefined;
  private checkId: string = '';
  private checkVersion: number = 0;

  serverRevision(): number { return this.checkVersion; }
  cancelServerCheck(): void {
    const id = this.checkId; this.checkId = ''; this.checkVersion++;
    if (id) nativeSsh.close(id).catch(() => {});
  }
  private async serverCheck<T>(work: (id: string, current: () => boolean) => Promise<T>): Promise<T> {
    this.cancelServerCheck();
    const id = util.generateRandomUUID(), current = (): boolean => this.checkId === id;
    this.checkId = id;
    try {
      const result = await work(id, current);
      if (!current()) throw new Error('ssh_cancelled');
      return result;
    } finally { if (current()) this.checkId = ''; }
  }
  async observeServer(host: string, port: number): Promise<NativeSshHostIdentity> {
    return this.serverCheck(async (id: string): Promise<NativeSshHostIdentity> => {
      const identity = await nativeSsh.observeHost({ id: id, sshHost: host, sshPort: port });
      if (identity.id !== id || !/^[a-zA-Z0-9@.+-]{1,96}$/.test(identity.algorithm) ||
        !/^SHA256:[A-Za-z0-9+/]{43}$/.test(identity.fingerprint)) throw new Error('ssh_native_failed');
      return identity;
    });
  }
  async testForward(config: SshConfiguration, privateKey: string, passphrase: string, deviceKey: boolean,
    targetHost: string, targetPort: number): Promise<void> {
    SshTunnel.validateServer(config);
    await this.serverCheck(async (id: string, current: () => boolean): Promise<void> => {
      const secrets = deviceKey ? await SshDeviceIdentity.secrets(current) :
        privateKey ? SshSecrets.fromText(privateKey, passphrase) : this.material?.copy();
      if (!secrets) throw new Error('ssh_key_required');
      try {
        if (!current()) throw new Error('ssh_cancelled');
        const request: NativeSshConfig = { id: id, sshHost: config.sshHost, sshPort: config.sshPort, username: config.username,
          hostKey: config.hostKey, targetHost: targetHost, targetPort: targetPort, resolvedHubIps: [],
          privateKey: secrets.privateKey, passphrase: secrets.passphrase };
        const checking = nativeSsh.verifyTarget(request); secrets.clear();
        if ((await checking).id !== id) throw new Error('ssh_native_failed');
      } finally { secrets.clear(); }
    });
  }

  configure(config: SshConfiguration, privateKey: string, passphrase: string, hubPin: string, confirmed: boolean, useDeviceKey: boolean = false): void {
    const next = config.copy(); next.sshHost = next.sshHost.trim(); next.username = next.username.trim();
    next.hostKey = next.hostKey.trim(); next.keyRef = ''; next.enabled = true;
    SshTunnel.validateServer(next);
    const pin = hubPin.trim();
    if (!confirmed || (pin && !/^[A-Za-z0-9_-]{43}$/.test(pin))) throw new Error('ssh_invalid_config');
    next.deviceIdentity = useDeviceKey && !privateKey;
    if (!privateKey && ((!this.material && !next.deviceIdentity) || passphrase)) throw new Error('ssh_key_required');
    const replacement = privateKey ? SshSecrets.fromText(privateKey, passphrase) : undefined;
    this.cancelAttempt();
    if (replacement || next.deviceIdentity) { this.material?.clear(); this.material = replacement; }
    this.configuration = next; this.hubPin = pin; this.configured = true;
    this.label = next.username + '@' + next.sshHost + (next.sshPort === 22 ? '' : ':' + String(next.sshPort));
  }

  prepare(pairing: Pairing, admit: () => boolean): Pairing {
    this.cancelAttempt();
    const material = this.material;
    if (!this.configured || (!material && !this.configuration.deviceIdentity)) return pairing;
    const config = this.configuration.copy(), tunnel = new SshTunnel();
    let active = true, direct = false, endpoint = '', pinned = '', lease: SshProxyLease | undefined;
    const current = (): boolean => active && admit() && this.material === material;
    const route: PairingSshRoute = {
      open: async (address: string, pin: string): Promise<TransportProxy> => {
        if (!current() || (endpoint && (endpoint !== address || pin !== pinned))) throw new Error('ssh_cancelled');
        endpoint = address; pinned = pin; direct = false;
        if (lease?.isCurrent()) return lease;
        const selected = config.deviceIdentity ? await SshDeviceIdentity.secrets(current) : material?.copy();
        if (!selected) throw new Error('ssh_key_required');
        try { lease = await tunnel.openPairing(address, pin, config, selected, current); }
        finally { selected.clear(); }
        if (!current() || !lease.isCurrent()) throw new Error('ssh_cancelled');
        return lease;
      },
      install: async (hub: string, address: string, pin: string, view: ClientViewLease, owns: () => boolean): Promise<(committed: boolean) => Promise<void>> => {
        const valid = (): boolean => current() && owns() && (direct || !!lease?.isCurrent()) && endpoint === address && pinned === pin;
        if (!valid()) throw new Error('ssh_cancelled');
        return SshTunnel.stagePairing(hub, config, material, view, valid);
      },
      close: (): void => { active = false; tunnel.stop(); lease = undefined; }
    };
    if (config.routePolicy === 'fallback') {
      route.admitDirect = (address: string, pin: string): void => {
        if (!current() || (endpoint && (endpoint !== address || pinned !== pin))) throw new Error('ssh_cancelled');
        tunnel.stop(); lease = undefined; direct = true; endpoint = address; pinned = pin;
      };
      route.directCurrent = (address: string, pin: string): boolean => current() && direct && endpoint === address && pinned === pin;
    }
    pairing.setSshRoute(route, this.hubPin); this.request = route;
    return pairing;
  }

  cancelAttempt(): void { this.cancelServerCheck(); this.request?.close(); this.request = undefined; }
  clear(): void {
    this.cancelAttempt(); this.material?.clear(); this.material = undefined;
    this.configuration = new SshConfiguration(); this.hubPin = ''; this.label = ''; this.configured = false;
  }
}
