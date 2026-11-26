import { isIP } from "node:net";
import { isValidPin } from "./tls-pin.js";
import type { HubPin } from "./tls-pin.js";
import { parseHubAddress } from "./urls.js";
import type { HubAddress } from "./urls.js";

export type ParsedConnectInput =
  | {
      readonly kind: "hub";
      readonly hub: HubAddress;
      readonly initialURL: string;
      readonly autoSignIn: boolean;
      readonly pin?: HubPin;
    }
  | {
      readonly kind: "link-pair";
      readonly linkURL: string;
      readonly suggestedHub?: HubAddress;
      readonly pin?: HubPin;
    };

function isIPLiteral(hostname: string): boolean {
  const bare = hostname.startsWith("[") && hostname.endsWith("]") ? hostname.slice(1, -1) : hostname;
  return isIP(bare) !== 0;
}

function stripOuterQuotes(value: string): string {
  if (value.length >= 2) {
    const first = value[0];
    const last = value[value.length - 1];
    if ((first === "'" && last === "'") || (first === '"' && last === '"')) {
      return value.slice(1, -1).trim();
    }
  }
  return value;
}

function validPort(raw: string | null): string | undefined {
  const trimmed = raw?.trim() ?? "";
  if (!/^\d{1,5}$/.test(trimmed)) return undefined;
  const n = Number(trimmed);
  return n >= 1 && n <= 65535 ? String(n) : undefined;
}

function resolveHubAndPin(params: URLSearchParams): { readonly hub: HubAddress; readonly pin?: HubPin } | undefined {
  const urlParam = params.get("url")?.trim();
  if (!urlParam) return undefined;
  const base = parseHubAddress(urlParam);
  const fp = params.get("fp")?.trim() ?? "";
  const tls = validPort(params.get("tls"));
  const basePort = base.url.port || (base.url.protocol === "https:" ? "443" : "80");

  // Reverse-proxy HTTPS links with a hostname and no matching Hub TLS port use system CA trust (docs/operations/network.md).
  if (base.url.protocol === "https:" && !isIPLiteral(base.url.hostname) && (!tls || basePort !== tls)) {
    return { hub: base };
  }
  if (isValidPin(fp)) {
    if (base.url.protocol === "https:") {
      return { hub: base, pin: { origin: base.url.origin, fp } };
    }
    if (tls) {
      const host = base.url.hostname.includes(":") && !base.url.hostname.startsWith("[")
        ? `[${base.url.hostname}]`
        : base.url.hostname;
      const tlsHub = parseHubAddress(`https://${host}:${tls}/`);
      return { hub: tlsHub, pin: { origin: tlsHub.url.origin, fp } };
    }
  }
  return { hub: base };
}

/** Extract a clean `persona://link?...` or `jarvis://link?...` URI from raw input or a pasted CLI command. */
export function extractPersonaLinkURL(raw: string): string | undefined {
  const cleaned = stripOuterQuotes(raw.trim());
  if (!cleaned) return undefined;
  if (/^(?:persona|jarvis):\/\/link\?/i.test(cleaned) && !/[\s\u0000-\u001f\u007f]/.test(cleaned)) {
    try {
      const parsed = new URL(cleaned);
      return (parsed.protocol === "persona:" || parsed.protocol === "jarvis:") && parsed.host === "link" && parsed.search ? parsed.href : undefined;
    } catch {
      return undefined;
    }
  }
  const match = cleaned.match(/(?:persona|jarvis):\/\/link\?[^\s'"<>]+/i);
  if (!match) return undefined;
  try {
    const parsed = new URL(match[0]);
    return (parsed.protocol === "persona:" || parsed.protocol === "jarvis:") && parsed.host === "link" && parsed.search ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Parse whatever a user pastes into the desktop connection box:
 * 1. `persona://link?...` (or a CLI command containing it) -> `link-pair` (with optional suggestedHub + pin)
 * 2. `persona://pair?...` -> 1-step `hub` + `#token=...` auto-sign-in + optional SPKI `pin`
 * 3. `https://.../#token=...` or `?token=...` -> 1-step `hub` + `#token=...` auto-sign-in
 * 4. Plain Hub URL / host -> `hub` without token
 */
export function parseConnectInput(raw: string): ParsedConnectInput {
  const trimmed = raw.trim();
  if (!trimmed || trimmed.length > 4096) {
    throw new Error("Enter a Hub address or pairing link.");
  }
  const unquoted = stripOuterQuotes(trimmed);

  const linkURL = extractPersonaLinkURL(unquoted);
  if (linkURL) {
    const link = new URL(linkURL);
    let resolved: { readonly hub: HubAddress; readonly pin?: HubPin } | undefined;
    try { resolved = resolveHubAndPin(link.searchParams); } catch { resolved = undefined; }
    return {
      kind: "link-pair",
      linkURL,
      ...(resolved ? { suggestedHub: resolved.hub, ...(resolved.pin ? { pin: resolved.pin } : {}) } : {}),
    };
  }

  if (/^(?:persona|jarvis):\/\/pair(?:\?|$)/i.test(unquoted)) {
    if (/[\u0000-\u001f\u007f\s]/.test(unquoted)) {
      throw new Error("Enter a valid pairing link.");
    }
    const pair = new URL(unquoted);
    if (pair.username || pair.password || pair.port || pair.hash) {
      throw new Error("Enter a valid pairing link.");
    }
    const token = pair.searchParams.get("token")?.trim() ?? "";
    if (!token || token.length > 2048 || /[\u0000-\u0020\u007f]/.test(token)) {
      throw new Error("This pairing link is missing its token.");
    }
    const resolved = resolveHubAndPin(pair.searchParams);
    if (!resolved) {
      throw new Error("This pairing link is missing the Hub address.");
    }
    const initial = new URL(resolved.hub.url);
    initial.hash = `token=${encodeURIComponent(token)}`;
    return {
      kind: "hub",
      hub: resolved.hub,
      initialURL: initial.href,
      autoSignIn: true,
      ...(resolved.pin ? { pin: resolved.pin } : {}),
    };
  }

  if (/^https?:\/\//i.test(unquoted)) {
    if (/[\u0000-\u001f\u007f\s]/.test(unquoted)) {
      throw new Error("Enter a valid HTTP or HTTPS Hub address.");
    }
    let parsedURL: URL;
    try { parsedURL = new URL(unquoted); } catch {
      throw new Error("Enter a valid HTTP or HTTPS Hub address.");
    }
    if (parsedURL.username || parsedURL.password) {
      throw new Error("The Hub address cannot contain credentials.");
    }
    const hashParams = new URLSearchParams(parsedURL.hash.replace(/^#/, ""));
    const token = (hashParams.get("token") ?? parsedURL.searchParams.get("token"))?.trim() ?? "";
    if (token && !/[\u0000-\u0020\u007f]/.test(token) && token.length <= 2048) {
      const clean = new URL(parsedURL);
      clean.search = "";
      clean.hash = "";
      const hub = parseHubAddress(clean.href);
      const initial = new URL(hub.url);
      initial.hash = `token=${encodeURIComponent(token)}`;
      return {
        kind: "hub",
        hub,
        initialURL: initial.href,
        autoSignIn: true,
      };
    }
  }

  const hub = parseHubAddress(unquoted);
  return {
    kind: "hub",
    hub,
    initialURL: hub.url.href,
    autoSignIn: false,
  };
}
