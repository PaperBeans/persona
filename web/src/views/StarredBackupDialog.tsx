import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../i18n";
import { api, ApiError, credentialOwner, errorText, getToken, subscribeCredentialOwner } from "../lib/api";
import { onDefaultManager, onManagerChoice, shownManager, stampedHeaders } from "../lib/manager-choice";
import { decodeStarredBackup, starredBackup, starredMarkdown, STARRED_BACKUP_MAX_BYTES, type StarredBackup } from "../lib/starred-backup";
import type { Star } from "../lib/starred-rows";
import { Button, Dialog, toast } from "../ui/atoms";

const IMPORT = "/assistant/starred/import";

export type StarredImportPreview = { revision: number; total: number; to_add: number; already_saved: number; available: number };
export type StarredImportResult = { revision: number; added: number; already_saved: number };
export type StarredBackupSupport = "checking" | "available" | "unsupported" | "error";
export type StarredReviewedBackup = { bundle: StarredBackup; preview: StarredImportPreview };
export type StarredBackupDownload = { format: "json" | "md"; filename: string; mimeType: string; content: string };
export type StarredBackupRequestContext = { manager: string; credentialOwner: string; signal: AbortSignal; isCurrent: () => boolean };

export interface StarredBackupDialogDeps {
  checkImport?: (ctx: StarredBackupRequestContext) => Promise<{ can_import: boolean }>;
  previewImport?: (raw: string, ctx: StarredBackupRequestContext) => Promise<StarredImportPreview>;
  restoreImport?: (bundle: StarredBackup, revision: number, ctx: StarredBackupRequestContext) => Promise<StarredImportResult>;
  exportFile?: (file: StarredBackupDownload) => void | Promise<void>;
  notify?: (message: string, tone: "ok" | "error" | "info") => void;
  manager?: string;
  getManager?: () => string;
  credentialOwner?: string;
  getCredentialOwner?: () => string;
  getToken?: () => string;
  subscribeManager?: (listener: () => void) => () => void;
  subscribeCredentialOwner?: (listener: () => void) => () => void;
}

export interface StarredBackupDialogProps {
  stars: readonly Star[];
  canWrite: boolean;
  speaker: (star: Star) => string;
  onClose: () => void;
  onRestored: () => void;
  deps?: StarredBackupDialogDeps;
}

export interface StarredBackupDialogViewProps {
  open?: boolean;
  support: StarredBackupSupport;
  capabilityError: string;
  working?: "preview" | "restore";
  file?: File;
  reviewed?: StarredReviewedBackup;
  restored?: StarredImportResult;
  error: string;
  onClose: () => void;
  onDownload: (format: "json" | "md") => void;
  onRetryCheck: () => void;
  onPickFile: (candidate?: File) => void;
  onPreview: () => void;
  onRestore: () => void;
}

type BackupApiOptions = { token: string; tokenOwner: string; headers: Record<string, string> | undefined; signal: AbortSignal; isAuthContextCurrent: () => boolean };

type BackupScope = {
  current: () => boolean;
  retire: () => void;
  context: StarredBackupRequestContext;
  apiOptions: () => BackupApiOptions;
};

function backupScope(depsRef: { current?: StarredBackupDialogDeps }): BackupScope {
  const deps = depsRef.current;
  const fallbackGetManager = deps?.getManager;
  const fallbackGetOwner = deps?.getCredentialOwner;
  const hasControlledManager = deps?.manager !== undefined;
  const hasControlledOwner = deps?.credentialOwner !== undefined;
  const readManager = hasControlledManager
    ? () => depsRef.current?.manager ?? deps!.manager!
    : fallbackGetManager
      ? () => (depsRef.current?.getManager ?? fallbackGetManager)()
      : shownManager;
  const readOwner = hasControlledOwner
    ? () => depsRef.current?.credentialOwner ?? deps!.credentialOwner!
    : fallbackGetOwner
      ? () => (depsRef.current?.getCredentialOwner ?? fallbackGetOwner)()
      : credentialOwner;
  const manager = readManager();
  const owner = readOwner();
  const controller = new AbortController();
  const current = () => !controller.signal.aborted && readManager() === manager && readOwner() === owner;
  let cachedOptions: BackupApiOptions | undefined;
  const apiOptions = (): BackupApiOptions =>
    (cachedOptions ??= {
      token: (depsRef.current?.getToken ?? getToken)(),
      tokenOwner: owner,
      headers: stampedHeaders(manager),
      signal: controller.signal,
      isAuthContextCurrent: current,
    });
  return {
    current,
    retire: () => controller.abort(),
    context: { manager, credentialOwner: owner, signal: controller.signal, isCurrent: current },
    apiOptions,
  };
}

