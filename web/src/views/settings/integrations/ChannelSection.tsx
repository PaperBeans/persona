// Channel state and unified sections (Account & Connection binding + Chat & Delivery routing) for IntegrationDetail (#14847).
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { t } from "../../../i18n";
import { errorText } from "../../../lib/api";
import {
  canBind, capabilityText, channelDetail, channelStateText, channelTone, forgetChannels, isBound,
  targetText, testChannel, unbindChannel, updateChannel,
  type Channel, type ChannelDetail, type ChannelTargetOverride, type ChannelTone, type ChannelUpdateBody,
} from "../../../lib/channels";
import { ago } from "../../../lib/format";
import { otherManager, pinManager } from "../../../lib/manager-choice";
import { Button, cx, Dialog, Loading } from "../../../ui/atoms";
import { ChannelMark } from "../../../ui/ChannelMark";
import { Icon } from "../../../ui/icons";
import { toast } from "../../../ui/atoms";
import { BindSteps, TargetPicker } from "../Channels";
import { ConfigurationForm } from "../configuration/ConfigurationForm";
import { Group, Row, SettingsHint, Switch } from "../SettingsParts";
import { channelRoutingSource } from "./model";

const DOT: Record<ChannelTone, string> = { green: "bg-green", red: "bg-red", idle: "bg-line-strong" };

function targetOverrideText(override: ChannelTargetOverride): string {
  switch (override.mode) {
    case "topics":
      return t("settings-channels.target_override_topics");
    case "threads":
      return t("settings-channels.target_override_threads");
    default:
      return t("settings-channels.target_override_generic");
  }
}

export interface ChannelController {
  detail: ChannelDetail | null;
  error: string;
  loading: boolean;
  busy: string;
  reloadKey: number;
  testFeedback: { tone: "ok" | "error"; text: string } | null;
  load: () => Promise<void>;
  change: (what: string, body: ChannelUpdateBody) => Promise<void>;
  unbind: () => Promise<boolean>;
  runTest: () => Promise<void>;
  adopt: (next: ChannelDetail) => void;
}

/** Loads and mutates one channel with request sequencing and exclusive writes. */
export function useChannelController(channelId: string | null, manager?: string): ChannelController {
  useLayoutEffect(() => {
    if (!channelId) return undefined;
    return pinManager(`/channels/${encodeURIComponent(channelId)}`, otherManager(manager));
  }, [channelId, manager]);

  const [detail, setDetail] = useState<ChannelDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [testFeedback, setTestFeedback] = useState<{ tone: "ok" | "error"; text: string } | null>(null);

  const seq = useRef(0);
  const saving = useRef(false);

  const load = useCallback(async () => {
    if (!channelId) {
      setDetail(null);
      setError("");
      setLoading(false);
      return;
    }
    const n = ++seq.current;
    setLoading(true);
    try {
      const next = await channelDetail(channelId);
      if (n !== seq.current) return;
      setDetail(next);
      setError("");
      setReloadKey((k) => k + 1);
    } catch (e) {
      if (n === seq.current) setError(errorText(e));
    } finally {
      if (n === seq.current) setLoading(false);
    }
  }, [channelId]);

  useEffect(() => {
    setDetail(null);
    setError("");
    setTestFeedback(null);
    void load();
  }, [load]);

  const adopt = useCallback((next: ChannelDetail) => {
    seq.current++;
    forgetChannels();
    setDetail(next);
    setError("");
  }, []);

  const exclusive = useCallback(async (what: string, save: () => Promise<ChannelDetail | void>): Promise<boolean> => {
    if (!channelId || saving.current) return false;
    saving.current = true;
    const n = ++seq.current;
    setBusy(what);
    let stale = false;
    let ok = false;
    try {
      const next = await save();
      if (next) {
        forgetChannels();
        if (n === seq.current) setDetail(next);
        else stale = true;
      }
      ok = true;
    } catch (e) {
      toast(errorText(e), "error");
    } finally {
      saving.current = false;
      setBusy("");
    }
    if (stale) void load();
    return ok;
  }, [channelId, load]);

  const change = useCallback(async (what: string, body: ChannelUpdateBody) => {
    if (!channelId) return;
    await exclusive(what, () => updateChannel(channelId, body));
  }, [channelId, exclusive]);

  const unbind = useCallback(async () => {
    if (!channelId) return false;
    return exclusive("unbind", () => unbindChannel(channelId));
  }, [channelId, exclusive]);

  const runTest = useCallback(async () => {
    if (!channelId || !detail) return;
    const replyOnly = !!detail.capabilities.reply_only;
    await exclusive("test", async () => {
      setTestFeedback(null);
      try {
        const res = await testChannel(channelId);
        const text = replyOnly || res?.reply_only
          ? (res?.note || t("settings-channels.connection_ok", { name: detail.name }))
          : (res?.note || t("settings-channels.test_sent", { name: detail.name }));
        setTestFeedback({ tone: "ok", text });
        toast(text, "ok");
      } catch (e) {
        setTestFeedback({ tone: "error", text: errorText(e) });
        throw e;
      }
    });
  }, [channelId, detail, exclusive]);

  return { detail, error, loading, busy, reloadKey, testFeedback, load, change, unbind, runTest, adopt };
}

