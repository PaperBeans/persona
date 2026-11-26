import type { Item } from "./types";

export interface UserInputPosition {
  id: string;
  seq: number;
  preview?: string;
  answer?: string;
  attachments?: number;
}
export interface UserInputIndex { total: number; inputs: UserInputPosition[]; history: string }

export interface UserInputPreviews { inputs: UserInputPosition[]; history: string }

/** Both the compact index and older hubs' index with excerpts use the same positions. */
export function validUserInputIndex(value: unknown): value is UserInputIndex {
  return validUserInputPreviews(value) && (value as UserInputIndex).total === value.inputs.length;
}

export function validUserInputPreviews(value: unknown): value is UserInputPreviews {
  if (!value || typeof value !== "object") return false;
  const index = value as UserInputPreviews;
  if (!Array.isArray(index.inputs) || typeof index.history !== "string") return false;
  const ids = new Set<string>();
  let last = 0;
  return index.inputs.every((input) => {
    if (!input || typeof input !== "object" || typeof input.id !== "string" || !input.id || ids.has(input.id) || !Number.isSafeInteger(input.seq) || input.seq < last || input.seq <= 0) return false;
    const { preview, answer, attachments } = input;
    if (preview !== undefined && typeof preview !== "string") return false;
    if (answer !== undefined && typeof answer !== "string") return false;
    if (attachments !== undefined && (!Number.isSafeInteger(attachments) || attachments < 0)) return false;
    ids.add(input.id); last = input.seq;
    return true;
  });
}

export const inputEntry = (id: string) => id.split("#")[0];

export function hasInputExcerpt(input: UserInputPosition): boolean {
  return Boolean(input.preview || input.answer || input.attachments);
}

/** Refresh on raw user-entry arrivals, including queued inputs merged into an existing bubble.
 * The server owns provenance filtering; tool-result frames never trigger a full-history scan. */
export function userInputRevision(items: Item[]): string | undefined {
  return items.findLast((item) => item.role === "user" && (item.kind === "text" || item.kind === "image") && !item.tool_use_id)?.id;
}

export function containsInput(items: Item[], input: UserInputPosition): boolean {
  return items.some((item) => item.seq === input.seq && inputEntry(item.id) === inputEntry(input.id));
}
