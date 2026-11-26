import { asset } from '@kit.AssetStoreKit';
import { util } from '@kit.ArkTS';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { SshSecrets } from './SshCredentialVault';

// Separate from per-Hub imported keys. Forgetting a Hub leaves this phone's identity available for its other Hubs.
const ALIAS: string = 'persona.harmony.ssh.device-identity.v1';
const SPKI: number[] = [0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00];
const PKCS8: number[] = [0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20];
function bytes(s: string): Uint8Array { return new util.TextEncoder().encodeInto(s); }
function prefix(input: Uint8Array, expected: number[]): boolean {
  return input.length >= expected.length && expected.every((value: number, index: number): boolean => input[index] === value);
}
function keyPairRecord(publicDer: Uint8Array, privateDer: Uint8Array): Uint8Array {
  const rawPublic = publicDer.length === 32 ? publicDer : prefix(publicDer, SPKI) && publicDer.length === SPKI.length + 32 ? publicDer.subarray(SPKI.length) : undefined;
  const rawPrivate = privateDer.length === 32 ? privateDer : prefix(privateDer, PKCS8) && privateDer.length === PKCS8.length + 32 ? privateDer.subarray(PKCS8.length) : undefined;
  if (!rawPublic || !rawPrivate) throw new Error('ssh_invalid_key');
  const result = new Uint8Array(65); result[0] = 1; result.set(rawPublic, 1); result.set(rawPrivate, 33);
  return result;
}
function publicWire(stored: Uint8Array): Uint8Array {
  // RFC 4253 string "ssh-ed25519", then the 32-octet RFC 8709 public key.
  const wire = new Uint8Array(51); wire.set([0, 0, 0, 11]); wire.set(bytes('ssh-ed25519'), 4);
  wire.set([0, 0, 0, 32], 15); wire.set(stored.subarray(1, 33), 19); return wire;
}
function code(error: Error): number {
  const n = (error as Object as Record<string, Object>)['code']; return typeof n === 'number' ? n : 0;
}
function lookup(): asset.AssetMap {
  const q: asset.AssetMap = new Map(); q.set(asset.Tag.ALIAS, bytes(ALIAS)); return q;
}

export class SshPublicIdentity {
  readonly line: string;
  readonly fingerprint: string;
  constructor(wire: Uint8Array, comment: string) {
    const encoder = new util.Base64Helper(), hash = cryptoFramework.createMd('SHA256');
    hash.updateSync({data:wire});
    this.fingerprint = 'SHA256:' + encoder.encodeToStringSync(hash.digestSync().data).replace(/=+$/,'');
    const safeComment = comment.replace(/[\u0000-\u001f\u007f]/g, ' ').trim().slice(0, 100);
    this.line = 'ssh-ed25519 ' + encoder.encodeToStringSync(wire) + (safeComment ? ' ' + safeComment : '');
  }
  restrictedLine(host: string, port: number): string {
    const plain = host.replace(/^\[|\]$/g, '');
    if (!plain || plain.length > 253 || !/^[a-zA-Z0-9.:-]+$/.test(plain) || !Number.isInteger(port) || port < 1 || port > 65535) throw new Error('ssh_invalid_config');
    return 'restrict,port-forwarding,permitopen="' + (plain.includes(':') ? '[' + plain + ']' : plain) + ':' + port + '" ' + this.line;
  }
}

export class SshDeviceIdentity {
  private static creating: Promise<void> | undefined;
  private static async read(): Promise<Uint8Array | undefined> {
    const q = lookup(); q.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ALL);
    let rows: asset.AssetMap[];
    try { rows = await asset.query(q); }
    catch (error) { if (code(error as Error) === 24000002) return undefined; throw new Error('ssh_storage_failed'); }
    if (!rows.length) return undefined;
    const value = rows[0].get(asset.Tag.SECRET);
    if (!(value instanceof Uint8Array)) throw new Error('ssh_storage_failed');
    try {
      if (rows.length !== 1 || value.length !== 65 || value[0] !== 1) throw new Error('ssh_storage_failed');
      return value.slice();
    } finally { value.fill(0); }
  }
  private static async create(): Promise<void> {
    const pair = await cryptoFramework.createAsyKeyGenerator('Ed25519').generateKeyPair();
    let privateDer: Uint8Array | undefined, encoded: Uint8Array | undefined;
    try {
      privateDer = pair.priKey.getEncoded().data;
      encoded = keyPairRecord(pair.pubKey.getEncoded().data, privateDer);
      const entry = lookup(); entry.set(asset.Tag.SECRET, encoded);
      entry.set(asset.Tag.ACCESSIBILITY, asset.Accessibility.DEVICE_UNLOCKED);
      entry.set(asset.Tag.CONFLICT_RESOLUTION, asset.ConflictResolution.THROW_ERROR);
      entry.set(asset.Tag.AUTH_TYPE, asset.AuthType.NONE); entry.set(asset.Tag.SYNC_TYPE, asset.SyncType.NEVER);
      try { await asset.add(entry); }
      catch (error) { if (code(error as Error) !== 24000003) throw new Error('ssh_storage_failed'); }
    } finally { pair.priKey.clearMem(); privateDer?.fill(0); encoded?.fill(0); }
  }
  private static async stored(): Promise<Uint8Array> {
    const existing = await SshDeviceIdentity.read(); if (existing) return existing;
    const creating = SshDeviceIdentity.creating || SshDeviceIdentity.create();
    SshDeviceIdentity.creating = creating;
    try { await creating; } finally { if (SshDeviceIdentity.creating === creating) SshDeviceIdentity.creating = undefined; }
    const saved = await SshDeviceIdentity.read(); if (!saved) throw new Error('ssh_storage_failed');
    return saved;
  }
  static async publicIdentity(comment: string): Promise<SshPublicIdentity> {
    const record = await SshDeviceIdentity.stored();
    try { return new SshPublicIdentity(publicWire(record), comment); } finally { record.fill(0); }
  }
  static async secrets(admit: () => boolean): Promise<SshSecrets> {
    if (!admit()) throw new Error('ssh_cancelled');
    const record = await SshDeviceIdentity.stored(), der = new Uint8Array(PKCS8.length + 32);
    try {
      if (!admit()) throw new Error('ssh_cancelled');
      der.set(PKCS8); der.set(record.subarray(33), PKCS8.length);
      const result = new SshSecrets();
      result.privateKey = bytes('-----BEGIN PRIVATE KEY-----\n' + new util.Base64Helper().encodeToStringSync(der) + '\n-----END PRIVATE KEY-----\n');
      return result;
    } finally { record.fill(0); der.fill(0); }
  }
}
