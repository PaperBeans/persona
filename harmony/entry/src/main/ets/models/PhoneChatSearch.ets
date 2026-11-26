import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';
import { searchHitRanges } from '../core/PhoneSearchExcerpt';

export class PhoneChatMatch {
  row: ResourceRow;
  subtitle: string;
  hitId: string;
  ranges: number[][];
  constructor(row: ResourceRow, subtitle: string, hitId: string = '', ranges: number[][] = []) {
    this.row = row; this.subtitle = subtitle; this.hitId = hitId; this.ranges = ranges;
  }
}

function lastText(row: ResourceRow): string {
  return Wire.text(row.data(), 'last_text') || row.detail;
}
function newer(a: ResourceRow, b: ResourceRow): number {
  const date = (r: ResourceRow): number => Date.parse(Wire.text(r.data(), 'updated_at') || Wire.text(r.data(), 'last_at')) || 0;
  return date(b) - date(a);
}
export function localChatMatches(rows: ResourceRow[], term: string, agents: boolean): PhoneChatMatch[] {
  const words = term.trim().toLowerCase().split(/\s+/).filter((v: string) => !!v);
  return rows.filter((r: ResourceRow) => {
    const data = r.data();
    if (Wire.bool(data, 'archived') || agents && (Wire.bool(data, 'is_subagent') || Wire.bool(data, 'assistant'))) return false;
    const text = [r.title, lastText(r), agents ? r.path + ' ' + r.machine : ''].join(' ').toLowerCase();
    return words.every((word: string) => text.includes(word));
  }).sort(newer).map((r: ResourceRow) => new PhoneChatMatch(r, lastText(r) || (agents ? r.path : '')));
}

@ObservedV2
export class PhoneSearchIndex {
  @Trace matches: PhoneChatMatch[] = [];
  @Trace partial: boolean = false;
  @Trace busy: boolean = false;
  @Trace failed: boolean = false;
  private revision: number = 0;
  private timer: number = -1;

  close(): void {
    this.revision++;
    if (this.timer >= 0) clearTimeout(this.timer);
    this.timer = -1; this.matches = []; this.busy = false; this.partial = false; this.failed = false;
  }
  schedule(model: ClientModel, input: string, agents: boolean): void {
    this.close();
    const term = input.trim();
    if (!term || agents || !model.connected || !model.features.includes('assistant_search')) return;
    const rev = this.revision, scope = model.scope(), epoch = model.connectionGeneration();
    this.busy = true;
    this.timer = setTimeout(() => {
      this.timer = -1;
      this.lookup(model, term, rev, epoch, scope);
    }, 200);
  }
  private async lookup(model: ClientModel, q: string, rev: number, epoch: number, scope: string): Promise<void> {
    const current = (): boolean => rev === this.revision && model.owns(epoch, scope);
    if (!current()) return;
    try {
      const res = await model.repo().call('/api/v1/assistant/search?q=' + encodeURIComponent(q) + '&limit=30');
      if (!current()) return;
      const found: PhoneChatMatch[] = [];
      for (const v of Wire.rows(res, 'results')) {
        const id = Wire.text(v, 'session_id'), machine = Wire.text(v, 'machine_id');
        if (!id || !machine) continue;
        const row = model.conversations.find((r: ResourceRow) => r.id === id && r.machine === machine) || ResourceRow.from(v,'conversation');
        if (!model.conversations.includes(row)) { row.id = id; row.machine = machine; row.session = id; row.manager = model.profile.manager; row.group = model.profile.id; }
        const hit = Wire.rows(v, 'hits')[0];
        found.push(new PhoneChatMatch(row, hit ? Wire.text(hit, 'snippet') : lastText(row),hit ? Wire.text(hit,'item_id') : '',
          hit ? searchHitRanges(hit['ranges']) : []));
      }
      this.matches = found; this.partial = Wire.bool(res, 'partial');
    } catch (err) {
      if (current()) {
        const reason = (err as Error).message;
        this.failed = reason !== 'unsupported' && reason !== 'not_found' && reason !== 'unsupported_feature';
      }
    } finally { if (current()) this.busy = false; }
  }
}

export function mergeChatMatches(local: PhoneChatMatch[], remote: PhoneChatMatch[]): PhoneChatMatch[] {
  const result = local.slice();
  for (const hit of remote) {
    const i = result.findIndex((v: PhoneChatMatch) => v.row.id === hit.row.id && v.row.machine === hit.row.machine);
    if (i < 0) result.push(hit); else if (hit.subtitle) result[i] = new PhoneChatMatch(result[i].row, hit.subtitle, hit.hitId,hit.ranges);
  }
  return result;
}
