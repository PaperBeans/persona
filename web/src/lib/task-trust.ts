import { api, credentialOwner, getToken, onCredentialChange, subscribeCredentialOwner } from "./api";
import { MANAGER_HEADER, onDefaultManager, onManagerChoice, shownManager } from "./manager-choice";

export interface TaskTrustRule { key: string; machine: string; cwd: string; readonly: boolean; ambiguous?: boolean }
export interface TaskTrustScope { manager: string; owner: string; token: string }

// A round trip away from and back to the same assistant still retires its old confirmations and reads.
let managerGeneration = 0;
onManagerChoice(() => { managerGeneration++; });
onDefaultManager(() => { managerGeneration++; });
export const taskTrustContext = () => JSON.stringify([credentialOwner(), shownManager(), managerGeneration]);
export function subscribeTaskTrustContext(listener: () => void): () => void {
  const stops = [subscribeCredentialOwner(listener), onManagerChoice(listener), onDefaultManager(listener)];
  return () => stops.forEach((stop) => stop());
}

export function captureTaskTrustScope(manager: string): TaskTrustScope {
  return { manager, owner: credentialOwner(), token: getToken() };
}

function options(scope: TaskTrustScope, isAuthContextCurrent: () => boolean) {
  return { token: scope.token, tokenOwner: scope.owner, isAuthContextCurrent, headers: { [MANAGER_HEADER]: scope.manager } };
}

export async function listTaskTrust(scope: TaskTrustScope, signal: AbortSignal, isAuthContextCurrent: () => boolean): Promise<TaskTrustRule[]> {
  const response = await api<{ kinds?: unknown }>("/task-trust", { ...options(scope, isAuthContextCurrent), signal });
  if (!Array.isArray(response.kinds) || !response.kinds.every((kind: unknown) => {
    if (!kind || typeof kind !== "object") return false;
    const row = kind as Record<string, unknown>;
    return typeof row.key === "string" && typeof row.machine === "string" && typeof row.cwd === "string" && typeof row.readonly === "boolean"
      && (row.ambiguous === undefined || typeof row.ambiguous === "boolean");
  })) throw new Error("Invalid task trust response");
  return response.kinds as TaskTrustRule[];
}

export async function revokeTaskTrust(scope: TaskTrustScope, key: string, isAuthContextCurrent: () => boolean): Promise<void> {
  const response = await api<{ ok?: unknown }>(`/task-trust?key=${encodeURIComponent(key)}`, { ...options(scope, isAuthContextCurrent), method: "DELETE" });
  if (response.ok !== true) throw new Error("Unconfirmed task trust removal");
}

export type TrustFailure = "forbidden" | "not_found" | "unavailable" | "unconfirmed";
export interface TrustLedger {
  owner: string;
  manager: string;
  revision: number;
  pending: TaskTrustRule | null;
  failed: Map<string, { rule: TaskTrustRule; reason: TrustFailure }>;
  removed: Map<string, number>;
  retired: boolean;
  listeners: Set<(settled: boolean) => void>;
}

// An uncertain deletion must remain retryable even if GET omits it or the user leaves this settings page.
// This is session memory only, partitioned by the captured authorization generation and assistant.
const ledgers = new Map<string, TrustLedger>();
onCredentialChange(() => {
  for (const ledger of ledgers.values()) ledger.retired = true;
  ledgers.clear();
});

export function taskTrustLedger(scope: TaskTrustScope): TrustLedger {
  const id = JSON.stringify([scope.owner, scope.manager]);
  let ledger = ledgers.get(id);
  if (!ledger) {
    ledger = { owner: scope.owner, manager: scope.manager, revision: 0, pending: null, failed: new Map(), removed: new Map(), retired: false, listeners: new Set() };
    ledgers.set(id, ledger);
  }
  return ledger;
}

export function emitTaskTrust(ledger: TrustLedger, settled: boolean) {
  ledger.revision++;
  ledger.listeners.forEach((listener) => listener(settled));
}
