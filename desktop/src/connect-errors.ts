export type ConnectErrorCode =
  | "cert_untrusted"
  | "cert_pin_mismatch"
  | "cert_expired"
  | "tls_protocol"
  | "connection_refused"
  | "dns_failed"
  | "timeout"
  | "offline"
  | "invalid_url"
  | "unknown";

export interface ConnectErrorInfo {
  readonly code: ConnectErrorCode;
  readonly technical?: string;
}

const CHROMIUM_TOKEN = /\bERR_[A-Z0-9_]+\b/;

/** Extract a short sanitized Chromium/network code without leaking URLs, query parameters, or tokens. */
function extractTechnicalCode(error: unknown): string | undefined {
  if (!error || typeof error !== "object") return undefined;
  const rawCode = (error as { code?: unknown }).code;
  if (typeof rawCode === "string") {
    const match = rawCode.match(CHROMIUM_TOKEN);
    if (match) return match[0];
    if (/^[A-Z0-9_-]{2,48}$/.test(rawCode)) return rawCode;
  }
  const rawMessage = (error as { message?: unknown }).message;
  if (typeof rawMessage === "string") {
    const match = rawMessage.match(CHROMIUM_TOKEN);
    if (match) return match[0];
  }
  return undefined;
}

/** Map raw Chromium / Electron load failures to plain-language error categories. */
export function classifyConnectError(error: unknown, hadPin = false): ConnectErrorInfo {
  const technical = extractTechnicalCode(error);
  const rawMessage = error && typeof error === "object" && typeof (error as { message?: unknown }).message === "string"
    ? (error as { message: string }).message
    : "";
  const haystack = `${technical ?? ""} ${rawMessage}`;

  let code: ConnectErrorCode = "unknown";
  if (/ERR_CERT_DATE_INVALID/.test(haystack)) {
    code = "cert_expired";
  } else if (/ERR_CERT_(?:AUTHORITY_INVALID|COMMON_NAME_INVALID|INVALID|NO_REVOCATION_MECHANISM|UNABLE_TO_CHECK_REVOCATION|REVOKED)/.test(haystack)) {
    code = hadPin ? "cert_pin_mismatch" : "cert_untrusted";
  } else if (/ERR_SSL_/.test(haystack)) {
    code = "tls_protocol";
  } else if (/ERR_CONNECTION_REFUSED|ECONNREFUSED/.test(haystack)) {
    code = "connection_refused";
  } else if (/ERR_NAME_NOT_RESOLVED|ENOTFOUND/.test(haystack)) {
    code = "dns_failed";
  } else if (/ERR_(?:CONNECTION_TIMED_OUT|TIMED_OUT)|ETIMEDOUT/.test(haystack)) {
    code = "timeout";
  } else if (/ERR_(?:INTERNET_DISCONNECTED|NETWORK_CHANGED|ADDRESS_UNREACHABLE)|ENETUNREACH|EHOSTUNREACH/.test(haystack)) {
    code = "offline";
  }

  return technical ? { code, technical } : { code };
}
