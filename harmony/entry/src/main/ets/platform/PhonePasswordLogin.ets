import { url, util } from '@kit.ArkTS';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { EndpointConfig, HubTransport } from './HubTransport';

function base64url(bytes: Uint8Array): string {
  return new util.Base64Helper().encodeToStringSync(bytes).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function loginOrigin(address: string): string {
  const text = address.trim();
  if (!text || text.length > 2048 || /[\s\\]/.test(text)) throw new Error('phone_password_address');
  try {
    const parsed = url.URL.parseURL(text.includes('://') ? text : 'https://' + text);
    if (parsed.protocol !== 'https:' || !parsed.hostname || parsed.username || parsed.password ||
      parsed.search || parsed.hash || (parsed.pathname && parsed.pathname !== '/')) throw new Error('phone_password_address');
    return 'https://' + parsed.host;
  } catch (_) { throw new Error('phone_password_address'); }
}

interface BrowserReturn {
  state: string;
  timer: number;
  accept: (code: string) => void;
  reject: (error: Error) => void;
}

// The system browser owns the password ceremony. The app holds a one-use PKCE verifier only in memory.
export class PhonePasswordLogin {
  private revision: number = 0;
  private pending: BrowserReturn | undefined;

  cancel(): void {
    this.revision++;
    const prior = this.pending;
    if (prior) {
      this.pending = undefined;
      clearTimeout(prior.timer);
      prior.reject(new Error('request_updated'));
    }
  }

  receive(value: string): boolean {
    const waiting = this.pending;
    if (!waiting || !value || value.length > 2048) return false;
    try {
      const callback = url.URL.parseURL(value);
      if (callback.protocol !== 'persona:' || callback.host !== 'password-login' || callback.username ||
        callback.password || callback.hash || (callback.pathname && callback.pathname !== '/')) return false;
      const states = callback.params.getAll('state'), codes = callback.params.getAll('code');
      if (states.length !== 1 || states[0] !== waiting.state || codes.length !== 1 || !/^[A-Za-z0-9_-]{43}$/.test(codes[0])) return false;
      this.pending = undefined;
      clearTimeout(waiting.timer);
      waiting.accept(codes[0]);
      return true;
    } catch (_) { return false; }
  }

  async authorize(address: string, deviceName: string, open: (page: string) => Promise<void>,
    admit: () => boolean): Promise<EndpointConfig> {
    this.cancel();
    const revision = this.revision, origin = loginOrigin(address);
    const current = (): boolean => revision === this.revision && admit();
    const random = cryptoFramework.createRandom();
    const state = base64url((await random.generateRandom(32)).data);
    const verifier = base64url((await random.generateRandom(32)).data);
    const digest = cryptoFramework.createMd('SHA256');
    await digest.update({ data: new util.TextEncoder().encodeInto(verifier) });
    const challenge = base64url((await digest.digest()).data);
    if (!current()) throw new Error('request_updated');
    const page = origin + '/login/device#state=' + state + '&challenge=' + challenge +
      '&kind=phone&name=' + encodeURIComponent(deviceName.slice(0, 70));
    const code = await new Promise<string>((resolve, reject) => {
      const fail = (error: Error): void => {
        if (this.pending?.state === state) {
          clearTimeout(this.pending.timer);
          this.pending = undefined;
          reject(error);
        }
      };
      const timer = setTimeout(() => fail(new Error('phone_password_expired')), 300000);
      this.pending = { state: state, timer: timer, accept: resolve, reject: reject };
      open(page).catch(() => fail(new Error('phone_password_browser')));
    });
    if (!current()) throw new Error('request_updated');
    const transport = new HubTransport(new EndpointConfig(origin, '', '', ''));
    try {
      const raw = await transport.request('/api/v1/auth/password/exchange', 'POST', JSON.stringify({ code, verifier }));
      if (!current()) throw new Error('request_updated');
      const result = JSON.parse(raw) as Record<string, Object>;
      const token = result['token'], id = result['id'], hub = result['hub_id'];
      if (typeof token !== 'string' || !token.startsWith('jd_') || typeof id !== 'string' ||
        !id.startsWith('dev_') || typeof hub !== 'string' || !hub.trim()) throw new Error('pairing_response_invalid');
      return new EndpointConfig(origin, hub, token, '');
    } finally { transport.close(); }
  }
}

export const phonePasswordLogin = new PhonePasswordLogin();
