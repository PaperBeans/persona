import type { TranscriptItem } from './Domain';

/** The local status of one submitted message. The Hub's session status remains authoritative for actual work. */
export class ChatSendFeedback {
  static readonly acceptedWindow = 60000;
  readonly id: string;
  readonly machine: string;
  readonly session: string;
  readonly afterSequence: number;
  readonly startedAt: number;
  private phase: string = 'sending';
  acceptedAt: number = 0;
  echoSequence: number = -1;
  private status: string;
  private failure: string;
  private stateSince: string;

  constructor(id: string, machine: string, session: string, afterSequence: number, startedAt: number,
    status: string, failure: string, stateSince: string) {
    this.id = id; this.machine = machine; this.session = session; this.afterSequence = afterSequence; this.startedAt = startedAt;
    this.status = status; this.failure = failure; this.stateSince = stateSince;
  }

  visible(now: number): string {
    if (this.phase === 'processing' && now >= this.acceptedAt + ChatSendFeedback.acceptedWindow) return '';
    return this.phase;
  }

  accept(at: number): void {
    if (this.phase !== 'sending') return;
    this.acceptedAt = at; this.phase = 'processing';
  }

  finish(): void { this.phase = ''; }

  observeItems(items: readonly TranscriptItem[]): void {
    if (!this.phase) return;
    if (this.echoSequence < 0) {
      const echo = items.find((item: TranscriptItem) => item.seq > this.afterSequence && item.role === 'user' &&
        typeof item.client_msg_id === 'string' && item.client_msg_id.toLowerCase() === this.id.toLowerCase());
      if (echo) this.echoSequence = echo.seq;
    }
    if (this.echoSequence >= 0 && items.some((item: TranscriptItem) => item.seq > this.echoSequence && item.role === 'assistant' && item.kind === 'text')) this.finish();
  }

  observeSession(status: string, failure: string, stateSince: string): void {
    if (this.phase === 'processing') {
      const terminal = (status === 'blocked' || status === 'offline') && status !== this.status;
      const failed = !!failure && (failure !== this.failure || stateSince !== this.stateSince);
      const ended = this.echoSequence >= 0 && ['working','blocked'].includes(this.status) && !!status && !['working','blocked'].includes(status);
      if (terminal || failed || ended) this.finish();
    }
    this.status = status; this.failure = failure; this.stateSince = stateSince;
  }
}
