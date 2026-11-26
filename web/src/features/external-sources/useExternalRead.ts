import { useEffect, useState } from "react";

type Reading<T> =
  | { data: T; error?: never }
  | { data?: never; error: unknown };

/** A read belongs to its parameters and retry attempt, even while the previous request settles. */
export function useExternalRead<T>(read: ((signal: AbortSignal) => Promise<T>) | null) {
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<{ read: typeof read; attempt: number; value: Reading<T> }>();
  useEffect(() => {
    if (!read) return;
    const controller = new AbortController();
    const keep = (value: Reading<T>) => {
      if (!controller.signal.aborted) setResult({ read, attempt, value });
    };
    void read(controller.signal).then((data) => keep({ data }), (error) => keep({ error }));
    return () => controller.abort();
  }, [read, attempt]);

  const value = result?.read === read && result.attempt === attempt ? result.value : undefined;
  return { data: value?.data, error: value?.error, loading: Boolean(read && !value), retry: () => setAttempt((n) => n + 1) };
}