export function StarredBackupDialog({ stars, canWrite, speaker, onClose, onRestored, deps }: StarredBackupDialogProps) {
  const origin = useRef<BackupScope | null>(null);
  const depsRef = useRef(deps);
  const onCloseRef = useRef(onClose);
  const onRestoredRef = useRef(onRestored);
  const checkSeq = useRef(0);
  const opSeq = useRef(0);
  const activeOp = useRef(0);
  const pendingScope = useRef<BackupScope | null>(null);
  const [working, setWorking] = useState<"preview" | "restore">();
  const [support, setSupport] = useState<StarredBackupSupport>("checking");
  const [capabilityError, setCapabilityError] = useState("");
  const [file, setFile] = useState<File>();
  const [reviewed, setReviewed] = useState<StarredReviewedBackup>();
  const [restored, setRestored] = useState<StarredImportResult>();
  const [error, setError] = useState("");

  useLayoutEffect(() => {
    depsRef.current = deps;
    onCloseRef.current = onClose;
    onRestoredRef.current = onRestored;
  });

  const notify = (message: string, tone: "ok" | "error" | "info") => {
    if (depsRef.current?.notify) depsRef.current.notify(message, tone);
    else toast(message, tone);
  };

  const resetLocalState = () => {
    setWorking(undefined);
    setCapabilityError("");
    setFile(undefined);
    setReviewed(undefined);
    setRestored(undefined);
    setError("");
  };

  const check = async (scope = origin.current) => {
    if (!scope?.current()) return;
    const seq = ++checkSeq.current;
    setCapabilityError("");
    setSupport("checking");
    try {
      const checkImport = depsRef.current?.checkImport;
      const response = checkImport
        ? await checkImport(scope.context)
        : await api<{ can_import: boolean }>(IMPORT, scope.apiOptions());
      if (scope.current() && checkSeq.current === seq) setSupport(response.can_import ? "available" : "unsupported");
    } catch (failure) {
      if (!scope.current() || checkSeq.current !== seq) return;
      if (failure instanceof ApiError && (failure.status === 404 || failure.status === 405 || failure.status === 403)) {
        setSupport("unsupported");
      } else {
        setCapabilityError(errorText(failure));
        setSupport("error");
      }
    }
  };

  const resolvedManager = deps?.manager !== undefined ? deps.manager : deps?.getManager?.();
  const resolvedOwner = deps?.credentialOwner !== undefined ? deps.credentialOwner : deps?.getCredentialOwner?.();
  const hasCustomManager = deps?.manager !== undefined || deps?.getManager !== undefined;
  const hasCustomOwner = deps?.credentialOwner !== undefined || deps?.getCredentialOwner !== undefined;
  const subscribeManager = deps?.subscribeManager;
  const subscribeOwner = deps?.subscribeCredentialOwner;

  useLayoutEffect(() => {
    const scope = backupScope(depsRef);
    origin.current = scope;
    resetLocalState();
    const changed = () => {
      if (!scope.current()) {
        scope.retire();
        if (pendingScope.current === scope) {
          pendingScope.current = null;
          activeOp.current = 0;
        }
        resetLocalState();
        onCloseRef.current();
      }
    };
    const unsubscribe: Array<() => void> = [];
    if (subscribeManager) unsubscribe.push(subscribeManager(changed));
    else if (!hasCustomManager) unsubscribe.push(onManagerChoice(changed), onDefaultManager(changed));
    if (subscribeOwner) unsubscribe.push(subscribeOwner(changed));
    else if (!hasCustomOwner) unsubscribe.push(subscribeCredentialOwner(changed));
    if (canWrite) void check(scope);
    else {
      checkSeq.current += 1;
      setSupport("unsupported");
    }
    return () => {
      unsubscribe.forEach((off) => off());
      scope.retire();
      if (pendingScope.current === scope) {
        pendingScope.current = null;
        activeOp.current = 0;
      }
      if (origin.current === scope) origin.current = null;
    };
  }, [canWrite, resolvedManager, resolvedOwner, hasCustomManager, hasCustomOwner, subscribeManager, subscribeOwner]);

  const download = (format: "json" | "md") => {
    const scope = origin.current;
    if (!scope?.current()) return;
    const json = format === "json";
    const content = json
      ? JSON.stringify(starredBackup(stars), null, 2) + "\n"
      : starredMarkdown(stars, {
          title: t("starred.title"), conversation: t("starred.conversation"), speaker,
          omitted: (count) => t("starred.truncated", { count }),
        });
    const filename = `persona-starred-${new Date().toISOString().slice(0, 10)}.${format}`;
    const mimeType = json ? "application/json;charset=utf-8" : "text/markdown;charset=utf-8";
    const exportFile = depsRef.current?.exportFile;
    if (exportFile) {
      try {
        void Promise.resolve(exportFile({ format, filename, mimeType, content })).catch((failure: unknown) => {
          if (scope.current()) notify(errorText(failure), "error");
        });
      } catch (failure) {
        if (scope.current()) notify(errorText(failure), "error");
      }
      return;
    }
    const url = URL.createObjectURL(new Blob([content], { type: mimeType }));
    const link = document.createElement("a");
    link.href = url;
    link.download = filename;
    link.rel = "noopener";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  };

  const pick = (candidate?: File) => {
    if (activeOp.current !== 0 || !origin.current?.current()) return;
    setReviewed(undefined);
    setRestored(undefined);
    const tooLarge = candidate !== undefined && candidate.size > STARRED_BACKUP_MAX_BYTES;
    setFile(tooLarge ? undefined : candidate);
    setError(tooLarge ? t("starred.backup_too_large") : "");
  };

  const preview = async () => {
    const scope = origin.current;
    if (activeOp.current !== 0 || support !== "available" || !file || !scope?.current()) return;
    const op = ++opSeq.current;
    activeOp.current = op;
    pendingScope.current = scope;
    setWorking("preview");
    setError("");
    setReviewed(undefined);
    setRestored(undefined);
    try {
      const bytes = await file.arrayBuffer();
      if (!scope.current() || activeOp.current !== op) return;
      const decoded = decodeStarredBackup(bytes);
      if (!decoded) {
        setError(t("starred.backup_invalid"));
        return;
      }
      const previewImport = depsRef.current?.previewImport;
      const result = previewImport
        ? await previewImport(decoded.raw, scope.context)
        : await api<StarredImportPreview>(IMPORT + "/preview", {
            ...scope.apiOptions(), method: "POST",
            headers: { ...scope.apiOptions().headers, "Content-Type": "application/json" },
            body: decoded.raw,
          });
      if (scope.current() && activeOp.current === op) setReviewed({ bundle: decoded.bundle, preview: result });
    } catch (failure) {
      if (scope.current() && activeOp.current === op) setError(errorText(failure));
    } finally {
      if (activeOp.current === op) {
        activeOp.current = 0;
        pendingScope.current = null;
        if (origin.current === scope) setWorking(undefined);
      }
    }
  };

  const canRestore = Boolean(reviewed && reviewed.preview.to_add > 0 && reviewed.preview.to_add <= reviewed.preview.available);
  const restore = async () => {
    const scope = origin.current;
    if (activeOp.current !== 0 || support !== "available" || !canRestore || !reviewed || !scope?.current()) return;
    const op = ++opSeq.current;
    activeOp.current = op;
    pendingScope.current = scope;
    setWorking("restore");
    setError("");
    try {
      const restoreImport = depsRef.current?.restoreImport;
      const result = restoreImport
        ? await restoreImport(reviewed.bundle, reviewed.preview.revision, scope.context)
        : await api<StarredImportResult>(IMPORT, {
            ...scope.apiOptions(), method: "POST",
            json: { ...reviewed.bundle, revision: reviewed.preview.revision },
          });
      if (!scope.current() || activeOp.current !== op) return;
      setReviewed(undefined);
      setFile(undefined);
      setRestored(result);
      onRestoredRef.current();
      notify(t("starred.backup_restored", { count: result.added }), "ok");
    } catch (failure) {
      if (scope.current() && activeOp.current === op) {
        setReviewed(undefined);
        setError(failure instanceof ApiError && failure.status === 409 ? t("starred.backup_conflict") : errorText(failure));
      }
    } finally {
      if (activeOp.current === op) {
        activeOp.current = 0;
        pendingScope.current = null;
        if (origin.current === scope) setWorking(undefined);
      }
    }
  };

  return (
    <StarredBackupDialogView
      support={support}
      capabilityError={capabilityError}
      working={working}
      file={file}
      reviewed={reviewed}
      restored={restored}
      error={error}
      onClose={() => { if (activeOp.current === 0) onCloseRef.current(); }}
      onDownload={download}
      onRetryCheck={() => void check()}
      onPickFile={pick}
      onPreview={() => void preview()}
      onRestore={() => void restore()}
    />
  );
}

