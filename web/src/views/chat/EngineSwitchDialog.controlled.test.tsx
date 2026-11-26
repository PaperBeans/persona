// @vitest-environment jsdom
// Controlled switch dialogs own container, checkbox and confirm/cancel behavior without default atoms.Dialog; default fallbacks stay pinned too.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { EngineSwitchDialogContainerProps, EngineSwitchDialogProps } from "./EngineSwitchDialog";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const mocks = vi.hoisted(() => ({
  Dialog: vi.fn(),
}));

vi.mock("../../ui/atoms", async (importOriginal) => {
  const original = await importOriginal<typeof import("../../ui/atoms")>();
  mocks.Dialog.mockImplementation((props: Parameters<typeof original.Dialog>[0]) => original.Dialog(props));
  return {
    ...original,
    Dialog: mocks.Dialog,
  };
});

const { EngineSwitchDialog } = await import("./EngineSwitchDialog");
const { t } = await import("../../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

const mount = async (props: EngineSwitchDialogProps) => {
  await act(async () => root.render(<EngineSwitchDialog {...props} />));
};
const findDialog = (container: ParentNode = host) => container.querySelector<HTMLElement>("[data-engine-switch-dialog]");
const checkbox = (container: ParentNode = host) => findDialog(container)?.querySelector<HTMLInputElement>('input[type="checkbox"]') ?? null;
const buttonNamed = (label: string, container: ParentNode = host) =>
  [...(findDialog(container)?.querySelectorAll<HTMLButtonElement>("button") ?? [])].find((b) => b.textContent?.trim() === label);

beforeEach(() => {
  mocks.Dialog.mockClear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("EngineSwitchDialog with controlled props", () => {
  it("renders through renderDialog without calling default Dialog and routes controlled skip/confirm/cancel", async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    const onAnswer = vi.fn();
    const onSkipChange = vi.fn();
    let captured: EngineSwitchDialogContainerProps | null = null;
    const renderDialog = vi.fn((props: EngineSwitchDialogContainerProps) => {
      captured = props;
      if (!props.open) return null;
      return (
        <div
          data-custom-dialog
          role="dialog"
          aria-labelledby={props.labelledBy}
          aria-describedby={props.describedBy}
          data-width={props.width}
        >
          {props.children}
        </div>
      );
    });

    await mount({
      open: true,
      skip: true,
      onSkipChange,
      onConfirm,
      onCancel,
      onAnswer,
      renderDialog,
    });

    expect(mocks.Dialog).not.toHaveBeenCalled();
    expect(renderDialog).toHaveBeenCalledTimes(1);
    const modal = host.querySelector<HTMLElement>("[data-custom-dialog]")!;
    expect(modal.getAttribute("data-width")).toBe("400");
    expect(document.getElementById(modal.getAttribute("aria-labelledby")!)?.textContent).toBe(t("chat-modelpanel.engine_dialog_title"));
    expect(document.getElementById(modal.getAttribute("aria-describedby")!)?.textContent).toBe(t("chat-modelpanel.engine_dialog_body"));
    expect([...findDialog()!.querySelectorAll("button")].map((b) => b.textContent?.trim())).toEqual([
      t("chat-modelpanel.engine_dialog_switch"),
      t("chat-modelpanel.engine_dialog_keep"),
    ]);
    expect(checkbox()?.checked).toBe(true);

    // Controlled checkbox reports intent via onSkipChange without mutating its own state.
    act(() => checkbox()!.click());
    expect(onSkipChange).toHaveBeenCalledWith(false);
    expect(checkbox()?.checked).toBe(true);

    // Reopening preserves caller-controlled skip.
    await mount({ open: false, skip: true, onSkipChange, onConfirm, onCancel, onAnswer, renderDialog });
    expect(host.querySelector("[data-custom-dialog]")).toBeNull();
    await mount({ open: true, skip: true, onSkipChange, onConfirm, onCancel, onAnswer, renderDialog });
    expect(checkbox()?.checked).toBe(true);

    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_switch"))!.click());
    expect(onConfirm).toHaveBeenCalledWith(true);
    expect(onCancel).not.toHaveBeenCalled();
    expect(onAnswer).not.toHaveBeenCalled();

    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_keep"))!.click());
    expect(onCancel).toHaveBeenCalledTimes(1);

    act(() => captured!.onClose());
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onAnswer).not.toHaveBeenCalled();
    expect(mocks.Dialog).not.toHaveBeenCalled();
  });

  it("supports parent-driven skip updates, hidden skip option, and busy locking", async () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    let containerClose: (() => void) | null = null;

    function ControlledHost({ showSkip = true, busy = false }: { showSkip?: boolean; busy?: boolean }) {
      const [skip, setSkip] = useState(false);
      return (
        <EngineSwitchDialog
          open
          skip={skip}
          onSkipChange={setSkip}
          onConfirm={onConfirm}
          onCancel={onCancel}
          showSkip={showSkip}
          busy={busy}
          renderDialog={(props) => {
            containerClose = props.onClose;
            return <div data-custom-dialog>{props.children}</div>;
          }}
        />
      );
    }

    await act(async () => root.render(<ControlledHost />));
    expect(checkbox()?.checked).toBe(false);
    act(() => checkbox()!.click());
    expect(checkbox()?.checked).toBe(true);
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_switch"))!.click());
    expect(onConfirm).toHaveBeenLastCalledWith(true);

    await act(async () => root.render(<ControlledHost showSkip={false} />));
    expect(checkbox()).toBeNull();
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_switch"))!.click());
    expect(onConfirm).toHaveBeenLastCalledWith(false);

    onConfirm.mockClear();
    onCancel.mockClear();
    await act(async () => root.render(<ControlledHost showSkip busy />));
    expect(checkbox()?.disabled).toBe(true);
    expect(buttonNamed(t("chat-modelpanel.engine_dialog_switch"))?.disabled).toBe(true);
    expect(buttonNamed(t("chat-modelpanel.engine_dialog_keep"))?.disabled).toBe(true);
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_switch"))!.click());
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_keep"))!.click());
    act(() => containerClose?.());
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
    expect(mocks.Dialog).not.toHaveBeenCalled();
  });
});

