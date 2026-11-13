export interface BlobReadLimit {
  maxBytes: number;
  mimeTypes: readonly string[];
}

/** An opt-in reader for the normal API's binary seam, with no separate authentication or error handling. */
export function boundedBlobReader({ maxBytes, mimeTypes }: BlobReadLimit) {
  if (!Number.isSafeInteger(maxBytes) || maxBytes < 1) throw new RangeError("Invalid blob limit");
  const allowed = new Set(mimeTypes.map(type => type.toLowerCase()));
  return async (response: Response, signal?: AbortSignal | null): Promise<Blob> => {
    const type = response.headers.get("Content-Type")?.split(";")[0].trim().toLowerCase() ?? "";
    const length = response.headers.get("Content-Length");
    const abortError = () => new DOMException("Aborted", "AbortError");
    const invalid = !allowed.has(type) || (length !== null && (!/^\d+$/.test(length) || Number(length) > maxBytes));
    if (invalid || signal?.aborted) {
      try { await response.body?.cancel(); } catch { /* The response may already be closed. */ }
      if (signal?.aborted) throw abortError();
      throw new Error("Invalid bounded blob response");
    }
    const reader = response.body?.getReader();
    if (!reader) throw new Error("Missing binary response body");
    const abort = () => { void reader.cancel(signal?.reason).catch(() => {}); };
    signal?.addEventListener("abort", abort, { once: true });
    let bytes = 0;
    const chunks: BlobPart[] = [];
    try {
      while (true) {
        if (signal?.aborted) throw abortError();
        const { done, value } = await reader.read();
        if (signal?.aborted) throw abortError();
        if (done) break;
        bytes += value.byteLength;
        if (bytes > maxBytes) throw new Error("Binary response exceeds blob limit");
        // A producer may reuse its buffer; retain only a bounded immutable copy.
        chunks.push(value.slice());
      }
      return new Blob(chunks, { type });
    } catch (error) {
      try { await reader.cancel(); } catch { /* Keep the original read or cancellation error. */ }
      throw error;
    } finally {
      signal?.removeEventListener("abort", abort);
      reader.releaseLock();
    }
  };
}
