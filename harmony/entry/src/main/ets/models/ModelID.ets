// PersonaWire.ModelID and proto.ValidModelID use ASCII identifiers. Every accepted character is one UTF-8 byte.
export function modelIDProblem(id: string, word: (key: string) => string): string {
  if (!id) return word('flow_model_id_empty');
  for (let i = 0; i < id.length; i++) {
    const ch = id.charAt(i);
    if (/[a-zA-Z0-9]/.test(ch) || (i === 0 && ch === '~' && /[a-zA-Z0-9]/.test(id.charAt(1))) ||
      (i > 0 && '._-/:@[]'.includes(ch))) continue;
    if (ch === ' ' || ch === '\t') return word('flow_model_id_space');
    if (id.charCodeAt(i) < 32 || id.charCodeAt(i) === 127) return word('flow_model_id_control');
    if (i === 0) return word('flow_model_id_start');
    return word('flow_model_id_character').replace('{character}', String.fromCodePoint(id.codePointAt(i) || 0));
  }
  return id.length > 256 ? word('flow_model_id_length') : '';
}
