import { X509Certificate, createHash, timingSafeEqual } from "node:crypto";

export interface HubPin {
  readonly origin: string;
  readonly fp: string;
}

export interface CertificateVerifyRequest {
  readonly hostname: string;
  readonly certificateData: string;
  readonly verificationResult: string;
}

const PIN_PATTERN = /^[A-Za-z0-9_-]{43}$/;

/** Normalize hostnames from URLs or Chromium callbacks (strip IPv6 brackets and lowercase). */
function normalizeHostname(hostname: string): string {
  const trimmed = hostname.trim().toLowerCase();
  return trimmed.startsWith("[") && trimmed.endsWith("]") ? trimmed.slice(1, -1) : trimmed;
}

/** Validate an unpadded base64url SHA-256 SPKI fingerprint (43 characters). */
export function isValidPin(fp: string): boolean {
  return PIN_PATTERN.test(fp);
}

/** Compute the Hub-compatible unpadded base64url SHA-256 SPKI pin of a PEM or DER X.509 certificate. */
export function certificateSPKIPin(pemOrDer: string | Buffer): string | undefined {
  if (!pemOrDer || (typeof pemOrDer === "string" && !pemOrDer.trim())) return undefined;
  try {
    const cert = new X509Certificate(pemOrDer);
    const spki = cert.publicKey.export({ type: "spki", format: "der" });
    return createHash("sha256").update(spki).digest("base64url");
  } catch {
    return undefined;
  }
}

function effectivePort(url: URL): string {
  if (url.port) return url.port;
  if (url.protocol === "https:" || url.protocol === "wss:") return "443";
  if (url.protocol === "http:" || url.protocol === "ws:") return "80";
  return "";
}

function parsePinnedOrigin(pin: HubPin): URL | undefined {
  try {
    const parsed = new URL(pin.origin);
    return parsed.protocol === "https:" && parsed.hostname ? parsed : undefined;
  } catch {
    return undefined;
  }
}

/**
 * Restrict requests within a pinned session so Chromium's hostname-only `setCertificateVerifyProc`
 * callback cannot apply the pin to another port or plaintext scheme on the same hostname.
 */
export function allowPinnedSessionRequest(rawURL: string, pin: HubPin | undefined): boolean {
  if (!pin || !isValidPin(pin.fp)) return true;
  const pinned = parsePinnedOrigin(pin);
  if (!pinned) return false;
  let target: URL;
  try {
    target = new URL(rawURL);
  } catch {
    return false;
  }
  const networkScheme = target.protocol === "https:" || target.protocol === "wss:"
    || target.protocol === "http:" || target.protocol === "ws:";
  if (!networkScheme) return true;
  if (normalizeHostname(target.hostname) !== normalizeHostname(pinned.hostname)) return true;
  return (target.protocol === "https:" || target.protocol === "wss:")
    && effectivePort(target) === effectivePort(pinned);
}

/**
 * Electron `session.setCertificateVerifyProc` verdict:
 * - `-3`: fall back to Chromium's system CA / hostname verification (when unpinned or for any other host)
 * - `0`: trust when the leaf certificate's SPKI SHA-256 matches the pinned Hub fingerprint
 * - `-2`: reject on pin mismatch or unreadable certificate
 */
export function verifyPinnedCertificate(
  request: CertificateVerifyRequest,
  pin: HubPin | undefined,
): number {
  if (!pin || !isValidPin(pin.fp)) return -3;
  const pinned = parsePinnedOrigin(pin);
  if (!pinned) return -2;
  if (normalizeHostname(request.hostname) !== normalizeHostname(pinned.hostname)) return -3;
  const actual = certificateSPKIPin(request.certificateData);
  if (!actual || !isValidPin(actual)) return -2;
  const a = Buffer.from(actual, "utf8");
  const b = Buffer.from(pin.fp, "utf8");
  return a.length === b.length && timingSafeEqual(a, b) ? 0 : -2;
}
