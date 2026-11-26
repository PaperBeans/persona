// The sidebar head and conversation title share the same live or remembered assistant identity.
// Display only: the remembered identity never changes routing or the current manager.
import { useSyncExternalStore } from "react";
import { pickedName, useChosenManager, useShownIdentity, type ShownIdentity } from "./manager-choice";
import { currentRoster, subscribeRoster } from "./managers";

const hasRoster = () => currentRoster() !== null;

export function useAssistantIdentity(): ShownIdentity | null {
  const loaded = useSyncExternalStore(subscribeRoster, hasRoster, hasRoster);
  const remembered = useShownIdentity();
  const picked = useChosenManager();
  // setRoster records the live identity separately, so status and count pushes leave the chat's title alone.
  if (!loaded) {
    if (picked) {
      const same = remembered?.id === picked;
      return {
        id: picked,
        name: same ? remembered.name : pickedName() ?? picked,
        isDefault: false,
        ...(same && remembered.line ? { line: remembered.line } : {}),
        ...(same && remembered.tone ? { tone: remembered.tone } : {}),
      };
    }
    return remembered ? { ...remembered, isDefault: remembered.isDefault ?? true } : null;
  }
  return remembered;
}
