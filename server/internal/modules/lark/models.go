package lark

import (
	"context"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// botModels uses the main conversation's persistent model pick, shared with the apps. What it reads and changes is the
// core's, asked for the assistant (its launcher purpose).
func (s core) botModels(ctx context.Context, f FeishuFeatures, messageID, cmd, model, root, parent string) {
	l := s.botLang(ctx)
	note := func(key string, args ...i18n.Args) {
		s.botNote(ctx, messageID, i18n.T(l, "assistant-feishu_cards."+key, args...))
	}
	if !f.Main {
		note("main_off")
		return
	}
	main, machine := s.h.MainChat(ctx)
	t := s.botTargetFor(root, parent)
	if t.Kind != targetMain && !(t.Kind == targetPersona && main != "" && t.SessionID == main && (t.MachineID == "" || t.MachineID == machine)) {
		note("model_main_only")
		return
	}
	if main == "" {
		note("model_no_main")
		return
	}
	if cmd == "/modelchange" && model == "" {
		note("model_change_usage")
		return
	}
	launcher, current, effort, err := s.h.MainModel(ctx, machine, main, l)
	if err != nil {
		note("model_error", i18n.Args{"error": err.Error()})
		return
	}
	res, err := s.h.LauncherModels(ctx, machine, launcher)
	if err != nil {
		note("model_error", i18n.Args{"error": err.Error()})
		return
	}
	if cmd == "/modelchange" {
		if !s.h.ListsModel(res, model) {
			note("model_change_unknown", i18n.Args{"model": model})
			return
		}
		err = s.h.PickModel(ctx, main, machine, proto.ModelPick{Launcher: launcher, Model: model, Effort: effort}, l)
		if err != nil {
			note("model_error", i18n.Args{"error": err.Error()})
			return
		}
		note("model_change_done", i18n.Args{"model": model})
		return
	}
	var b strings.Builder
	b.WriteString(i18n.T(l, "assistant-feishu_cards.model_list_title"))
	fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_launcher", i18n.Args{"launcher": launcher}))
	if current != "" {
		fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_current", i18n.Args{"model": current}))
	}
	if len(res.Models) == 0 {
		fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_empty"))
	} else {
		for _, m := range res.Models {
			if m.ID == "" {
				continue
			}
			line := "\n• " + m.ID
			if b.Len()+len(line) > 16000 {
				fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_truncated"))
				break
			}
			b.WriteString(line)
		}
	}
	fmt.Fprintf(&b, "\n%s", i18n.T(l, "assistant-feishu_cards.model_list_hint"))
	s.botNote(ctx, messageID, b.String())
}
