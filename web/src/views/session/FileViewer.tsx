// The file viewer as its own panel (a path from a tool step or a message; docs/apple/ios.md "File viewer"). Folders and
// Markdown links open further paths in the same panel, with back.
import { useState } from "react";
import { dirName, fileName } from "../../lib/files";
import { navigate, routeAt } from "../../lib/router";
import { openPanel } from "../../lib/panel";
import { FileView } from "./files/FileView";

export function FileViewerPanel({ mid, sid, path, cwd, line, to, readOnly, onClose }: { mid: string; sid?: string; path: string; cwd?: string; line?: number; to?: number; readOnly?: boolean; onClose: () => void }) {
  const [stack, setStack] = useState<{ path: string; line?: number; to?: number }[]>([{ path, line, to }]);
  const top = stack[stack.length - 1];
  function showInFolder(file: string) {
    const folder = dirName(file) || "/";
    const route = routeAt(location.href);
    if (route?.name === "machine" && route.mid === mid && route.subview === "files") {
      onClose();
      navigate({ name: "machine", mid, subview: "files", path: folder });
    } else {
      openPanel({ kind: "files", mid, sid: sid ?? "", cwd: cwd || folder, path: folder, highlight: fileName(file) });
    }
  }
  return (
    <FileView
      key={`${stack.length}:${top.path}`}
      mid={mid}
      sid={sid}
      path={top.path}
      cwd={cwd}
      line={top.line}
      lineTo={top.to}
      readOnly={readOnly}
      onClose={onClose}
      onBack={stack.length > 1 ? () => setStack((s) => s.slice(0, -1)) : undefined}
      onOpenPath={(p) => setStack((s) => [...s, { path: p }])}
      // Show in Files: the Files panel at its folder, the file marked (#5827). Omitted in read-only mode so it cannot escape to writable file management.
      onShowInFolder={readOnly ? undefined : showInFolder}
    />
  );
}
