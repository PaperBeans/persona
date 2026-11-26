import { t } from "../../i18n";
import { Button } from "../../ui/atoms";

export default function TranscriptWindowPager({ direction, loading, disabled, onClick }: {
  direction: "before" | "after";
  loading: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  const earlier = direction === "before";
  return (
    <div className="flex justify-center">
      <Button data-context-page={direction} disabled={loading || disabled} onClick={onClick}>
        {loading ? t("chat-navrail.loading_target") : t(earlier ? "chat-navrail.load_earlier" : "chat-navrail.load_later")}
      </Button>
    </div>
  );
}