describe("EngineSwitchDialog default compatibility", () => {
  it("uses default Dialog, resets uncontrolled skip on reopen, and answers via onAnswer", async () => {
    const onAnswer = vi.fn();
    await mount({ open: true, onAnswer });

    expect(mocks.Dialog).toHaveBeenCalled();
    const d = findDialog(document.body)!;
    const modal = d.closest('[role="dialog"]')!;
    expect(modal.getAttribute("aria-modal")).toBe("true");
    expect(document.getElementById(modal.getAttribute("aria-labelledby")!)?.textContent).toBe(t("chat-modelpanel.engine_dialog_title"));
    expect(document.getElementById(modal.getAttribute("aria-describedby")!)?.textContent).toBe(t("chat-modelpanel.engine_dialog_body"));
    expect(checkbox(document.body)?.checked).toBe(false);

    act(() => checkbox(document.body)!.click());
    expect(checkbox(document.body)?.checked).toBe(true);
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_switch"), document.body)!.click());
    expect(onAnswer).toHaveBeenLastCalledWith(true, true);

    // Reopening starts unticked again.
    await mount({ open: false, onAnswer });
    await mount({ open: true, onAnswer });
    expect(checkbox(document.body)?.checked).toBe(false);

    // Ticked + Keep / Escape / backdrop all call onAnswer(false, false).
    act(() => checkbox(document.body)!.click());
    act(() => buttonNamed(t("chat-modelpanel.engine_dialog_keep"), document.body)!.click());
    expect(onAnswer).toHaveBeenLastCalledWith(false, false);

    act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(onAnswer).toHaveBeenLastCalledWith(false, false);

    const backdrop = findDialog(document.body)!.closest('[role="dialog"]')!.parentElement!;
    act(() => {
      backdrop.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
    });
    expect(onAnswer).toHaveBeenLastCalledWith(false, false);
    expect(onAnswer).toHaveBeenCalledTimes(4);
  });
});
