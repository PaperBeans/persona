import type { PairingSshRoute } from '../models/PairingSsh';
import type { TransportProxy } from './HubTransport';
import { HubAccess } from '../core/HubAccess';
import { isPairingScheme } from '../core/WireCompatibility';
import { url } from '@kit.ArkTS';
import { EndpointConfig, HubTransport, hubCredentialOrigin, normalizeBaseUrl } from './HubTransport';

export class PairingTarget {
  readonly baseUrl: string;
  readonly hubId: string;
  readonly name: string;
  readonly pin: string;
  constructor(baseUrl: string, hubId: string, name: string, pin: string) {
    this.baseUrl = baseUrl; this.hubId = hubId; this.name = name; this.pin = pin;
  }
}

function safeString(data: Record<string, Object>, key: string): string {
  const value = data[key];
  return typeof value === 'string' ? value as string : '';
}

function only(params: url.URLParams, name: string, required: boolean = false): string {
  const values = params.getAll(name);
  if (values.length > 1 || (required && values.length !== 1)) throw new Error('invalid_pairing');
  return values.length === 1 ? values[0] : '';
}

export class Pairing {
  private readonly baseUrl: string;
  private selectedUrl: string;
  private readonly addresses: string[];
  private readonly pairingCode: string;
  private readonly expectedHubId: string;
  private pin: string;
  private route: PairingSshRoute | undefined;
  private proxy: TransportProxy | undefined;
  private inspectedKey: string = '';
  private access: HubAccess | undefined;
  private used: boolean = false;
  private redeeming: boolean = false;

  private constructor(baseUrl: string, code: string, hubId: string, pin: string, alternates: string[] = []) {
    this.baseUrl = normalizeBaseUrl(baseUrl); this.selectedUrl = this.baseUrl;
    this.addresses = Pairing.addresses(this.baseUrl, alternates);
    this.pairingCode = Pairing.validateCode(code);
    this.expectedHubId = hubId;
    this.pin = pin;
  }

  get initialAddress(): string { return this.baseUrl; }

  private static validateCode(code: string): string {
    const value = code.trim();
    if (!value || value.length > 8192 || /[\r\n\u0000]/.test(value)) throw new Error('invalid_pairing');
    return value;
  }

  static manual(baseUrl: string, code: string, pin: string = ''): Pairing {
    return new Pairing(baseUrl, code, '', pin);
  }

  static parse(input: string): Pairing {
    if (!input || input.length > 16384) throw new Error('invalid_pairing');
    let parsed: url.URL;
    try { parsed = url.URL.parseURL(input.trim()); } catch (_) { throw new Error('invalid_pairing'); }
    const scheme = parsed.protocol.toLowerCase();
    if (!isPairingScheme(scheme) || parsed.hostname.toLowerCase() !== 'pair' || (parsed.pathname !== '' && parsed.pathname !== '/'))
      throw new Error('invalid_pairing');
    if (parsed.username || parsed.password || parsed.hash) throw new Error('invalid_pairing');
    const params = parsed.params;
    let baseUrl = only(params, 'url', true);
    const code = only(params, 'token', true);
    const hubId = only(params, 'hub');
    const pin = only(params, 'fp');
    const tlsText = only(params, 'tls');
    // Hub pairing QR codes keep the legacy HTTP URL and carry the TLS listener port separately.
    if (tlsText) {
      const tlsPort = Number(tlsText);
      if (!Number.isInteger(tlsPort) || tlsPort < 1 || tlsPort > 65535) throw new Error('invalid_pairing');
      try {
        const endpoint = url.URL.parseURL(baseUrl);
        endpoint.protocol = 'https:';
        endpoint.port = String(tlsPort);
        baseUrl = endpoint.toString();
      } catch (_) { throw new Error('invalid_pairing'); }
    }
    const allowed = ['url', 'token', 'hub', 'fp', 'tls', 'alt'];
    params.forEach((_value: string, key: string) => { if (!allowed.includes(key)) throw new Error('invalid_pairing'); });
    if (hubId && hubId.length > 128) throw new Error('invalid_pairing');
    return new Pairing(baseUrl, code, hubId, pin, params.getAll('alt'));
  }

