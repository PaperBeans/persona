// A secret provider's reference as a setting holds it (docs/extensions/secret-providers.md), and how the apps say where
// its value comes from. Its own module, not lib/extensions.ts: the settings pages that read it load lazily, and their
// importing lib/extensions.ts split that first-screen module into a chunk of its own, 1.4 kB more on the first screen.
import { t } from "../i18n";

/** A secret provider's reference a setting holds: whose it is, the reference and what it names in words. */
export interface SecretReference {
  provider: string;
  provider_name: string;
  reference: string;
  title?: string;
}

/** "From <provider> · <title>" for a setting that holds a reference; the reference itself once its provider is gone. */
export function referenceState(r: SecretReference): string {
  const provider = r.provider_name || r.provider;
  if (!provider) return r.title || r.reference;
  const from = t("lib-extensions.from_provider", { provider });
  return r.title ? `${from} · ${r.title}` : from;
}
