// A nonsecret sign-in marker shared by tabs. It follows the same storage scope as persona.token.
export const CREDENTIAL_EPOCH_KEY = "persona.credentialEpoch";

export function credentialEpoch(): string {
  try { return localStorage.getItem(CREDENTIAL_EPOCH_KEY) ?? ""; }
  catch { return ""; }
}

let sequence = 0;
export function renewCredentialEpoch(): boolean {
  let marker: string;
  try { marker = globalThis.crypto.randomUUID(); }
  catch { marker = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}-${++sequence}`; }
  try {
    localStorage.setItem(CREDENTIAL_EPOCH_KEY, marker);
    return localStorage.getItem(CREDENTIAL_EPOCH_KEY) === marker;
  } catch {
    // Pairing already succeeded; this tab still retires its work, but other tabs cannot see the new epoch.
    return false;
  }
}