  setSshRoute(route: PairingSshRoute, suppliedPin: string): void {
    if (this.used || this.redeeming || this.inspectedKey || this.route) throw new Error('pairing_target_changed');
    if (this.pin && suppliedPin && this.pin !== suppliedPin) throw new Error('invalid_pin');
    const pin = this.pin || suppliedPin;
    if (!/^[A-Za-z0-9_-]{43}$/.test(pin) || !this.baseUrl.startsWith('https://')) throw new Error('ssh_invalid_config');
    this.pin = pin; this.route = route;
  }
  get sshRoute(): PairingSshRoute | undefined { return this.route; }
  setCloudflareAccess(clientID: string, clientSecret: string): void {
    if (this.used || this.redeeming || this.inspectedKey) throw new Error('pairing_target_changed');
    this.access = HubAccess.fromInput(hubCredentialOrigin(this.baseUrl), clientID, clientSecret);
  }

  private targetKey(target: PairingTarget): string {
    return JSON.stringify([target.baseUrl, target.hubId, target.name, target.pin]);
  }

  private static addresses(primary: string, alternates: string[]): string[] {
    if (alternates.length > 8) throw new Error('invalid_pairing');
    const values = new Set<string>([normalizeBaseUrl(primary)]);
    for (const value of alternates) { const address = normalizeBaseUrl(value); if (!address.startsWith('https://')) throw new Error('invalid_pairing'); values.add(address); }
    return Array.from(values);
  }
  private static async verifyAddress(address: string, expectedHubId: string, pin: string, access?: HubAccess, proxy?: TransportProxy, deadline: number = 0): Promise<PairingTarget> {
    const anonymous = new HubTransport(new EndpointConfig(address, expectedHubId, '', pin, '', [], proxy, access));
    let timer = -1;
    try {
      const request = anonymous.request('/api/v1/health');
      const raw = deadline > 0 ? await Promise.race([request, new Promise<never>((_resolve, reject) => {
        timer = setTimeout((): void => { reject(new Error('hub_unavailable')); anonymous.close(); }, deadline);
      })]) : await request;
      const data = JSON.parse(raw) as Record<string, Object>;
      if (data['ok'] !== true) throw new Error('hub_unavailable');
      const hubId = safeString(data, 'hub_id'), name = safeString(data, 'hub_name');
      if (!hubId || (expectedHubId && hubId !== expectedHubId)) throw new Error('hub_identity_mismatch');
      return new PairingTarget(address, hubId, name || 'Persona hub', pin);
    } finally { if (timer !== -1) clearTimeout(timer); anonymous.close(); }
  }
  static async reachable(primary: string, alternatives: string[], hubId: string, pin: string, admit: () => boolean = (): boolean => true, access?: HubAccess, requireCheck: boolean = false): Promise<string> {
    if (!admit()) throw new Error('request_updated');
    // Reconnection can transfer credentials only between advertised addresses of the same pinned Hub.
    if (requireCheck && (!hubId || !pin)) throw new Error('invalid_pin');
    if (!hubId || !pin || (!alternatives.length && !requireCheck)) return primary;
    let failure: Error = new Error('hub_unavailable');
    let accessFailure: Error | undefined;
    for (const address of Pairing.addresses(primary, alternatives)) {
      if (!admit()) throw new Error('request_updated');
      try { const target = await Pairing.verifyAddress(address, hubId, pin, access, undefined, requireCheck ? 3000 : 0); if (!admit()) throw new Error('request_updated'); return target.baseUrl; }
      catch (error) {
        failure = error as Error;
        if (failure.message === 'cloudflare_access_required' || failure.message === 'cloudflare_access_rejected') accessFailure = failure;
      }
    }
    throw accessFailure || failure;
  }
  private routeCurrent(): boolean {
    return !this.route || (this.proxy ? this.proxy.isCurrent() : this.route.directCurrent?.(this.selectedUrl, this.pin) === true);
  }
  async inspect(): Promise<PairingTarget> {
    if (this.used) throw new Error('pairing_already_used');
    let failure: Error = new Error('hub_unavailable');
    let accessFailure: Error | undefined;
    const access = this.access, route = this.route;
    // The native forward is restricted to its selected logical Hub. Never send this pairing to an alternate directly.
    const addresses = route ? [this.baseUrl] : this.addresses;
    for (const address of addresses) {
      if (access !== this.access) throw new Error('pairing_target_changed');
      try {
        let target: PairingTarget | undefined, proxy: TransportProxy | undefined;
        if (route?.admitDirect) {
          try { target = await Pairing.verifyAddress(address,this.expectedHubId,this.pin,access,undefined,3000); }
          catch (_) { /* The same pinned authority can also be reached through the configured SSH server. */ }
          if (target) route.admitDirect(address,this.pin);
        }
        if (!target) {
          proxy = route ? await route.open(address, this.pin) : undefined;
          target = await Pairing.verifyAddress(address, this.expectedHubId, this.pin, access, proxy);
        }
        if (route && ((proxy && !proxy.isCurrent()) || this.route !== route)) throw new Error('ssh_cancelled');
        this.proxy = proxy;
        if (access !== this.access) throw new Error('pairing_target_changed');
        this.selectedUrl = address; this.inspectedKey = this.targetKey(target); return target;
      } catch (error) {
        failure = error as Error;
        if (failure.message === 'cloudflare_access_required' || failure.message === 'cloudflare_access_rejected') accessFailure = failure;
      }
    }
    route?.close(); throw accessFailure || failure;
  }

