export interface NativeSshConfig {
  id: string;
  sshHost: string;
  sshPort: number;
  username: string;
  hostKey: string;
  targetHost: string;
  targetPort: number;
  forwardHost?: string;
  forwardPort?: number;
  resolvedHubIps: string[];
  privateKey: Uint8Array;
  passphrase: Uint8Array;
}
export interface NativeSshLease { id: string; host: string; port: number; username: string; password: string; }
export interface NativeSshHost { id: string; sshHost: string; sshPort: number; }
export interface NativeSshHostIdentity { id: string; algorithm: string; fingerprint: string; }
export interface NativeSshVerification { id: string; }
export interface NativeSshBridge {
  observeHost(host: NativeSshHost): Promise<NativeSshHostIdentity>;
  verifyTarget(config: NativeSshConfig): Promise<NativeSshVerification>;
  open(config: NativeSshConfig): Promise<NativeSshLease>;
  close(id: string): Promise<void>;
  isOpen(id: string): boolean;
}
declare const bridge: NativeSshBridge;
export default bridge;
