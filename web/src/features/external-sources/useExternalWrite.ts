import { useLayoutEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { ApiError, credentialOwner, errorText } from "../../lib/api";

function externalWriteError(error: unknown): string {
  if (error instanceof ApiError) {
    switch (error.code) {
      case "external_binding_exists": return t("external-sources.duplicate");
      case "external_target_not_found": return t("external-sources.target_gone");
      case "external_binding_not_found": return t("external-sources.binding_gone");
    }
  }
  return errorText(error);
}

/** Serialize writes and apply results only to the dialog, view and login that initiated them. */
export function useExternalWrite() {
  const alive = useRef(false);
  const writing = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useLayoutEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);

  async function write<T>(request: () => Promise<T>, done: (value: T) => void) {
    if (writing.current) return;
    const owner = credentialOwner();
    const current = () => alive.current && owner === credentialOwner();
    writing.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await request();
      if (current()) done(result);
    } catch (failure) {
      if (current()) setError(externalWriteError(failure));
    } finally {
      writing.current = false;
      if (current()) setBusy(false);
    }
  }

  return { busy, error, write };
}
