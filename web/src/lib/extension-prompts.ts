// Pure presentation of declarative quick prompts: selecting a row fills the composer, never sends it.
export interface QuickPrompt {
  extension: string;
  extension_name: string;
  id: string;
  title: string;
  text: string;
}
export interface ComposerCommand {
  word: string;
  desc: string;
  text: string;
  confirm?: string;
  key?: string;
  /** The small label at the right of its row in the "/" palette, where it comes from: the agent's name for its own slash
   *  commands (agentCommands), the extension's name for a quick prompt; none for Persona's own. */
  source?: string;
  /** One of the agent's own slash commands, typed into it as it is: agentCommands names its source. */
  agent?: boolean;
}
export function promptCommands(prompts: QuickPrompt[]): ComposerCommand[] {
  return prompts.map((p) => ({
    key: `extension:${p.extension}/${p.id}`,
    word: `/${p.title.replace(/\s+/g, "")}`,
    // its row names the extension; the palette's card shows what it types
    desc: "",
    text: p.text,
    source: p.extension_name,
  }));
}
export const EXTENSIONS_CHANGED = "persona:extensions-changed";
