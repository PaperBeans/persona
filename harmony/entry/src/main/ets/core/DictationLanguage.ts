import type { TranscriptItem } from './Domain';

// Dictation runs only when the microphone is tapped. Keep the language hint on
// device and use only text the person actually authored in this conversation.
export function dictationTexts(draft: string, items: readonly TranscriptItem[]): string[] {
  const texts: string[] = draft.trim() ? [draft.slice(0, 320)] : [];
  for (let i = items.length - 1; i >= 0 && texts.length < 6; i--) {
    const item = items[i];
    if (item.kind === 'text' && item.role === 'user' && !item.wake && !item.from && !item.injected && !item.source && item.text?.trim()) {
      texts.push(item.text.slice(0, 320));
    }
  }
  return texts;
}

export function dictationLocale(choice: string, texts: string[], last: string, interfaceLanguage: string): string {
  if (choice === 'zh-CN' || choice === 'keyboard') return choice;
  for (const text of texts) {
    const han = (text.match(/[\u3400-\u4dbf\u4e00-\u9fff]/g) || []).length;
    const kana = (text.match(/[\u3040-\u30ff]/g) || []).length;
    const latin = (text.match(/[A-Za-z]/g) || []).length;
    if (kana >= 3 && han + kana >= 6) return 'ja-JP';
    if (han >= 6 && han >= 0.8 * (han + kana + latin)) return 'zh-CN';
    if (latin >= 18 && latin >= 0.8 * (han + kana + latin) && /[A-Za-z]+\s+[A-Za-z]+/.test(text)) return 'en-US';
  }
  return last || interfaceLanguage;
}
