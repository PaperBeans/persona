import type { ContextRef } from "../../lib/links";
import type { IconName } from "../../ui/icons";

export function referenceIcon(ref: ContextRef): IconName {
  switch (ref.kind) {
    case "skill": return "sparkles";
    case "file": return ref.attrs?.path?.endsWith("/") ? "folder" : "file";
    case "workspace": return "folder";
    case "repository": return "git";
    default: return "chat";
  }
}
