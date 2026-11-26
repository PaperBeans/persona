import { customScan, scanBarcode, scanCore } from '@kit.ScanKit';
import type { BusinessError } from '@kit.BasicServicesKit';
import { Pairing } from './Pairing';

// Scan Kit owns one camera stream. Finish closing the previous surface before opening its replacement.
export class PhonePairScanner {
  private generation: number = 0;
  private initialized: boolean = false;
  private pending: Promise<void> = Promise.resolve();

  start(surface: customScan.ViewControl, onCode: (value: string) => void, unavailable: () => void): number {
    const generation = ++this.generation;
    this.pending = this.pending.then(async (): Promise<void> => {
      await this.release();
      if (generation !== this.generation) return;
      const failed = (): void => {
        if (generation !== this.generation) return;
        this.stop(generation);
        unavailable();
      };
      try {
        customScan.init({ scanTypes: [scanCore.ScanType.QR_CODE], enableMultiMode: false, enableAlbum: false });
        this.initialized = true;
        customScan.start(surface, (error: BusinessError, results: scanBarcode.ScanResult[]): void => {
          if (generation !== this.generation) return;
          if (error) { failed(); return; }
          const values = Array.from(new Set<string>(results.map((result: scanBarcode.ScanResult) => result.originalValue)));
          try {
            if (values.length !== 1) throw new Error('invalid_pairing');
            Pairing.parse(values[0]);
          } catch (_) {
            try { customScan.rescan(); } catch (_) { failed(); }
            return;
          }
          this.stop(generation);
          onCode(values[0]);
        });
      } catch (_) { failed(); }
    });
    return generation;
  }

  stop(generation: number): void {
    if (generation !== this.generation) return;
    this.generation++;
    this.pending = this.pending.then(() => this.release());
  }

  private async release(): Promise<void> {
    if (!this.initialized) return;
    this.initialized = false;
    try { await customScan.stop(); } catch (_) {}
    try { await customScan.release(); } catch (_) {}
  }
}

export const phonePairScanner: PhonePairScanner = new PhonePairScanner();