  async redeem(target: PairingTarget, deviceName: string = 'HarmonyOS device', admit: () => boolean = (): boolean => true): Promise<EndpointConfig> {
    if (this.used || this.redeeming || !admit() || !this.inspectedKey || this.targetKey(target) !== this.inspectedKey)
      throw new Error('pairing_target_not_confirmed');
    if (!this.routeCurrent()) throw new Error('ssh_cancelled');
    const baseUrl = normalizeBaseUrl(target.baseUrl);
    if (baseUrl !== this.selectedUrl || target.pin !== this.pin || target.hubId !== this.expectedHubId && this.expectedHubId !== '')
      throw new Error('pairing_target_changed');

    this.redeeming = true;
    try {
    // Recheck the exact endpoint and hub id after the user's confirmation, without sending the pairing token.
    const verified = await Pairing.verifyAddress(this.selectedUrl, target.hubId, this.pin, this.access, this.proxy);
    if (!admit()) throw new Error('request_updated');
    if (!this.routeCurrent()) throw new Error('ssh_cancelled');
    if (this.targetKey(verified) !== this.inspectedKey) throw new Error('hub_identity_changed');

    const name = deviceName.trim().slice(0, 128) || 'HarmonyOS device';
    const transport = new HubTransport(new EndpointConfig(target.baseUrl, target.hubId, this.pairingCode, target.pin, '', [], this.proxy, this.access));
    this.used = true;
    try {
      const raw = await transport.request('/api/v1/auth/tokens', 'POST', JSON.stringify({ name: name, kind: 'phone' }));
      const result = JSON.parse(raw) as Record<string, Object>;
      const deviceToken = safeString(result, 'token');
      const deviceId = safeString(result, 'id');
      if (!deviceToken || !deviceId) throw new Error('pairing_response_invalid');
      if (!admit() || !this.routeCurrent()) throw new Error('request_updated');
      return new EndpointConfig(target.baseUrl, target.hubId, deviceToken, target.pin, '', this.addresses.filter((address: string) => address !== target.baseUrl && address.startsWith('https://')), undefined, this.access);
    } finally { transport.close(); }
    } catch (error) { this.route?.close(); throw error as Error; }
    finally { this.redeeming = false; }
  }
}
