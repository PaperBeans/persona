// Settings > Hub > Recovery. Read-only whole-install checks live apart from
// the program snapshots on the Hub update page and from Machines.
import { useCallback, useEffect, useState } from "react";
import { ApiError, errorText } from "../../lib/api";
import { ago, bytes } from "../../lib/format";
import {
  inspectRecoveryBundle, recoveryOverview, verifyRecoveryBundle,
  RECOVERY_CREATE_COMMAND, RECOVERY_VERIFY_COMMAND,
  type RecoveryBundle, type RecoveryCapability, type RecoveryCheck, type RecoveryOverview,
} from "../../lib/recovery";
import { t } from "../../i18n";
import { Button, copyText, cx, IconButton, Loading, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Group, Pill, Row, RowFailed, SettingsHint } from "./SettingsParts";

type BundleChecks = { revision: string; inspect?: RecoveryCheck; verify?: RecoveryCheck };

export function RecoveryEntry({ onOpen }: { onOpen?: () => void }) {
  if (!onOpen) return null;
  return (
    <Group title={t("settings-recovery.title")} anchor="hub-recovery" footer={t("settings-recovery.entry_footer")}>
      <Row label={t("settings-recovery.title")} hint={t("settings-recovery.entry_hint")} icon="info" wrapHint onClick={onOpen} />
    </Group>
  );
}