export function ChannelStateInline({ c }: { c: Channel }) {
  return (
    <span className={cx("flex items-center gap-1.5 whitespace-nowrap text-label", c.state === "error" ? "text-red" : "text-ink-2")}>
      <span aria-hidden className={cx("size-1.5 shrink-0 rounded-full", DOT[channelTone(c)])} />
      <span>{channelStateText(c)}</span>
    </span>
  );
}

/** Binding card rendered inside Account & Connection when a channel binds directly. */
export function ChannelBindingBlock({ ctl }: { ctl: ChannelController }) {
  const [unbinding, setUnbinding] = useState(false);
  const d = ctl.detail;
  if (!d) return null;
  const editable = !d.read_only && d.source !== "market";
  if (!editable) return null;

  return (
    <>
      {canBind(d) && <BindSteps c={d} onDone={ctl.load} />}
      {isBound(d) && (
        <Group title={t("settings-channels.binding")} footer={t("settings-channels.binding_footer")}>
          <Row label={t("settings-channels.bound_account")}>
            <span className="flex items-center gap-2">
              <ChannelMark c={d} size={18} />
              <span className="truncate">{d.account?.name ?? t("settings-channels.bound")}</span>
            </span>
          </Row>
          <div className="px-4 py-2.5">
            <Button tone="quiet" icon="logout" onClick={() => setUnbinding(true)} disabled={!!ctl.busy} className="-ml-2 text-red">
              {t("settings-channels.unbind")}
            </Button>
          </div>
        </Group>
      )}
      <Dialog
        open={unbinding}
        onClose={() => setUnbinding(false)}
        title={t("settings-channels.unbind_title", { name: d.name })}
        footer={
          <>
            <Button tone="quiet" onClick={() => setUnbinding(false)}>{t("common.cancel")}</Button>
            <Button
              tone="danger"
              busy={ctl.busy === "unbind"}
              disabled={!!ctl.busy && ctl.busy !== "unbind"}
              onClick={() =>
                void ctl.unbind().then((ok) => {
                  if (!ok) return;
                  setUnbinding(false);
                  toast(t("settings-channels.unbound"), "ok");
                })
              }
            >
              {t("settings-channels.unbind")}
            </Button>
          </>
        }
      >
        <p className="px-5 py-4 text-meta leading-relaxed text-ink-2">{t("settings-channels.unbind_body", { name: d.name })}</p>
      </Dialog>
    </>
  );
}

