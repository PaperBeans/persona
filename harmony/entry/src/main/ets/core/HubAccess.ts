// A Cloudflare Access service token belongs to one Hub address. Only AssetStore receives its serialized form.
const STORED_PAIR = '\u0000persona.harmony.hub-access.v1\u0000';

export class HubAccess {
  readonly origin: string;
  private readonly clientID: string;
  private readonly clientSecret: string;

  private constructor(origin: string, clientID: string, clientSecret: string) {
    this.origin = origin;
    this.clientID = clientID;
    this.clientSecret = clientSecret;
  }

  static fromInput(origin: string, clientID: string, clientSecret: string): HubAccess | undefined {
    const id = clientID.trim(), secret = clientSecret.trim();
    if (!id && !secret) return undefined;
    if (!id || !secret) throw new Error('cloudflare_access_incomplete');
    if (!origin.startsWith('https://')) throw new Error('credentials_require_https');
    if (id.length > 1024 || secret.length > 4096 || /[\u0000-\u001f\u007f]/.test(id) || /[\u0000-\u001f\u007f]/.test(secret)) {
      throw new Error('cloudflare_access_invalid');
    }
    return new HubAccess(origin, id, secret);
  }

  apply(origin: string, headers: Record<string, string>): boolean {
    if (origin !== this.origin) return false;
    headers['CF-Access-Client-Id'] = this.clientID;
    headers['CF-Access-Client-Secret'] = this.clientSecret;
    return true;
  }

  equals(other?: HubAccess): boolean {
    return !!other && this.origin === other.origin && this.clientID === other.clientID && this.clientSecret === other.clientSecret;
  }

  toJSON(): string { return '[Cloudflare Access service token]'; }
  toString(): string { return this.toJSON(); }

  protect(deviceToken: string): string {
    return STORED_PAIR + JSON.stringify([deviceToken, this.origin, this.clientID, this.clientSecret]);
  }

  static restore(stored: string, receive?: (access: HubAccess | undefined) => void): string {
    // Previous releases stored the device token as plain UTF-8 inside the same private AssetStore item.
    // A valid legacy token cannot start with NUL. The complete token set changes in one asset update.
    if (!stored.startsWith(STORED_PAIR)) { receive?.(undefined); return stored; }
    const parts = JSON.parse(stored.slice(STORED_PAIR.length)) as string[];
    if (!Array.isArray(parts) || parts.length !== 4 || !parts.every((v: string) => typeof v === 'string') ||
        !parts[0] || parts[0].length > 8192 || /[\r\n\u0000]/.test(parts[0])) throw new Error('storage_unavailable');
    const edge = HubAccess.fromInput(parts[1], parts[2], parts[3]);
    if (!edge) throw new Error('storage_unavailable');
    receive?.(edge);
    return parts[0];
  }
}
