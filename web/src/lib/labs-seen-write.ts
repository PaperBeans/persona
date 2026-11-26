import { api, credentialOwner } from "./api";
import type { LabId } from "./labs-catalog";
import { pendingLabsSeen, saveLocalLabsSeen } from "./labs-seen";
import { MANAGER_HEADER } from "./manager-choice";
import { acceptLabsSeen } from "./store";
import type { AssistantInfo } from "./types";

export async function markLabsSeen(manager: string, ids: readonly LabId[], synced: boolean) {
  if (!ids.length) return;
  if (!synced) {
    saveLocalLabsSeen(manager, ids);
    return;
  }
  const owner = credentialOwner();
  const pending = pendingLabsSeen(manager, ids);
  if (!pending) return;
  try {
    const result = await api<Pick<AssistantInfo, "manager" | "labs_seen">>("/assistant", {
      method: "PUT", headers: { [MANAGER_HEADER]: manager }, json: { labs_seen: pending.ids }, timeout: 10_000,
    });
    if (credentialOwner() === owner && result.manager === manager && result.labs_seen) acceptLabsSeen(manager, result.labs_seen);
  } catch {
    // The additive receipt can be tried again on the next visit. Keep any receipts learned from other devices.
  } finally {
    pending.settle();
  }
}
