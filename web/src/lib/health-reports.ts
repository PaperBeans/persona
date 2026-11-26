// Read-side contract for the Health space's stored checkup reports. Writers stay in modules/health/reports.ts;
// the default panel uses these same wire types and paths when the module is off.
import { enc } from "./api";

/** One report in reports/ (GET /machines/{mid}/health/reports, docs/protocol/protocol.md): an original, its record, or both. */
export interface HealthReport {
  name: string;
  title: string;
  path?: string;
  record_path?: string;
  size: number;
  modified_at: string;
}

export const healthReportsPath = (mid: string) => `/machines/${enc(mid)}/health/reports`;
