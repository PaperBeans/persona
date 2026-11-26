// Lazy-loaded L3 per-step attribution row shown only when a step card is expanded (#13054).
// Displays honest per-step token consumption, elapsed duration, and optional TTFT from reported telemetry.
import { Fragment, useMemo, type ReactNode } from "react";
import { t } from "../../i18n";
import { formatLatencyMs, summarizeStepTelemetry } from "../../lib/session-insights";
import { compactTokens } from "../../lib/tokens";
import type { Item } from "../../lib/types";
import { workDuration } from "../../lib/work-groups";
import { cx } from "../../ui/atoms";

function formatStepDuration(ms: number): string {
  return ms < 10_000 ? formatLatencyMs(ms) : workDuration(ms);
}

export function StepTelemetry({
  items,
  durationMs,
  className,
}: {
  items: readonly (Item | undefined)[];
  durationMs?: number | null;
  className?: string;
}) {
  const telemetry = useMemo(() => summarizeStepTelemetry(items, durationMs), [items, durationMs]);
  if (!telemetry) return null;

  const tokenDetail = telemetry.tokens
    ? [
        t("chat-sessioninsights.step_tokens_detail", {
          input: compactTokens(telemetry.tokens.input),
          output: compactTokens(telemetry.tokens.output),
        }),
        telemetry.tokens.hasCache
          ? t("chat-sessioninsights.step_tokens_cache", {
              read: compactTokens(telemetry.tokens.cacheRead),
              write: compactTokens(telemetry.tokens.cacheWrite),
            })
          : "",
      ]
        .filter(Boolean)
        .join(" · ")
    : undefined;

  const parts: ReactNode[] = [];
  if (telemetry.tokens) {
    parts.push(
      <span key="tokens" data-step-tokens title={tokenDetail}>
        {t("chat-sessioninsights.step_tokens", { tokens: compactTokens(telemetry.tokens.total) })}
      </span>,
    );
  }
  if (telemetry.durationMs !== null) {
    parts.push(
      <span key="duration" data-step-duration>
        {formatStepDuration(telemetry.durationMs)}
      </span>,
    );
  }
  if (telemetry.ttftMs !== null) {
    parts.push(
      <span key="ttft" data-step-ttft>
        {t("chat-sessioninsights.step_ttft", { ttft: formatLatencyMs(telemetry.ttftMs) })}
      </span>,
    );
  }

  return (
    <div
      data-step-telemetry
      className={cx("flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-sans text-submeta text-ink-3 tabular-nums", className)}
    >
      {parts.map((part, i) => (
        <Fragment key={i}>
          {i > 0 && <span aria-hidden>·</span>}
          {part}
        </Fragment>
      ))}
    </div>
  );
}
