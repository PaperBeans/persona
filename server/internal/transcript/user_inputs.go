package transcript

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// UserInputPositions groups sorted items by input entry and extracts excerpts within each exchange.
func UserInputPositions(items []proto.Item) []proto.UserInputPosition {
	return projectInputPreviews(items, nil)
}

// UserInputPreviewsFor projects only the excerpts for the selected positions in a bounded window.
func UserInputPreviewsFor(items []proto.Item, positions []proto.UserInputRef) []proto.UserInputPosition {
	selected := make(map[proto.UserInputRef]bool, len(positions))
	for _, pos := range positions {
		selected[pos] = true
	}
	return projectInputPreviews(items, selected)
}

type inputGroup struct {
	position      proto.UserInputPosition
	start         int
	present       bool
	hasBody       bool
	excluded      bool
	otherExchange bool
}

func inputStem(id string) string {
	stem, _, _ := strings.Cut(id, "#")
	return stem
}

func inputGroups(items []proto.Item, excerpts func(proto.UserInputRef) bool) []inputGroup {
	groups := map[string]int{}
	var order []inputGroup
	for i, it := range items {
		if it.Role != "user" || (it.Kind != proto.KindText && it.Kind != proto.KindImage) || it.ToolUseID != "" {
			continue
		}
		id := inputStem(it.ID)
		at, found := groups[id]
		if !found {
			at = len(order)
			groups[id] = at
			order = append(order, inputGroup{start: i, position: proto.UserInputPosition{ID: it.ID, Seq: it.Seq}})
		}
		g := &order[at]
		extract := excerpts != nil && excerpts(g.position.Ref())
		ignore, otherExchange := inputIndexOrigin(it)
		g.excluded = g.excluded || ignore
		g.otherExchange = g.otherExchange || otherExchange
		switch {
		case it.Kind == proto.KindImage:
			g.present = true
			if extract {
				g.position.Attachments++
			}
		case !ignore:
			body, present := inputIndexText(it.Text)
			var attachments int
			if extract || strings.Contains(body, "/loop") {
				body, attachments = inputIndexBody(body)
			}
			if strings.HasPrefix(body, "/loop") {
				g.excluded = true
				g.otherExchange = g.otherExchange || !it.Queued
				continue
			}
			g.present = g.present || present
			g.hasBody = g.hasBody || body != ""
			if extract {
				if g.position.Preview == "" {
					g.position.Preview = inputIndexPreview(body)
				}
				g.position.Attachments += attachments
			}
		}
	}
	return order
}

func projectInputPreviews(items []proto.Item, selected map[proto.UserInputRef]bool) []proto.UserInputPosition {
	excerpt := func(pos proto.UserInputRef) bool { return selected == nil || selected[pos] }
	order := inputGroups(items, excerpt)
	out := make([]proto.UserInputPosition, 0, len(order))
	end := len(items)
	// Classify all blocks first. An image can precede the text that marks its entry as automated.
	for i := len(order) - 1; i >= 0; i-- {
		g := &order[i]
		if g.present && !g.excluded {
			if excerpt(g.position.Ref()) {
				g.position.Answer = inputIndexAnswer(items[g.start+1 : end])
				if g.hasBody {
					g.position.Attachments = 0
				}
				out = append(out, g.position)
			}
			end = g.start
		} else if g.otherExchange {
			end = g.start
		}
	}
	slices.Reverse(out)
	return out
}

// CompactInputIndex accumulates positions from disjoint, time-ordered pages in either direction.
// It keeps only input membership and position, including repeated entries whose blocks share an ID stem.
type CompactInputIndex struct {
	groups map[string]inputGroup
	rank   int
}

func (b *CompactInputIndex) Add(items []proto.Item) {
	for _, group := range inputGroups(items, nil) {
		b.add(group)
	}
}

func (b *CompactInputIndex) add(group inputGroup) {
	if b.groups == nil {
		b.groups = map[string]inputGroup{}
	}
	key := inputStem(group.position.ID)
	group.start = b.rank
	b.rank++
	if prior, ok := b.groups[key]; ok {
		group.present = group.present || prior.present
		group.excluded = group.excluded || prior.excluded
		if prior.position.Seq <= group.position.Seq {
			group.position, group.start = prior.position, prior.start
		}
	}
	b.groups[key] = group
}

func (b *CompactInputIndex) Index(history string) proto.UserInputIndex {
	groups := make([]inputGroup, 0, len(b.groups))
	for _, g := range b.groups {
		if g.present && !g.excluded {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		a, b := groups[i], groups[j]
		if a.position.Seq == b.position.Seq {
			return a.start < b.start
		}
		return a.position.Seq < b.position.Seq
	})
	out := proto.UserInputIndex{Total: len(groups), Inputs: make([]proto.UserInputPosition, 0, len(groups)), History: history}
	for _, g := range groups {
		out.Inputs = append(out.Inputs, g.position)
	}
	return out
}

func inputIndexOrigin(it proto.Item) (ignore, otherExchange bool) {
	source, injected := prompts.ItemSource(it), it.Injected
	if injected == nil && (source == nil || source.Kind == proto.SourceFollowup) && it.ClientMsgID == "" &&
		it.Kind == proto.KindText && it.From == "" {
		injected = prompts.ParseInjected(it.Text)
	}
	ignore = it.From != "" || it.Wake || it.SupervisionUser || source != nil || injected != nil
	otherExchange = ignore && !it.Queued && !prompts.ContinuesOwnWork(injected) &&
		(source == nil || source.Kind != proto.SourceAutoContinue)
	return
}

// UserInputs reads the complete existing history, including a cold file's older windows. A partial scan never
// masquerades as the total. A positive before sequence restricts the projection to earlier source lines.
func (f *File) UserInputs(ctx context.Context, before int) (proto.UserInputIndex, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.base > 0 {
		if err := ctx.Err(); err != nil {
			return proto.UserInputIndex{}, err
		}
		if f.loadOlder() == 0 {
			return proto.UserInputIndex{}, errors.New("user input history is incomplete")
		}
	}
	if err := ctx.Err(); err != nil {
		return proto.UserInputIndex{}, err
	}
	positions := UserInputPositions(f.items[:f.before(before)])
	return proto.UserInputIndex{Total: len(positions), Inputs: positions, History: f.history()}, nil
}
