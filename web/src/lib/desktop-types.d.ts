export type { DesktopBridge, DesktopIsland, DesktopIslandRow, DesktopIslandState, DesktopNotification, DesktopPresence, DesktopSurfaceStyle, DesktopWindowChrome } from "../../../spec/desktop-bridge.d.ts";

declare global {
  interface Window { readonly personaDesktop?: DesktopBridge }
}

export {};
