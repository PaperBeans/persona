// Additional fields for an expanded model error. The chat keeps the cause, actions and raw text available immediately.
import type { APIErrorInfo } from "../../lib/api-errors";
import type { RecoveryExhaustedView } from "../../lib/model-fallback";
import { clock } from "../../lib/format";
import { t } from "../../i18n";
import { copyText, cx } from "../../ui/atoms";
import { ErrorDetailsText } from "../../ui/ErrorNotice";
import { Icon } from "../../ui/icons";

export function APIErrorDetails({ info, count, at, exhausted }: { info: APIErrorInfo; count: number; at?: string; exhausted?: RecoveryExhaustedView | null }) {
  return (
    <div className="flex flex-col gap-1.5">
      {info.requestId && <Field label={t("chat-lines.request_id")} value={info.requestId} />}
      {info.session && <Field label="Session" value={info.session} />}
      {at && <Field label={t("chat-lines.time")} value={clock(at)} copy={false} />}
      {info.status !== undefined && <Field label={t("chat-lines.status")} value={String(info.status)} copy={false} />}
      {count > 1 && <Field label={t("chat-lines.repeated")} value={t("chat-lines.api_repeated", { count })} copy={false} mono={false} />}
      {exhausted?.tried && (
        <div className="min-w-0 text-label leading-5 [overflow-wrap:anywhere] text-ink-2" data-testid="recovery-exhausted-tried">
          {t("chat-modelfallback.exhausted_tried", { models: exhausted.tried })}
        </div>
      )}
      <ErrorDetailsText text={info.raw} />
      <button type="button" onClick={() => copyText(info.raw)} className="touch-hit flex w-fit items-center gap-1 text-label font-medium whitespace-nowrap text-accent-ink hover:underline">
        <Icon name="copy" size={12} />
        {t("chat-lines.copy_raw")}
      </button>
    </div>
  );
}

function Field({ label, value, copy = true, mono = true }: { label: string; value: string; copy?: boolean; mono?: boolean }) {
  return (
    <div className="flex min-w-0 items-center gap-2 text-label leading-5">
      <span className="w-20 shrink-0 truncate text-ink-3">{label}</span>
      <span className={cx("min-w-0 truncate text-ink-2", mono && "font-mono")}>{value}</span>
      {copy && (
        <button type="button" onClick={() => copyText(value)} aria-label={t("chat-lines.copy_field", { label })} className="flex size-5 shrink-0 items-center justify-center rounded-[5px] text-ink-3 hover:bg-hover-2 hover:text-ink">
          <Icon name="copy" size={11} />
        </button>
      )}
    </div>
  );
}