export function StarredBackupDialogView({
  open = true, support, capabilityError, working, file, reviewed, restored, error,
  onClose, onDownload, onRetryCheck, onPickFile, onPreview, onRestore,
}: StarredBackupDialogViewProps) {
  const input = useRef<HTMLInputElement>(null);
  const canRestore = Boolean(reviewed && reviewed.preview.to_add > 0 && reviewed.preview.to_add <= reviewed.preview.available);
  return (
    <Dialog open={open} title={t("starred.backup_title")} onClose={onClose}
      footer={<Button tone="quiet" disabled={!!working} onClick={onClose}>{t("starred.backup_done")}</Button>}>
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => onDownload("json")} disabled={!!working}>{t("starred.backup_json")}</Button>
          <Button onClick={() => onDownload("md")} disabled={!!working}>{t("starred.backup_markdown")}</Button>
        </div>
        {support === "checking" && <p className="text-label text-ink-3" role="status">{t("common.loading")}</p>}
        {support === "error" && <div className="flex flex-wrap items-center gap-2">
          <p className="min-w-0 flex-1 break-words text-label text-red" role="alert">{capabilityError}</p>
          <Button onClick={onRetryCheck}>{t("starred.retry")}</Button>
        </div>}
        {support === "available" && <div className="flex flex-col gap-3 border-t border-line pt-3">
          <div>
            <p className="mb-1 text-meta font-medium text-ink-2">{t("starred.backup_file")}</p>
            <div className="flex min-w-0 items-center gap-2">
              <Button disabled={!!working} onClick={() => input.current?.click()}>{t("starred.backup_choose_file")}</Button>
              {file && <span className="min-w-0 truncate text-label text-ink-2" title={file.name}>{file.name}</span>}
            </div>
            <input ref={input} type="file" accept=".json,application/json" disabled={!!working}
              aria-label={t("starred.backup_file")} className="sr-only" tabIndex={-1}
              onChange={(event) => {
                const candidate = event.target.files?.[0];
                event.target.value = "";
                onPickFile(candidate);
              }} />
          </div>
          {error && <p role="alert" className="break-words text-label text-red">{error}</p>}
          {reviewed && <div className="rounded-control bg-inset p-3">
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-label text-ink-2">
              <dt>{t("starred.backup_total")}</dt><dd className="text-right">{reviewed.preview.total}</dd>
              <dt>{t("starred.backup_will_restore")}</dt><dd className="text-right">{reviewed.preview.to_add}</dd>
              <dt>{t("starred.backup_already_saved")}</dt><dd className="text-right">{reviewed.preview.already_saved}</dd>
              <dt>{t("starred.backup_available")}</dt><dd className="text-right">{reviewed.preview.available}</dd>
            </dl>
            {!canRestore && <p role="status" className="mt-2 text-label text-ink-3">
              {t(reviewed.preview.to_add === 0 ? "starred.backup_no_changes" : "starred.backup_capacity")}
            </p>}
          </div>}
          {restored && <p role="status" className="text-label text-ink-2">
            {t("starred.backup_result", { added: restored.added, already: restored.already_saved })}
          </p>}
          <div className="flex flex-wrap gap-2">
            <Button disabled={!file || !!working} busy={working === "preview"} onClick={onPreview}>{t("starred.backup_preview")}</Button>
            {reviewed && <Button tone="primary" disabled={!canRestore || !!working} busy={working === "restore"} onClick={onRestore}>
              {t("starred.backup_restore")}
            </Button>}
          </div>
        </div>}
      </div>
    </Dialog>
  );
}
