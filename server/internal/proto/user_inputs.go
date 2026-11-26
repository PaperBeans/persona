package proto

import (
	"math"
	"unicode/utf8"
)

const (
	MethodSessionInputPreviews = "session.input_previews"
	MethodSessionItemsWindow   = "session.items_window"
	CodeNavigationChanged      = "history_changed"

	MaxInputPreviews     = 16
	InputPreviewLines    = 128
	InputPreviewRunes    = 100
	InputAnswerRunes     = 180
	DefaultWindowRadius  = 40
	MaxWindowRadius      = 100
	MaxNavigationSeq     = math.MaxInt32 - 1
	MaxNavigationID      = 512
	MaxNavigationHistory = 256
)

// UserInputItemID is the opaque item identity carried by a navigation position.
type UserInputItemID string

// NavigationSessionID identifies the session that owns a navigation request.
type NavigationSessionID string

// UserInputRef identifies a particular input, including inputs sharing a source line.
type UserInputRef struct {
	ID  UserInputItemID `json:"id"`
	Seq int             `json:"seq"`
}

// UserInputPosition identifies one genuine input and carries optional excerpts for navigation.
type UserInputPosition struct {
	ID          string `json:"id"`
	Seq         int    `json:"seq"`
	Preview     string `json:"preview,omitempty"`
	Answer      string `json:"answer,omitempty"`
	Attachments int    `json:"attachments,omitempty"`
}

func (p UserInputPosition) Ref() UserInputRef {
	return UserInputRef{ID: UserInputItemID(p.ID), Seq: p.Seq}
}

func (p UserInputPosition) WithoutExcerpts() UserInputPosition {
	return UserInputPosition{ID: p.ID, Seq: p.Seq}
}

// UserInputIndex is the temporary web count/position contract. The richer #2071 API supersedes it when merged.
type UserInputIndex struct {
	Total   int                 `json:"total"`
	Inputs  []UserInputPosition `json:"inputs"`
	History string              `json:"history"`
}

// InputPreviewRequest reads exact positions in a source. BeforeSeq is the exclusive end of a frozen segment.
type InputPreviewRequest struct {
	SessionID NavigationSessionID `json:"session_id"`
	Positions []UserInputRef      `json:"positions"`
	BeforeSeq int                 `json:"before_seq,omitempty"`
	History   string              `json:"history,omitempty"`
}

func (p InputPreviewRequest) Valid() bool {
	if len(p.Positions) == 0 || len(p.Positions) > MaxInputPreviews ||
		p.BeforeSeq < 0 || p.BeforeSeq > MaxNavigationSeq+1 || len(p.History) > MaxNavigationHistory {
		return false
	}
	seen := make(map[UserInputRef]bool, len(p.Positions))
	for _, pos := range p.Positions {
		if pos.Seq <= 0 || pos.Seq > MaxNavigationSeq || len(pos.ID) == 0 || len(pos.ID) > MaxNavigationID || seen[pos] {
			return false
		}
		seen[pos] = true
	}
	return true
}

func (p InputPreviewRequest) Through(seq int) int {
	end := seq + min(InputPreviewLines-1, MaxNavigationSeq-seq)
	if p.BeforeSeq > 0 {
		end = min(end, p.BeforeSeq-1)
	}
	return end
}

type InputPreviews struct {
	Inputs  []UserInputPosition `json:"inputs"`
	History string              `json:"history"`
}

func (p InputPreviews) Matches(request InputPreviewRequest) bool {
	if p.Inputs == nil || len(p.Inputs) > len(request.Positions) {
		return false
	}
	wanted := make(map[UserInputRef]bool, len(request.Positions))
	for _, ref := range request.Positions {
		wanted[ref] = request.BeforeSeq == 0 || ref.Seq < request.BeforeSeq
	}
	for _, pos := range p.Inputs {
		if !wanted[pos.Ref()] || pos.Attachments < 0 ||
			utf8.RuneCountInString(pos.Preview) > InputPreviewRunes || utf8.RuneCountInString(pos.Answer) > InputAnswerRunes {
			return false
		}
		delete(wanted, pos.Ref())
	}
	return true
}

// ItemsWindowRequest describes a closed source-line interval without walking from the newest page.
type ItemsWindowRequest struct {
	SessionID  NavigationSessionID `json:"session_id"`
	FromSeq    int                 `json:"from_seq"`
	ThroughSeq int                 `json:"through_seq"`
	BeforeSeq  int                 `json:"before_seq,omitempty"` // frozen end plus one, including for runtime history probes
	History    string              `json:"history,omitempty"`
}

func (p ItemsWindowRequest) Valid() bool {
	return p.FromSeq > 0 && p.ThroughSeq >= p.FromSeq && p.ThroughSeq <= MaxNavigationSeq &&
		p.ThroughSeq-p.FromSeq <= 2*MaxWindowRadius &&
		(p.BeforeSeq == 0 || (p.BeforeSeq > p.ThroughSeq && p.BeforeSeq <= MaxNavigationSeq+1)) &&
		len(p.History) <= MaxNavigationHistory
}

// ItemsWindow covers (FromSeq, ToSeq], including source lines that produced no items.
type ItemsWindow struct {
	Items     []Item `json:"items"`
	History   string `json:"history"`
	FromSeq   int    `json:"from_seq"`
	ToSeq     int    `json:"to_seq"`
	HasMore   bool   `json:"has_more"`
	MoreAfter bool   `json:"more_after"`
}

func (w ItemsWindow) Matches(request ItemsWindowRequest) bool {
	if w.Items == nil || w.FromSeq < 0 || w.ToSeq < w.FromSeq || w.ToSeq > request.ThroughSeq ||
		w.FromSeq > request.FromSeq-1 || (w.FromSeq < request.FromSeq-1 && w.FromSeq != w.ToSeq) {
		return false
	}
	last := w.FromSeq
	for _, it := range w.Items {
		if it.Seq <= w.FromSeq || it.Seq < last || it.Seq > w.ToSeq {
			return false
		}
		last = it.Seq
	}
	return true
}