export function RecoverySettings({ onBack }: { onBack: () => void }) {
  const [overview, setOverview] = useState<RecoveryOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [unsupported, setUnsupported] = useState(false);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [busy, setBusy] = useState("");
  const [checks, setChecks] = useState<Record<string, BundleChecks>>({});

  const load = useCallback(async () => {
    setLoading(true); setError(""); setUnsupported(false);
    try {
      const next = await recoveryOverview();
      setOverview(next);
      // A check belongs to the exact listed revision, never merely its name.
      setChecks(current => Object.fromEntries(Object.entries(current).filter(([id, check]) =>
        next.bundles.some(bundle => bundle.id === id && bundle.revision === check.revision))));
    } catch (e) {
      setOverview(null);
      if (e instanceof ApiError && e.status === 404) setUnsupported(true);
      else setError(errorText(e));
    } finally { setLoading(false); }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const run = async (kind: "inspect" | "verify", bundle: RecoveryBundle) => {
    setBusy(`${bundle.id}:${kind}`); setActionError("");
    try {
      let check = kind === "inspect" ? await inspectRecoveryBundle(bundle.id) : await verifyRecoveryBundle(bundle.id);
      if (check.bundle.id !== bundle.id || check.bundle.revision !== bundle.revision) {
        check = { ...check, bundle, state: "failed", reason: "bundle_changed", report: undefined };
      }
      setChecks(current => ({ ...current, [bundle.id]: { ...current[bundle.id], revision: bundle.revision, [kind]: check } }));
    } catch (e) {
      setActionError(e instanceof ApiError && e.status === 404 ? t("settings-recovery.bundle_missing") : t("settings-recovery.action_failed", { error: errorText(e) }));
    } finally { setBusy(""); }
  };

  const limited = overview && (overview.discovery.state !== "supported" || overview.capabilities.create.state === "unsupported" || overview.capabilities.verify.state === "unsupported");
  return (
    <div data-recovery-page>
      <div className="mb-4 flex items-center gap-2 px-1">
        <IconButton icon="chevronLeft" label={t("settings-recovery.back")} onClick={onBack} />
        <h2 className="min-w-0 truncate text-card-title font-semibold text-ink">{t("settings-recovery.title")}</h2>
      </div>

      <Group title={t("settings-recovery.overview")} anchor="recovery-overview" footer={t("settings-recovery.boundary_footer")}>
        <div className="px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="min-w-0 flex-1 text-meta font-medium text-ink">{t("settings-recovery.readiness")}</span>
            <Pill tone={limited || unsupported || error ? "orange" : overview ? "green" : undefined}>
              {t(unsupported || error ? "settings-recovery.unavailable" : limited ? "settings-recovery.limited" : overview ? "settings-recovery.read_only" : "settings-recovery.not_read")}
            </Pill>
          </div>
          <p className="mt-1 text-label leading-relaxed text-ink-3">{t("settings-recovery.overview_hint")}</p>
        </div>
        <Row label={t("settings-recovery.inspect_capability")}>{capabilityState(overview?.capabilities.inspect)}</Row>
        <Row label={t("settings-recovery.verify_capability")} hint={overview?.capabilities.verify.reason ? reasonText(overview.capabilities.verify.reason) : undefined} wrapHint>
          {capabilityState(overview?.capabilities.verify)}
        </Row>
        <Row label={t("settings-recovery.create_capability")} hint={overview?.capabilities.create.state === "unsupported" ? reasonText(overview.capabilities.create.reason) : undefined} wrapHint>
          {capabilityState(overview?.capabilities.create)}
        </Row>
      </Group>

      <Group title={t("settings-recovery.protect_title")} footer={t("settings-recovery.protect_footer")}>
        <div className="grid gap-2 px-4 py-3 sm:grid-cols-2">
          {(["databases", "kb", "runtime", "keys", "tls"] as const).map(item => (
            <div key={item} className="flex items-start gap-2 text-meta leading-relaxed text-ink-2">
              <Icon name="check" size={13} className="mt-0.5 shrink-0 text-green" />
              <span>{t(`settings-recovery.protect_${item}`)}</span>
            </div>
          ))}
        </div>
      </Group>

      <Group title={t("settings-recovery.bundles")} anchor="recovery-bundles" right={
        <Button icon="retry" busy={loading && !!overview} disabled={loading || !!busy} onClick={() => void load()}>{t("settings-recovery.refresh")}</Button>
      }>
        {loading && !overview && !unsupported && !error ? <Loading /> : unsupported ? (
          <SettingsHint caution>{t("settings-recovery.old_hub")}</SettingsHint>
        ) : error && !overview ? (
          <RowFailed text={t("settings-recovery.read_failed", { error })} retry={t("settings-recovery.refresh")} onRetry={() => void load()} />
        ) : overview?.discovery.state !== "supported" ? (
          <SettingsHint caution>{reasonText(overview?.discovery.reason)}</SettingsHint>
        ) : !overview?.bundles.length ? (
          <SettingsHint>{t("settings-recovery.empty")}</SettingsHint>
        ) : overview.bundles.map(bundle => (
          <BundleRow key={bundle.id} bundle={bundle} check={checks[bundle.id]} verify={overview.capabilities.verify}
            busy={busy} disabled={loading || (!!busy && busy !== `${bundle.id}:inspect` && busy !== `${bundle.id}:verify`)}
            onRun={run} />
        ))}
        {actionError && <SettingsHint alert>{actionError}</SettingsHint>}
      </Group>

      <Group title={t("settings-recovery.create_title")} footer={t("settings-recovery.create_footer")}>
        <div className="space-y-2 px-4 py-3 text-meta leading-relaxed text-ink-2">
          <p>{t("settings-recovery.create_stop")}</p>
          <p>{t("settings-recovery.create_run")}</p>
          <Command command={RECOVERY_CREATE_COMMAND} />
          <p>{t("settings-recovery.create_restart")}</p>
        </div>
        {overview?.capabilities.create.state === "unsupported" && <SettingsHint caution>{reasonText(overview.capabilities.create.reason)}</SettingsHint>}
        <SettingsHint>{t("settings-recovery.verify_command_hint")}</SettingsHint>
        <div className="px-4 pb-3"><Command command={RECOVERY_VERIFY_COMMAND} /></div>
      </Group>

      <Group title={t("settings-recovery.history_title")}>
        <SettingsHint>{t("settings-recovery.history_unavailable")}</SettingsHint>
        <SettingsHint>{t("settings-recovery.drill_boundary")}</SettingsHint>
      </Group>
    </div>
  );
}

function BundleRow({ bundle, check, verify, busy, disabled, onRun }: {
  bundle: RecoveryBundle; check?: BundleChecks; verify: RecoveryCapability; busy: string; disabled: boolean;
  onRun: (kind: "inspect" | "verify", bundle: RecoveryBundle) => Promise<void>;
}) {
  const inspectBusy = busy === `${bundle.id}:inspect`;
  const verifyBusy = busy === `${bundle.id}:verify`;
  return (
    <div className="px-4 py-3" data-recovery-bundle={bundle.id}>
      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        <span className="min-w-0 flex-1 truncate text-meta font-medium text-ink">{bundle.name}</span>
        <span className="shrink-0 text-submeta text-ink-3">{bytes(bundle.archive_bytes)} · {ago(bundle.modified_at)}</span>
      </div>
      {!check?.inspect && !check?.verify && <p className="mt-1 text-submeta text-ink-3">{t("settings-recovery.not_checked")}</p>}
      {check?.inspect && <CheckResult check={check.inspect} />}
      {check?.verify && <CheckResult check={check.verify} />}
      <div className="mt-2.5 flex flex-col gap-2 sm:flex-row sm:justify-end">
        <Button busy={inspectBusy} disabled={disabled || inspectBusy || verifyBusy} onClick={() => void onRun("inspect", bundle)}>
          {inspectBusy ? t("settings-recovery.checking_integrity") : t("settings-recovery.check_integrity")}
        </Button>
        <Button busy={verifyBusy} disabled={disabled || inspectBusy || verifyBusy || verify.state === "unsupported"} onClick={() => void onRun("verify", bundle)}>
          {verifyBusy ? t("settings-recovery.verifying_databases") : t("settings-recovery.verify_databases")}
        </Button>
      </div>
      {verify.state === "unsupported" && <p className="mt-2 text-submeta leading-relaxed text-orange">{reasonText(verify.reason)}</p>}
    </div>
  );
}

function CheckResult({ check }: { check: RecoveryCheck }) {
  const passed = check.state === "passed";
  const inspection = check.kind === "offline_integrity";
  const title = t(passed
    ? inspection ? "settings-recovery.integrity_passed" : "settings-recovery.verification_passed"
    : check.state === "unsupported" ? "settings-recovery.verification_unsupported"
      : inspection ? "settings-recovery.integrity_failed" : "settings-recovery.verification_failed_title");
  return (
    <div className="mt-2 rounded-control bg-inset px-2.5 py-2">
      <div className="flex items-start gap-2">
        <Icon name={passed ? "check" : "info"} size={13} className={cx("mt-0.5 shrink-0", passed ? "text-green" : "text-orange")} />
        <div className="min-w-0 flex-1">
          <p className={cx("text-label font-medium", passed ? "text-green" : "text-orange")}>{title}</p>
          {!passed && <p className="mt-0.5 text-submeta leading-relaxed text-ink-3">{reasonText(check.reason)}</p>}
          {passed && <p className="mt-0.5 text-submeta text-ink-3">{t(inspection ? "settings-recovery.database_not_verified" : "settings-recovery.not_drill")}</p>}
        </div>
      </div>
      {check.report && (
        <details className="mt-1.5 text-submeta text-ink-3">
          <summary className="cursor-pointer text-accent-ink">{t("settings-recovery.inspection_details")}</summary>
          <dl className="mt-1.5 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            <dt>{t("settings-recovery.created")}</dt><dd>{ago(check.report.created_at)}</dd>
            <dt>{t("settings-recovery.archive")}</dt><dd>{bytes(check.report.archive_bytes)}</dd>
            <dt>{t("settings-recovery.knowledge")}</dt><dd>{knowledgeState(check.report.knowledge_base.state)}</dd>
          </dl>
        </details>
      )}
    </div>
  );
}

function Command({ command }: { command: string }) {
  return (
    <div className="flex items-center gap-2 rounded-control bg-inset px-2.5 py-1.5 shadow-hairline">
      <code className="min-w-0 flex-1 break-all font-mono text-submeta text-ink select-all">{command}</code>
      <IconButton icon="copy" label={t("settings-recovery.copy_command")} size={24} iconSize={12}
        onClick={() => void copyText(command).then(() => toast(t("settings-recovery.copied"), "ok"))} />
    </div>
  );
}

function capabilityState(capability?: RecoveryCapability) {
  if (!capability) return t("settings-recovery.not_read");
  return t(capability.state === "supported" ? "settings-recovery.available" : capability.state === "guided" ? "settings-recovery.terminal_only" : "settings-recovery.unavailable");
}

function knowledgeState(state: string) {
  switch (state) {
    case "included": return t("settings-recovery.knowledge_included");
    case "empty": return t("settings-recovery.knowledge_empty");
    case "absent": return t("settings-recovery.knowledge_absent");
    default: return t("settings-recovery.unknown");
  }
}

function reasonText(reason?: string): string {
  switch (reason) {
    case "compose": return t("settings-recovery.reason_compose");
    case "postgresql_tools": return t("settings-recovery.reason_postgresql_tools");
    case "configuration": return t("settings-recovery.reason_configuration");
    case "external_manager": return t("settings-recovery.reason_external_manager");
    case "unsafe_directory": return t("settings-recovery.reason_unsafe_directory");
    case "unreadable_directory": return t("settings-recovery.reason_unreadable_directory");
    case "limited": return t("settings-recovery.reason_limited");
    case "invalid_bundle": return t("settings-recovery.reason_invalid_bundle");
    case "bundle_changed": return t("settings-recovery.reason_bundle_changed");
    case "verification_failed": return t("settings-recovery.reason_verification_failed");
    case "no_authoritative_source": return t("settings-recovery.history_unavailable");
    default: return t("settings-recovery.reason_unknown");
  }
}
