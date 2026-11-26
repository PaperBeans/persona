/** Unconsumed bytes of a socket read. A TLS record can contain parts of several WebSocket frames. */
export class SocketReceiveBuffer {
  private storage = new Uint8Array(0);
  private start: number = 0;
  private end: number = 0;

  get length(): number { return this.end - this.start; }

  view(): Uint8Array { return this.storage.subarray(this.start, this.end); }

  append(input: Uint8Array, limit: number): void {
    const size = this.length + input.length;
    if (size > limit) throw new Error('frame_too_large');
    if (size > this.storage.length) {
      const grown = new Uint8Array(Math.min(limit, Math.max(size, this.storage.length * 2, 1024)));
      grown.set(this.view());
      this.end = this.length;
      this.start = 0;
      this.storage = grown;
    } else if (this.storage.length - this.end < input.length) {
      this.storage.copyWithin(0, this.start, this.end);
      this.end = this.length;
      this.start = 0;
    }
    this.storage.set(input, this.end);
    this.end += input.length;
  }

  consume(size: number): void {
    this.start += size;
    if (this.start === this.end) {
      this.start = 0;
      this.end = 0;
      if (this.storage.length > 1024 * 1024) this.storage = new Uint8Array(0);
    }
  }
}
