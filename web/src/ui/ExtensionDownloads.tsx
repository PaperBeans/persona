import { formatNumber, t } from "../i18n";

/** Missing statistics stay missing, while explicit zero remains visible on every surface. */
export function packageDownloads(downloads?: number): string | null {
  return typeof downloads === "number" && Number.isInteger(downloads) && downloads >= 0
    ? t("settings-extensions.package_downloads", { downloads: formatNumber(downloads), count: downloads }) : null;
}

export function ExtensionDownloads({ downloads }: { downloads?: number }) {
  const text = packageDownloads(downloads);
  return text === null ? null : <span data-extension-downloads title={t("settings-extensions.package_downloads_definition")} className="text-[length:var(--extension-meta-size)] text-ink-3">{text}</span>;
}
