// @vitest-environment jsdom
// Cross-machine engine configuration isolation in EngineSection and EngineHostsSection: switching computers must discard
// prior unsaved drafts/errors immediately and never let an in-flight PUT from the previous computer overwrite the new one.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { Machine } from "../../lib/types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const mocks = vi.hoisted(() => ({
  api: vi.fn(),
  applyMachine: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../../lib/api", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/api")>();
  return { ...real, api: (...args: unknown[]) => mocks.api(...args) };
});

vi.mock("../../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../lib/store")>();
  return { ...real, applyMachine: (...args: unknown[]) => mocks.applyMachine(...args) };
});

vi.mock("../../ui/atoms", async (importOriginal) => {
  const real = await importOriginal<typeof import("../../ui/atoms")>();
  return { ...real, toast: (...args: unknown[]) => mocks.toast(...args) };
});

import { EngineHostsSection } from "./EngineHostsSection";
import { EngineSection } from "./EngineSection";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function singleMachine(id: string, engine: string): Machine {
  return {
    id,
    name: id,
    online: true,
    engine,
    engines: [
      { id: "herdr", name: "herdr", available: true },
      { id: "orca", name: "Orca", available: true },
      { id: "tern", name: "Tern", available: true },
      { id: "paseo", name: "Paseo", available: true },
    ],
  } as Machine;
}

function hostsMachine(id: string, hosts: string[], def: string): Machine {
  return {
    id,
    name: id,
    online: true,
    engine: def,
    engine_hosts: hosts,
    engine_default: def,
    engines: [
      { id: "herdr", name: "herdr", available: true },
      { id: "orca", name: "Orca", available: true },
      { id: "tern", name: "Tern", available: true },
      { id: "paseo", name: "Paseo", available: true },
    ],
  } as Machine;
}

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const render = async (node: React.ReactNode) => {
  await act(async () => root.render(node));
  await flush();
};

const radio = (id: string) => host.querySelector<HTMLButtonElement>(`button[role="radio"][data-engine="${id}"]`);
const checkbox = (id: string) => host.querySelector<HTMLButtonElement>(`[data-engine="${id}"] button[role="checkbox"]`);
const buttonByText = (label: string) =>
  [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);

