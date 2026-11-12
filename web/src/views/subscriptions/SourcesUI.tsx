// The manager's subscription pages (docs/extensions/subscriptions.md "Pages") as Settings › Inbox Sources and
// /subscriptions draw them: the declarative renderer under the reserved id, told the assistant's name as the composer
// shows it ("Ask {name}…"), which the pages' Set Up with … button takes.
import { useMemo } from "react";
import { SUBSCRIPTIONS_UI } from "../../lib/subscriptions-ui";
import { usePersona } from "../assistant/Persona";
import { ExtensionUI } from "../settings/ExtensionView";

export function SourcesUI({ title, state }: { title: string; state?: string }) {
  const name = usePersona().name;
  const context = useMemo(() => ({ assistant: name }), [name]);
  return <ExtensionUI extension={SUBSCRIPTIONS_UI} ui="pages" title={title} bare state={state} context={context} />;
}
