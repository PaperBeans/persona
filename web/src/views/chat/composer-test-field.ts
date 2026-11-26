export function composerField(host: ParentNode): HTMLTextAreaElement | HTMLDivElement {
  return host.querySelector("[data-composer-input]")!;
}

export function composerFieldText(host: ParentNode): string {
  const field = composerField(host);
  return field instanceof HTMLTextAreaElement ? field.value : field.textContent ?? "";
}

/** jsdom has no native contenteditable typing or paste. Drive the same input or clipboard event as the browser. */
export function changeComposerField(field: HTMLElement, text: string, options: { append?: boolean; caret?: number } = {}) {
  field.focus();
  if (field instanceof HTMLTextAreaElement) {
    const value = options.append ? field.value + text : text;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(field, value);
    const at = options.caret ?? value.length;
    field.setSelectionRange(at, at);
    field.dispatchEvent(new Event("input", { bubbles: true }));
    return;
  }
  const range = field.ownerDocument.createRange();
  range.selectNodeContents(field);
  if (options.append) range.collapse(false);
  const selection = field.ownerDocument.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", { value: { files: [], getData: (format: string) => format === "text/plain" ? text : "" } });
  field.dispatchEvent(event);
}
