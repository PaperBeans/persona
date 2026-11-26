import type { AssistantInfo } from "./types";

export const ASSISTANT_LABS = ["work_guide", "todos_v2", "integrations_v2"] as const;
export const TERMINAL_LABS_ID = "terminal";
export const LABS_CATALOG = [...ASSISTANT_LABS, TERMINAL_LABS_ID] as const;
export type LabId = (typeof LABS_CATALOG)[number];
export type LabSettings = Pick<AssistantInfo, (typeof ASSISTANT_LABS)[number]>;

function isAvailable(id: LabId, settings: LabSettings): boolean {
  return id === TERMINAL_LABS_ID || typeof settings[id] === "boolean";
}

export function visibleLabs(settings: LabSettings): LabId[] {
  return LABS_CATALOG.filter((id) => isAvailable(id, settings));
}

export function unseenLabs(settings: LabSettings, webgl: boolean, seen: readonly string[], pending: readonly string[] = []): LabId[] {
  return LABS_CATALOG.filter((id) => isUnseenLab(id, settings, webgl, seen, pending));
}

export function isUnseenLab(id: LabId, settings: LabSettings, webgl: boolean, seen: readonly string[], pending: readonly string[]): boolean {
  return isAvailable(id, settings)
    && (id === TERMINAL_LABS_ID ? !webgl : settings[id] !== true)
    && !seen.includes(id) && !pending.includes(id);
}
