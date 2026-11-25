import assert from "node:assert/strict";
import test from "node:test";
import { classifyConnectError } from "../src/connect-errors.js";

test("classifyConnectError maps Chromium cert and network errors to friendly categories", () => {
  assert.deepEqual(
    classifyConnectError(new Error("ERR_CERT_AUTHORITY_INVALID (-202) loading 'https://hub.example/?token=secret'"), false),
    { code: "cert_untrusted", technical: "ERR_CERT_AUTHORITY_INVALID" },
  );
  assert.deepEqual(
    classifyConnectError(new Error("ERR_CERT_AUTHORITY_INVALID (-202)"), true),
    { code: "cert_pin_mismatch", technical: "ERR_CERT_AUTHORITY_INVALID" },
  );
  assert.equal(classifyConnectError({ code: "ERR_CERT_DATE_INVALID", message: "expired" }).code, "cert_expired");
  assert.equal(classifyConnectError(new Error("ERR_SSL_PROTOCOL_ERROR")).code, "tls_protocol");
  assert.equal(classifyConnectError(new Error("ERR_CONNECTION_REFUSED")).code, "connection_refused");
  assert.equal(classifyConnectError(new Error("ERR_NAME_NOT_RESOLVED")).code, "dns_failed");
  assert.equal(classifyConnectError(new Error("ERR_CONNECTION_TIMED_OUT")).code, "timeout");
  assert.equal(classifyConnectError(new Error("ERR_INTERNET_DISCONNECTED")).code, "offline");
  assert.deepEqual(classifyConnectError(new Error("unexpected https://hub.example/#token=secret")), { code: "unknown" });
});