/** Chat & Delivery controls: enable, target, thread_mode & thread_idle_hours, features, capabilities, test message. */
export function ChannelDeliveryBlock({ ctl, onSettings }: { ctl: ChannelController; onSettings?: (url: string) => void }) {
  const [picking, setPicking] = useState(false);
  const detailRef = useRef<ChannelDetail | null>(ctl.detail);
  detailRef.current = ctl.detail;

  const d = ctl.detail;
  const routingSource = useMemo(() => {
    if (!d) return null;
    return channelRoutingSource(
      d.id,
      ctl.reloadKey,
      () => detailRef.current ?? d,
      ctl.adopt,
    );
    // Keep the ConfigurationForm controller mounted across its own saves; only remount on channel change or explicit reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [d?.id, ctl.reloadKey, ctl.adopt]);

  if (!d) {
    if (ctl.error) {
      return (
        <Group>
          <SettingsHint alert>{t("settings-integrations.channel_unavailable", { error: ctl.error })}</SettingsHint>
        </Group>
      );
    }
    if (ctl.loading) {
      return (
        <Group>
          <div className="px-4 py-3"><Loading /></div>
        </Group>
      );
    }
    return null;
  }

  const editable = !d.read_only && d.source !== "market";
  const replyOnly = !!d.capabilities.reply_only;

  return (
    <>
      {d.state === "error" && d.error && (
        <div role="alert" className="mb-4 flex items-start gap-2 rounded-card bg-red-tint px-3.5 py-2.5 text-meta text-red">
          <Icon name="info" size={14} className="mt-px shrink-0" />
          <span className="min-w-0 break-words">{d.error}</span>
        </div>
      )}

      {editable && (
        <Group footer={d.source === "plugin" ? t("settings-channels.disable_footer") : undefined}>
          <Row label={t("settings-channels.enable", { name: d.name })}>
            <Switch
              on={d.enabled}
              onChange={(on) => void ctl.change("enabled", { enabled: on })}
              disabled={!!ctl.busy}
              label={t("settings-channels.enable", { name: d.name })}
            />
          </Row>
        </Group>
      )}

      {editable && d.target && (
        <Group title={t("settings-channels.target_title")} footer={t("settings-channels.target_footer")}>
          <Row label={t("settings-channels.target_row")} onClick={() => !ctl.busy && setPicking(true)}>
            <span className="max-w-[220px] truncate">{targetText(d.target)}</span>
          </Row>
          {d.target_override && (
            <SettingsHint>
              {targetOverrideText(d.target_override)}
              {d.settings_url && onSettings && (
                <>
                  {" "}
                  <button
                    type="button"
                    onClick={() => onSettings(d.settings_url!)}
                    className="font-medium whitespace-nowrap text-accent-ink hover:underline"
                  >
                    {d.source === "plugin"
                      ? t("settings-channels.target_override_extension_settings")
                      : t("settings-channels.target_override_settings")}
                  </button>
                </>
              )}
            </SettingsHint>
          )}
        </Group>
      )}

      {routingSource && d.kind !== "app" && (
        <div className="mb-6" data-channel-routing>
          <ConfigurationForm source={routingSource} />
        </div>
      )}

      {editable && (d.features?.length ?? 0) > 0 && (
        <Group title={t("settings-channels.features")}>
          {d.features!.map((f) => (
            <Row key={f.id} label={f.title} hint={f.description} wrapHint>
              <Switch
                on={f.on}
                onChange={(on) => void ctl.change(`f:${f.id}`, { features: { [f.id]: on } })}
                disabled={!!ctl.busy}
                label={f.title}
              />
            </Row>
          ))}
        </Group>
      )}

      {(d.waiting.answers > 0 || d.waiting.asks > 0 || (d.conversations?.length ?? 0) > 0) && (
        <Group title={t("settings-channels.recent")}>
          {(d.waiting.answers > 0 || d.waiting.asks > 0) && (
            <Row label={t("settings-channels.waiting")}>
              {[
                d.waiting.answers > 0 && t("settings-channels.waiting_answers", { count: d.waiting.answers }),
                d.waiting.asks > 0 && t("settings-channels.waiting_asks", { count: d.waiting.asks }),
              ].filter(Boolean).join(" · ")}
            </Row>
          )}
          {d.conversations?.map((cv) => (
            <Row
              key={cv.session_id}
              label={cv.title || t("settings-channels.a_chat")}
              hint={t("settings-channels.chat_hint", { when: ago(cv.at) })}
            />
          ))}
        </Group>
      )}

      <Group title={t("settings-channels.capabilities")}>
        {capabilityText(d.capabilities).map((cap) => (
          <div key={cap} className="flex items-center gap-2 px-4 py-2 text-meta text-ink-2">
            <Icon name="check" size={12} stroke={2.2} className="shrink-0 text-ink-3" />
            {cap}
          </div>
        ))}
        {d.surfaces && d.surfaces.length > 0 && (
          <Row label={t("settings-channels.surfaces")}>{d.surfaces.join(t("settings-channels.list_separator"))}</Row>
        )}
      </Group>

      {editable && d.state === "connected" && (d.capabilities.push || replyOnly) && (
        <Group footer={replyOnly ? t("settings-channels.check_connection_footer") : t("settings-channels.test_footer")}>
          <div className="px-4 py-2.5">
            <Button
              tone="secondary"
              icon={replyOnly ? "check" : "send"}
              busy={ctl.busy === "test"}
              disabled={!!ctl.busy && ctl.busy !== "test"}
              onClick={() => void ctl.runTest()}
            >
              {replyOnly ? t("settings-channels.check_connection") : t("settings-channels.send_test")}
            </Button>
            {ctl.testFeedback && (
              <p
                role={ctl.testFeedback.tone === "error" ? "alert" : "status"}
                className={cx("mt-2 text-submeta break-words", ctl.testFeedback.tone === "error" ? "text-red" : "text-ink-2")}
              >
                {ctl.testFeedback.text}
              </p>
            )}
          </div>
        </Group>
      )}

      <TargetPicker
        open={picking}
        current={d.target}
        onClose={() => setPicking(false)}
        onPick={(target) => {
          setPicking(false);
          void ctl.change("target", { target });
        }}
      />
    </>
  );
}
