import { t } from "../../i18n";
import { shortPath } from "../../lib/format";
import { Icon } from "../../ui/icons";

export interface ChatEmptyStateProps {
  assistant?: boolean;
  personaName?: string;
  cwd?: string;
  canCompose?: boolean;
  onPick: (prompt: string) => void;
}

export function ChatEmptyState({
  assistant,
  personaName,
  cwd,
  canCompose,
  onPick,
}: ChatEmptyStateProps) {
  const title = assistant
    ? t("chat-emptystate.ask_persona", { name: personaName?.trim() || "Persona" })
    : t("chat-emptystate.new_chat");
  const shortenedCwd = !assistant && cwd ? shortPath(cwd) : "";
  const subtitle = shortenedCwd || t("chat-emptystate.subtitle");
  const starters = assistant
    ? [
        t("chat-emptystate.assistant_starter_todos"),
        t("chat-emptystate.assistant_starter_messages"),
        t("chat-emptystate.assistant_starter_tasks"),
      ]
    : [
        t("chat-emptystate.agent_starter_repo"),
        t("chat-emptystate.agent_starter_git"),
        t("chat-emptystate.agent_starter_tests"),
      ];

  return (
    <div className="my-auto flex flex-col items-center justify-center py-14 text-center">
      <div className="size-10 rounded-[12px] bg-inset border border-line flex items-center justify-center text-ink-2 mb-3">
        <Icon name={assistant ? "sparkles" : "chat"} size={18} />
      </div>
      <h2 className="text-[length:var(--chat-body)] font-semibold text-ink">{title}</h2>
      <p
        className={`text-xs text-ink-3 mt-1 max-w-[440px] ${shortenedCwd ? "font-mono truncate" : ""}`}
        title={shortenedCwd ? cwd : undefined}
      >
        {subtitle}
      </p>
      {canCompose && (
        <div className="flex flex-wrap justify-center gap-2 mt-5 max-w-[520px]">
          {starters.map((prompt) => (
            <button
              key={prompt}
              type="button"
              onClick={() => onPick(prompt)}
              className="rounded-full border border-line bg-surface px-3 py-1.5 text-xs text-ink-2 shadow-hairline transition-colors hover:bg-hover hover:text-ink whitespace-nowrap cursor-pointer"
            >
              {prompt}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
