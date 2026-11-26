import { subscribeCredentialOwner } from "../../../lib/api";
import type { JsonObject } from "../../../lib/extensions";
import type { Typed } from "../Extensions";

type Draft = { base: JsonObject; draft: JsonObject; typed: Typed };

// Keep config edits in tab memory so the settings list can freely switch detail panes.
const drafts = new Map<string, Draft>();
export const integrationDraft = (key: string) => drafts.get(key);

function warn(event: BeforeUnloadEvent) {
  event.preventDefault();
  event.returnValue = true;
}

export function keepIntegrationDraft(key: string, value?: Draft) {
  if (value) drafts.set(key, value);
  else drafts.delete(key);
  if (drafts.size) window.addEventListener("beforeunload", warn);
  else window.removeEventListener("beforeunload", warn);
}

subscribeCredentialOwner(() => {
  drafts.clear();
  window.removeEventListener("beforeunload", warn);
});