beforeEach(() => {
  mocks.api.mockReset();
  mocks.applyMachine.mockReset();
  mocks.toast.mockReset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("EngineSection (single-engine) cross-machine isolation", () => {
  it("discards an unsaved engine draft and Save bar when switching to another machine", async () => {
    const a = singleMachine("machine-a", "herdr");
    const b = singleMachine("machine-b", "tern");

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    expect(radio("herdr")?.getAttribute("aria-checked")).toBe("true");

    await act(async () => radio("orca")!.click());
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeDefined();

    await render(<EngineSection machine={b} heading={<h2>Engine</h2>} />);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("ignores a stale in-flight save resolution after switching machines while still updating the store", async () => {
    const a = singleMachine("machine-a", "herdr");
    const b = singleMachine("machine-b", "tern");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // Switch to machine-b while machine-a's PUT is still in flight, and begin an edit on machine-b.
    await render(<EngineSection machine={b} heading={<h2>Engine</h2>} />);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(radio("paseo")?.disabled).toBe(false);
    await act(async () => radio("paseo")!.click());
    expect(radio("paseo")?.getAttribute("aria-checked")).toBe("true");

    const savedA = singleMachine("machine-a", "orca");
    await act(async () => {
      req.resolve(savedA);
      await Promise.resolve();
    });
    await flush();

    expect(mocks.applyMachine).toHaveBeenCalledWith(savedA);
    expect(radio("paseo")?.getAttribute("aria-checked")).toBe("true");
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeDefined();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("does not display an in-flight save error from the previous machine on the newly selected machine", async () => {
    const a = singleMachine("machine-a", "herdr");
    const b = singleMachine("machine-b", "tern");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    await render(<EngineSection machine={b} heading={<h2>Engine</h2>} />);
    await act(async () => {
      req.reject(new Error("hub rejected machine-a"));
      await Promise.resolve();
    });
    await flush();

    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");
  });

  it("sends only one PUT on rapid double-click before re-render", async () => {
    const a = singleMachine("machine-a", "herdr");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    const saveBtn = buttonByText(t("common.save"))!;
    act(() => {
      saveBtn.click();
      saveBtn.click();
    });
    expect(mocks.api).toHaveBeenCalledTimes(1);

    const savedA = singleMachine("machine-a", "orca");
    await act(async () => {
      req.resolve(savedA);
      await Promise.resolve();
    });
    await flush();
    expect(mocks.toast).toHaveBeenCalledWith(t("machines-enginesection.saved", { engine: "Orca" }), "ok");
    expect(radio("orca")?.disabled).toBe(false);
  });

  it("drops a superseded earlier save on A -> B -> A round-trip so stale PUT #1 cannot overwrite PUT #2", async () => {
    const a = singleMachine("machine-a", "herdr");
    const b = singleMachine("machine-b", "tern");
    const put1 = deferred<Machine>();
    const put2 = deferred<Machine>();
    mocks.api.mockReturnValueOnce(put1.promise).mockReturnValueOnce(put2.promise);

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // Switch A -> B -> A and save a newer engine on A
    await render(<EngineSection machine={b} heading={<h2>Engine</h2>} />);
    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => radio("tern")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // PUT #2 resolves first
    const saved2 = singleMachine("machine-a", "tern");
    await act(async () => {
      put2.resolve(saved2);
      await Promise.resolve();
    });
    await flush();
    await render(<EngineSection machine={saved2} heading={<h2>Engine</h2>} />);
    expect(mocks.applyMachine).toHaveBeenCalledTimes(1);
    expect(mocks.applyMachine).toHaveBeenLastCalledWith(saved2);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");

    // Delayed PUT #1 resolves later: must NOT overwrite store or UI with stale Orca
    const saved1 = singleMachine("machine-a", "orca");
    await act(async () => {
      put1.resolve(saved1);
      await Promise.resolve();
    });
    await flush();
    expect(mocks.applyMachine).toHaveBeenCalledTimes(1);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");
  });

  it("preserves draft and error across same-machine hub pushes and lets Revert restore the latest pushed engine", async () => {
    const a1 = singleMachine("machine-a", "herdr");
    const a2 = singleMachine("machine-a", "tern");
    const a3 = singleMachine("machine-a", "paseo");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    expect(radio("herdr")?.getAttribute("aria-checked")).toBe("true");

    // Untouched: follows hub push
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");

    // Start saving Orca while a hub push arrives for Paseo: draft stays Orca
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("true");

    // Fail save: error appears and draft stays Orca; Revert restores latest pushed Paseo
    await act(async () => {
      req.reject(new Error("boom"));
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("boom");
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("true");

    await act(async () => buttonByText(t("machines-enginesection.revert"))!.click());
    expect(radio("paseo")?.getAttribute("aria-checked")).toBe("true");
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("clears the edited latch when a hub push converges to the local draft so subsequent pushes follow remote updates", async () => {
    const a1 = singleMachine("machine-a", "herdr");
    const a2 = singleMachine("machine-a", "orca");
    const a3 = singleMachine("machine-a", "paseo");

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeDefined();

    // Hub pushes the same engine as the unsaved draft: Save bar disappears and latch must clear.
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // A later hub push to Paseo must update the selection cleanly instead of staying locked on Orca.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(radio("paseo")?.getAttribute("aria-checked")).toBe("true");
    expect(radio("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("clears a failed save error and edited latch when a hub push converges to the failed draft", async () => {
    const a1 = singleMachine("machine-a", "herdr");
    const a2 = singleMachine("machine-a", "orca");
    const a3 = singleMachine("machine-a", "tern");
    mocks.api.mockRejectedValueOnce(new Error("timeout"));

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("timeout");

    // Hub confirms Orca: stale error clears and latch unlocks.
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Next push to Tern follows remote state cleanly.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(radio("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("reconciles a convergent hub push received while a save is in flight even if the PUT later fails", async () => {
    const a1 = singleMachine("machine-a", "herdr");
    const a2 = singleMachine("machine-a", "orca");
    const a3 = singleMachine("machine-a", "paseo");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => radio("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // Hub WS push arrives with Orca before the HTTP PUT settles (e.g. reportNow before response or concurrent client).
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);

    // HTTP PUT fails after the hub already applied Orca: stale error and latch must clear once saving ends.
    await act(async () => {
      req.reject(new Error("connection lost"));
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Subsequent push to Paseo follows remote state cleanly.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(radio("paseo")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });
});

describe("EngineHostsSection (host sets) cross-machine isolation", () => {
  it("discards unsaved host-set edits when switching machines via both EngineSection and direct render", async () => {
    const a = hostsMachine("machine-a", ["herdr"], "herdr");
    const b = hostsMachine("machine-b", ["tern"], "tern");

    // Via EngineSection
    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeDefined();

    await render(<EngineSection machine={b} heading={<h2>Engine</h2>} />);
    expect(checkbox("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("false");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Direct render without an external React key
    await render(<EngineHostsSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeDefined();

    await render(<EngineHostsSection machine={b} heading={<h2>Engine</h2>} />);
    expect(checkbox("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("false");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("does not clear machine-b's active saving state when machine-a's earlier save settles", async () => {
    const a = hostsMachine("machine-a", ["herdr"], "herdr");
    const b = hostsMachine("machine-b", ["tern"], "tern");
    const reqA = deferred<Machine>();
    const reqB = deferred<Machine>();
    mocks.api.mockReturnValueOnce(reqA.promise).mockReturnValueOnce(reqB.promise);

    await render(<EngineHostsSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // Switch to machine-b and start saving on machine-b too
    await render(<EngineHostsSection machine={b} heading={<h2>Engine</h2>} />);
    expect(checkbox("paseo")?.disabled).toBe(false);
    await act(async () => checkbox("paseo")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());
    expect(checkbox("paseo")?.disabled).toBe(true);

    // machine-a's save resolves while machine-b's save is still in flight
    const savedA = hostsMachine("machine-a", ["herdr", "orca"], "herdr");
    await act(async () => {
      reqA.resolve(savedA);
      await Promise.resolve();
    });
    await flush();

    expect(mocks.applyMachine).toHaveBeenCalledWith(savedA);
    expect(mocks.toast).not.toHaveBeenCalled();
    // machine-b must still be saving (inputs remain disabled) and keep its draft
    expect(checkbox("paseo")?.disabled).toBe(true);
    expect(checkbox("paseo")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");

    // Now machine-b's save resolves
    const savedB = hostsMachine("machine-b", ["tern", "paseo"], "tern");
    await act(async () => {
      reqB.resolve(savedB);
      await Promise.resolve();
    });
    await flush();
    expect(mocks.applyMachine).toHaveBeenCalledWith(savedB);
    expect(checkbox("paseo")?.disabled).toBe(false);
    expect(mocks.toast).toHaveBeenCalledWith(t("machines-enginesection.saved", { engine: "Tern + Paseo" }), "ok");
  });

  it("sends only one PUT on rapid double-click and blocks stale A -> B -> A rejection", async () => {
    const a = hostsMachine("machine-a", ["herdr"], "herdr");
    const b = hostsMachine("machine-b", ["tern"], "tern");
    const reqReject = deferred<Machine>();
    mocks.api.mockReturnValueOnce(reqReject.promise);

    await render(<EngineHostsSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    const saveBtn = buttonByText(t("common.save"))!;
    act(() => {
      saveBtn.click();
      saveBtn.click();
    });
    expect(mocks.api).toHaveBeenCalledTimes(1);

    await render(<EngineHostsSection machine={b} heading={<h2>Engine</h2>} />);
    await render(<EngineHostsSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => {
      reqReject.reject(new Error("stale failure"));
      await Promise.resolve();
    });
    await flush();

    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("true");
  });

  it("completes a normal same-machine host-set save with exact toast and re-enabled controls", async () => {
    const a = hostsMachine("machine-a", ["herdr"], "herdr");
    const savedA = hostsMachine("machine-a", ["herdr", "orca"], "herdr");
    mocks.api.mockResolvedValueOnce(savedA);

    await render(<EngineSection machine={a} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());
    await flush();

    expect(mocks.api).toHaveBeenCalledWith("/machines/machine-a/engine", {
      method: "PUT",
      json: { hosts: ["herdr", "orca"], default: "herdr" },
    });
    expect(mocks.applyMachine).toHaveBeenCalledWith(savedA);
    expect(mocks.toast).toHaveBeenCalledWith(t("machines-enginesection.saved", { engine: "herdr + Orca" }), "ok");
    expect(checkbox("orca")?.disabled).toBe(false);
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("clears the edited latch when a hub push converges to the local host-set draft so subsequent pushes follow remote updates", async () => {
    const a1 = hostsMachine("machine-a", ["herdr"], "herdr");
    const a2 = hostsMachine("machine-a", ["herdr", "orca"], "herdr");
    const a3 = hostsMachine("machine-a", ["tern"], "tern");

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeDefined();

    // Hub pushes the same host set as the unsaved draft: Save bar disappears and latch must clear.
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Subsequent hub push to Tern must update the checkboxes cleanly instead of staying locked on herdr + Orca.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(checkbox("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("false");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("preserves a divergent host-set draft across pushes and clears stale error once the hub converges", async () => {
    const a1 = hostsMachine("machine-a", ["herdr"], "herdr");
    const a2 = hostsMachine("machine-a", ["paseo"], "paseo");
    const a3 = hostsMachine("machine-a", ["herdr", "orca"], "herdr");
    const a4 = hostsMachine("machine-a", ["tern"], "tern");
    mocks.api.mockRejectedValueOnce(new Error("network"));

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());

    // Divergent push keeps the local draft intact.
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("paseo")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeDefined();

    // Save fails -> error shown.
    await act(async () => buttonByText(t("common.save"))!.click());
    await flush();
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("network");

    // Hub converges to the draft -> error and latch clear.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Subsequent push follows remote state cleanly.
    await render(<EngineSection machine={a4} heading={<h2>Engine</h2>} />);
    expect(checkbox("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("false");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });

  it("reconciles a convergent host-set hub push received while saving even if the PUT later fails", async () => {
    const a1 = hostsMachine("machine-a", ["herdr"], "herdr");
    const a2 = hostsMachine("machine-a", ["herdr", "orca"], "herdr");
    const a3 = hostsMachine("machine-a", ["tern"], "tern");
    const req = deferred<Machine>();
    mocks.api.mockReturnValueOnce(req.promise);

    await render(<EngineSection machine={a1} heading={<h2>Engine</h2>} />);
    await act(async () => checkbox("orca")!.click());
    await act(async () => buttonByText(t("common.save"))!.click());

    // Hub WS push arrives with herdr + Orca while PUT is still in flight.
    await render(<EngineSection machine={a2} heading={<h2>Engine</h2>} />);

    // HTTP PUT fails after the hub already converged: stale error and latch must clear once saving ends.
    await act(async () => {
      req.reject(new Error("connection lost"));
      await Promise.resolve();
    });
    await flush();
    expect(host.querySelector('[role="alert"]')).toBeNull();
    expect(buttonByText(t("common.save"))).toBeUndefined();

    // Subsequent push to Tern follows remote state cleanly.
    await render(<EngineSection machine={a3} heading={<h2>Engine</h2>} />);
    expect(checkbox("tern")?.getAttribute("aria-checked")).toBe("true");
    expect(checkbox("herdr")?.getAttribute("aria-checked")).toBe("false");
    expect(checkbox("orca")?.getAttribute("aria-checked")).toBe("false");
    expect(buttonByText(t("common.save"))).toBeUndefined();
  });
});
