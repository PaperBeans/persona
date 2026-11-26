import { createContext, useContext, type ReactNode } from "react";
import { moduleOn, useModuleStates } from "../../core/module-host";
import { isLarkSource, type InboxItem } from "../../lib/inbox";

/** Inbox installs the module boundary. The default keeps the module-owned Messages page and isolated views writable;
 *  that page exists only while its module is on. */
const LarkWrites = createContext(true);
export const useLarkWrites = () => useContext(LarkWrites);

/** Existing Lark history stays in Core when the module is off; only its Lark write controls become read-only. */
export function LarkWriteGate({ children }: { children: ReactNode }) {
  useModuleStates();
  return <LarkWrites.Provider value={moduleOn("lark")}>{children}</LarkWrites.Provider>;
}

/** Whether writes targeting `item` belong to the optional Lark module (Feishu gateway or lark-cli). */
export function isLarkWriteTarget(item: InboxItem): boolean {
  return isLarkSource(item) || Boolean(item.lark);
}

export function useLarkItemReadOnly(item?: InboxItem): boolean {
  const writes = useLarkWrites();
  return !writes && Boolean(item && isLarkWriteTarget(item));
}
