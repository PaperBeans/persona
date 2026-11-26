import { audio } from '@kit.AudioKit';
import { speechRecognizer } from '@kit.CoreSpeechKit';
import { util } from '@kit.ArkTS';
import { requestRecordingMicrophone } from './NativeRecording';

export interface DictationListener {
  listening: () => void;
  preview: (text: string) => void;
  completed: (text: string) => void;
  failed: (message: string) => void;
  cancelled: () => void;
}

// Core Speech consumes 20 ms of mono 16 kHz signed PCM in each 640-byte frame.
const FRAME_BYTES: number = 640;
const MAX_FRAMES: number = 60 * 50 - 2;

export class NativeDictation {
  private static current: NativeDictation | undefined;
  private session: string = util.generateRandomUUID();
  private engine: speechRecognizer.SpeechRecognitionEngine | undefined;
  private input: audio.AudioCapturer | undefined;
  private listener: DictationListener;
  private frame: Uint8Array = new Uint8Array(FRAME_BYTES);
  private filled: number = 0;
  private frames: number = 0;
  private deadline: number = -1;
  private ending: boolean = false;
  private finalText: string = '';
  private pendingText: string = '';
  private confirmedText: string = '';
  private owner: string;
  private read: (data: ArrayBuffer) => void = (data: ArrayBuffer): void => this.feed(data);

  constructor(listener: DictationListener, owner: string) {
    this.listener = listener;
    this.owner = owner;
  }

  takeConfirmed(owner: string): string {
    const text = this.confirmedText;
    this.confirmedText = '';
    return owner === this.owner ? text : '';
  }

  static cancelActive(): void { NativeDictation.current?.cancel(); }

  private active(): boolean { return NativeDictation.current === this; }

  private timeout(ms: number): void {
    clearTimeout(this.deadline);
    this.deadline = setTimeout((): void => this.fail('phone_dictation_unavailable'), ms);
  }

  async start(context: Object): Promise<void> {
    NativeDictation.cancelActive();
    NativeDictation.current = this;
    try {
      await requestRecordingMicrophone(context);
      if (!this.active()) return;
      this.timeout(12000);
      const engine = await speechRecognizer.createEngine({language: 'zh-CN', online: 1,
        extraParams: {locate: 'CN', recognizerMode: 'long'}});
      if (!this.active()) { engine.shutdown(); return; }
      this.engine = engine;
      engine.setListener({
        onStart: (id: string): void => { if (id === this.session && this.active()) this.startInput(); },
        onEvent: (_id: string, _code: number, _message: string): void => {},
        onResult: (id: string, result: speechRecognizer.SpeechRecognitionResult): void => {
          if (id !== this.session || !this.active() || !result.result.trim()) return;
          if (result.isFinal) { this.finalText += result.result.trim(); this.pendingText = ''; }
          else this.pendingText = result.result.trim();
          this.listener.preview(this.finalText + this.pendingText);
        },
        onComplete: (id: string): void => {
          if (id !== this.session || !this.active()) return;
          const text = this.finalText + this.pendingText;
          this.confirmedText = text;
          this.release();
          if (text) this.listener.completed(text);
          else this.listener.failed('phone_dictation_empty');
        },
        onError: (id: string, code: number): void => {
          if (id === this.session) this.fail(code === 1002200012 ?
            'knowledge_recording_microphone_denied' : 'phone_dictation_unavailable');
        }
      });
      engine.startListening({sessionId: this.session, audioInfo: {audioType: 'pcm', sampleRate: 16000,
        soundChannel: 1, sampleBit: 16}, extraParams: {recognitionMode: 1, maxAudioDuration: 60000}});
    } catch (error) {
      this.fail(error instanceof Error && error.message === 'knowledge_recording_microphone_denied' ?
        'knowledge_recording_microphone_denied' : 'phone_dictation_unavailable');
    }
  }

  private async startInput(): Promise<void> {
    try {
      const input = await audio.createAudioCapturer({
        streamInfo: {samplingRate: audio.AudioSamplingRate.SAMPLE_RATE_16000, channels: audio.AudioChannel.CHANNEL_1,
          sampleFormat: audio.AudioSampleFormat.SAMPLE_FORMAT_S16LE, encodingType: audio.AudioEncodingType.ENCODING_TYPE_RAW},
        capturerInfo: {source: audio.SourceType.SOURCE_TYPE_MIC, capturerFlags: 0}
      });
      if (!this.active() || this.ending) { await input.release(); return; }
      this.input = input;
      input.on('readData', this.read);
      await input.start();
      if (!this.active() || this.ending) return;
      this.timeout(65000);
      this.listener.listening();
    } catch (_) { this.fail('phone_dictation_unavailable'); }
  }

  private feed(data: ArrayBuffer): void {
    if (!this.active() || this.ending || !this.engine) return;
    const bytes = new Uint8Array(data);
    try {
      let offset = 0;
      while (offset < bytes.length && this.active() && !this.ending) {
        const take = Math.min(FRAME_BYTES - this.filled, bytes.length - offset);
        this.frame.set(bytes.subarray(offset, offset + take), this.filled);
        this.filled += take;
        offset += take;
        if (this.filled === FRAME_BYTES) {
          this.engine.writeAudio(this.session, this.frame);
          this.frame = new Uint8Array(FRAME_BYTES);
          this.filled = 0;
          if (++this.frames >= MAX_FRAMES) this.finish();
        }
      }
    } catch (_) { this.fail('phone_dictation_unavailable'); }
  }

  finish(): void {
    if (!this.active() || this.ending || !this.engine) return;
    this.ending = true;
    this.releaseInput();
    try {
      if (this.filled) this.engine.writeAudio(this.session, this.frame);
      this.timeout(7000);
      this.engine.finish(this.session);
    } catch (_) { this.fail('phone_dictation_unavailable'); }
  }

  cancel(): void {
    this.confirmedText = '';
    if (!this.active()) return;
    this.release(true);
    this.listener.cancelled();
  }

  private fail(key: string): void {
    if (!this.active()) return;
    this.release(true);
    this.listener.failed(key);
  }

  private releaseInput(): void {
    const input = this.input;
    this.input = undefined;
    if (!input) return;
    try { input.off('readData', this.read); } catch (_) {}
    input.release().catch(() => {});
  }

  private release(cancel: boolean = false): void {
    NativeDictation.current = undefined;
    clearTimeout(this.deadline);
    this.releaseInput();
    const engine = this.engine;
    this.engine = undefined;
    if (cancel && engine) { try { engine.cancel(this.session); } catch (_) {} }
    if (engine) { try { engine.shutdown(); } catch (_) {} }
  }
}
