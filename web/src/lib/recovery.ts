// Settings > Hub > Recovery: read-only whole-install bundle discovery and the
// two explicit checks. There is intentionally no create, restore, drill,
// download or key-export client API.
import { api } from "./api";

export interface RecoveryCapability {
  state: "supported" | "guided" | "unsupported" | "unavailable" | string;
  reason?: string;
}

export interface RecoveryBundle {
  id: string;
  revision: string;
  name: string;
  archive_bytes: number;
  modified_at: string;
}

export interface RecoveryOverview {
  capabilities: {
    inspect: RecoveryCapability;
    verify: RecoveryCapability;
    create: RecoveryCapability;
    restore: RecoveryCapability;
    download: RecoveryCapability;
  };
  discovery: RecoveryCapability;
  history: RecoveryCapability;
  bundles: RecoveryBundle[];
}

export interface RecoveryInspection {
  check: "offline_integrity" | string;
  format: number;
  created_at: string;
  archive_bytes: number;
  database_restore_verified: boolean;
  hub_dump: { files: number; bytes: number };
  manager_dump: { files: number; bytes: number };
  runtime: { files: number; bytes: number };
  knowledge_base: { state: string; location: string; files: number; bytes: number };
}

export interface RecoveryCheck {
  kind: "offline_integrity" | "database_restore" | string;
  state: "passed" | "failed" | "unsupported" | string;
  reason?: string;
  checked_at: string;
  bundle: RecoveryBundle;
  report?: RecoveryInspection;
}

export const recoveryOverview = () => api<RecoveryOverview>("/hub/recovery");
export const inspectRecoveryBundle = (id: string) => api<RecoveryCheck>(`/hub/recovery/bundles/${encodeURIComponent(id)}/inspect`, { method: "POST" });
export const verifyRecoveryBundle = (id: string) => api<RecoveryCheck>(`/hub/recovery/bundles/${encodeURIComponent(id)}/verify`, { method: "POST" });

export const RECOVERY_CREATE_COMMAND = 'personad backup create --output "$HOME/.persona/backups/persona-$(date +%Y%m%d-%H%M%S).tar" --writes-quiesced';
export const RECOVERY_VERIFY_COMMAND = "personad backup verify --bundle '<backup file>'";
