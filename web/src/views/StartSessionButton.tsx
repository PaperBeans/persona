import { t } from "../i18n";
import { openNewAgent } from "../lib/new-agent";
import { Button, IconButton } from "../ui/atoms";

export function StartSessionButton({ compact, className }: { compact?: boolean; className?: string }) {
  const label = t("personaassistant.start_work_session");
  const open = () => openNewAgent({ workSession: true });
  return compact
    ? <IconButton icon="plus" label={label} onClick={open} className={className} />
    : <Button icon="plus" onClick={open} className={className}>{label}</Button>;
}
