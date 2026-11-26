// @vitest-environment jsdom
// Settings › General › Agent preferences › "Seen Agents" (#12585): stay until refresh by default, leave when the chat
// closes one click away, kept by this browser.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { t } from "../../i18n";
import { getSeenVisit, noteShownUnseen, recordMarkedSeen, resetSeenVisit, SEEN_ROWS_KEY, SEEN_ROWS_NOTICED_KEY, seenRowsMode, seenRowsNoticed, setSeenRowsMode, setSeenRowsNoticed } from "../../lib/seen-rows";
import type { Session } from "../../lib/types";
import { SeenRowsRow } from "./SeenRowsSetting";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  resetSeenVisit();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<SeenRowsRow />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetSeenVisit();
  setSeenRowsMode("stay");
  setSeenRowsNoticed(false);
  localStorage.clear();
});

const radio = (label: string) => [...host.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === label)!;

describe("Seen Agents setting", () => {
  it("defaults to Stay until refresh on a fresh browser, with plain label and hint", () => {
    expect(host.textContent).toContain(t("seen-rows.label"));
    expect(host.textContent).toContain(t("seen-rows.hint"));
    expect(host.querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe(t("seen-rows.label"));
    expect(radio(t("seen-rows.stay")).getAttribute("aria-checked")).toBe("true");
    expect(radio(t("seen-rows.leave")).getAttribute("aria-checked")).toBe("false");
    expect(seenRowsMode()).toBe("stay");
    expect(seenRowsNoticed()).toBe(false);
  });

  it("switches to Leave when the chat closes, clears held rows, marks notice answered, and switches back", () => {
    const sample: Session = { machine_id: "m", session_id: "s1", title: "Done", cwd: "/p", status: "done", state: "unread" };
    noteShownUnseen({ "m/s1": "unread" });
    recordMarkedSeen(sample, "unread", false);
    expect(getSeenVisit().seen).toEqual({ "m/s1": "unread" });

    act(() => radio(t("seen-rows.leave")).click());
    expect(seenRowsMode()).toBe("leave");
    expect(localStorage.getItem(SEEN_ROWS_KEY)).toBe("leave");
    expect(localStorage.getItem(SEEN_ROWS_NOTICED_KEY)).toBe("1");
    expect(seenRowsNoticed()).toBe(true);
    expect(getSeenVisit().seen).toEqual({});
    expect(radio(t("seen-rows.leave")).getAttribute("aria-checked")).toBe("true");

    act(() => radio(t("seen-rows.stay")).click());
    expect(seenRowsMode()).toBe("stay");
    expect(localStorage.getItem(SEEN_ROWS_KEY)).toBeNull();
    expect(radio(t("seen-rows.stay")).getAttribute("aria-checked")).toBe("true");
  });

  it("follows a change made in another tab", () => {
    act(() => {
      localStorage.setItem(SEEN_ROWS_KEY, "leave");
      window.dispatchEvent(new StorageEvent("storage", { key: SEEN_ROWS_KEY }));
    });
    expect(radio(t("seen-rows.leave")).getAttribute("aria-checked")).toBe("true");
  });
});
