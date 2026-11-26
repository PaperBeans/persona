// File cards (<persona-file> tags) and answer document/file cards (FileCards.tsx, lib/documents.ts, ui/Media.tsx):
// kept off the cold first screen and loaded on intent only when a message carries attached files or a finished reply
// names file paths. Outside the idle tier, so IDLE_TIER_BUDGET is unaffected.
import { Suspense, useMemo } from "react";
import { lazyPage } from "../../lib/lazy-page";
import { replyPaths, type AttachedFile } from "../../lib/turns";

const FileCardsBody = lazyPage(() => import("./FileCards").then((m) => m.FileCards));
const ReplyFilesBody = lazyPage(() => import("./FileCards").then((m) => m.ReplyFiles));

export function LazyFileCards({ files, mid, align = "end" }: { files: AttachedFile[]; mid: string; align?: "start" | "end" }) {
  if (!files.length) return null;
  return (
    <Suspense fallback={null}>
      <FileCardsBody files={files} mid={mid} align={align} />
    </Suspense>
  );
}

export function LazyReplyFiles({ text, mid, cwd }: { text: string; mid: string; cwd?: string }) {
  const hasPaths = useMemo(() => replyPaths(text).length > 0, [text]);
  if (!hasPaths) return null;
  return (
    <Suspense fallback={null}>
      <ReplyFilesBody text={text} mid={mid} cwd={cwd} />
    </Suspense>
  );
}
