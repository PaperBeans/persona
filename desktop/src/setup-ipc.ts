import { join } from "node:path";
import { ipcMain, net, protocol, shell, app } from "electron";
import type { BrowserWindow } from "electron";
import { parseConnectInput } from "./connect-input.js";
import type { HubPin } from "./tls-pin.js";
import { externalBrowserURL } from "./urls.js";
import type { HubAddress } from "./urls.js";
import { linkStatus, runPersonaLink } from "./link.js";
import type { LinkCommand } from "./link-contract.cjs";
import { desktopLocale, loadStrings } from "./strings.js";
import { SETUP_SCHEME, SETUP_URL, setupAsset } from "./setup-page.js";

/** Serves the setup page from its own scheme (setup-page.ts); main.ts registers the scheme before the app is ready
 *  and calls this once it is. Only the default session has this handler, never the hubs' partitions. */
export function installSetupProtocol(): void {
  protocol.handle(SETUP_SCHEME, async request => {
    const asset = setupAsset(request.url, request.method);
    if (!asset) return new Response("Not found", { status: 404 });
    const response = await net.fetch(asset.file);
    const headers = new Headers(response.headers);
    headers.set("content-type", asset.contentType);
    return new Response(request.method === "HEAD" ? null : response.body, {
      status: response.status,
      statusText: response.statusText,
      headers,
    });
  });
}

export interface ConnectResponse {
  readonly ok: boolean;
  readonly error?: string;
  readonly errorCode?: string;
  readonly technical?: string;
  readonly replaced?: boolean;
  readonly linkURL?: string;
  readonly suggestedHub?: string;
}

/** Setup is a local shell window. Its sender is validated again on every privileged action. */
export function installSetupIPC(window: BrowserWindow, deps: {
  readonly hub: () => HubAddress | undefined;
  readonly hasMainWindow: () => boolean;
  readonly connectionFailed: () => boolean;
  readonly connect: (raw: string) => Promise<ConnectResponse>;
  readonly onVerifiedLinkPin?: (pin: HubPin) => void;
}): () => void {
  const fromSetup = (sender: Electron.WebContents, frame: Electron.WebFrameMain | null): boolean => sender === window.webContents && !window.isDestroyed() && frame === sender.mainFrame && sender.getURL() === SETUP_URL;
  // Setup owns separate channels: closing it must not remove the hub window's login-item handlers.
  const names = ["desktop:hub-get", "desktop:hub-set", "desktop:open-external", "desktop:link-status", "desktop:link-command", "desktop:setup-login-get", "desktop:setup-login-set"];
  for (const name of names) ipcMain.removeHandler(name);
  ipcMain.handle("desktop:hub-get", async event => {
    if (!fromSetup(event.sender, event.senderFrame)) return { address: "" };
    const locale = desktopLocale(app.getLocale());
    const strings = await loadStrings(join(import.meta.dirname, "locales"), locale);
    const hub = deps.hub();
    const failed = deps.connectionFailed();
    const mode = deps.hasMainWindow() ? "settings" : (failed && hub ? "recovery" : "onboarding");
    return { address: hub?.url.href ?? "", connectionFailed: failed, mode, platform: process.platform, locale, strings };
  });
  ipcMain.handle("desktop:hub-set", async (event, value: unknown): Promise<ConnectResponse> => {
    if (!fromSetup(event.sender, event.senderFrame) || typeof value !== "string") {
      return { ok: false, errorCode: "invalid_url", error: "Enter the hub's web address or pairing link." };
    }
    try {
      parseConnectInput(value);
    } catch (error) {
      return {
        ok: false,
        errorCode: "invalid_url",
        error: error instanceof Error ? error.message : "Enter a valid HTTP/HTTPS hub address or pairing link.",
      };
    }
    return deps.connect(value);
  });
  ipcMain.handle("desktop:open-external", async (event, value: unknown) => {
    if (!fromSetup(event.sender, event.senderFrame) || typeof value !== "string") return;
    const url = externalBrowserURL(value);
    if (url) await shell.openExternal(url);
  });
  ipcMain.handle("desktop:link-status", async event => fromSetup(event.sender, event.senderFrame) ? linkStatus() : { available: false });
  ipcMain.handle("desktop:setup-login-get", async event => {
    if (!fromSetup(event.sender, event.senderFrame)) return { enabled: false };
    const settings = app.getLoginItemSettings();
    return { enabled: settings.openAtLogin, requiresApproval: process.platform === "darwin" && settings.status === "requires-approval" };
  });
  ipcMain.handle("desktop:setup-login-set", async (event, enabled: unknown) => {
    if (!fromSetup(event.sender, event.senderFrame) || typeof enabled !== "boolean") return { enabled: false };
    app.setLoginItemSettings({ openAtLogin: enabled });
    const settings = app.getLoginItemSettings();
    return { enabled: settings.openAtLogin, requiresApproval: process.platform === "darwin" && settings.status === "requires-approval" };
  });
  ipcMain.handle("desktop:link-command", async (event, command: LinkCommand) => {
    if (!fromSetup(event.sender, event.senderFrame) || !command || typeof command !== "object") {
      return { ok: false, stdout: "", stderr: "Unsupported command." };
    }
    const result = await runPersonaLink(command);
    if (result.ok && command.action === "pair-active" && typeof command.value === "string") {
      try {
        const parsed = parseConnectInput(command.value);
        if (parsed.kind === "link-pair") {
          if (parsed.pin) deps.onVerifiedLinkPin?.(parsed.pin);
          if (parsed.suggestedHub) return { ...result, suggestedHub: parsed.suggestedHub.url.href };
        }
      } catch {
        // Pairing succeeded in CLI; do not fail the response if suggestedHub parsing fails.
      }
    }
    return result;
  });
  return () => { for (const name of names) ipcMain.removeHandler(name); };
}
