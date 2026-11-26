package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	ownerActionPrepareTool = "owner_action_prepare"
	ownerActionBatchTool   = "owner_action_batch"
	// A proposal must fit in one Feishu card without safeCard replacing its
	// contents. Larger operations retain the ordinary approval flow.
	ownerActionMaxDisplay = 12000
)

func (s *Service) ownerActions() (*ownerActionBook, error) {
	book, ok := newOwnerActionBook(s.store())
	if !ok {
		return nil, errors.New("exact owner-action approval needs a persistent revision-aware database")
	}
	return book, nil
}

func ownerActionInteractionID(batchID, itemID string) string {
	if itemID != "" {
		return "ix_owner_item_" + strings.TrimPrefix(itemID, "oai_")
	}
	return "ix_owner_batch_" + strings.TrimPrefix(batchID, "oab_")
}

func ownerActionProposal(ix proto.Interaction) bool {
	if !ix.OwnerProposal || ix.Kind != proto.IxPermission || ix.ApprovalBatchID == "" {
		return false
	}
	if ix.ApprovalItemID != "" {
		return ix.ID == ownerActionInteractionID(ix.ApprovalBatchID, ix.ApprovalItemID) && ix.ToolName != ownerActionBatchTool
	}
	return ix.ID == ownerActionInteractionID(ix.ApprovalBatchID, "") && ix.ToolName == ownerActionBatchTool
}

func persistentOwnerProposal(ix proto.Interaction) bool {
	return ownerActionProposal(ix) && ix.Resumable && ix.NoExpiry
}

func ownerActionDisplay(item ownerActionItem) (string, error) {
	if len(item.Canonical) > ownerActionMaxDisplay {
		return "", errors.New("owner-action item is too large to display in full; use its ordinary approval card")
	}
	// RedactArgs is the existing channel privacy boundary. Secret values still
	// participate in the fingerprint, so changing one requires fresh approval,
	// but they are not copied into a chat card.
	redacted := proto.RedactArgs(json.RawMessage(item.Canonical))
	if redacted == "" {
		return "", errors.New("owner-action input cannot be displayed")
	}
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(redacted), "", "  "); err != nil {
		return "", err
	}
	if out.Len() > ownerActionMaxDisplay {
		return "", errors.New("owner-action item is too large to display in full; use its ordinary approval card")
	}
	return out.String(), nil
}

func ownerActionItemDisplay(item ownerActionItem) (string, error) {
	arguments, err := ownerActionDisplay(item)
	if err != nil {
		return "", err
	}
	if item.Display == "" {
		return arguments, nil
	}
	text := item.Display + "\n\nCanonical arguments:\n" + arguments
	if len(text) > ownerActionMaxDisplay {
		return "", errors.New("built-in before/after and canonical arguments are too large to display in full")
	}
	return text, nil
}

func (s *Service) toolOwnerActionPrepare(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	cfg := s.settings(ctx)
	persistent := persistentOwnerActionCardsOn(cfg)
	if !ownerReplyApprovalOn(cfg) {
		return "", errors.New("exact owner-reply approval is disabled in Settings")
	}
	var in struct {
		Items []struct {
			Tool  string          `json:"tool"`
			Input json.RawMessage `json:"input"`
		} `json:"items"`
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return "", err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("owner_action_prepare accepts exactly one JSON object")
	}
	drafts := make([]ownerActionDraft, 0, len(in.Items))
	ctx = withOwnerProfilePlan(ctx)
	for _, item := range in.Items {
		canonical, err := canonicalOwnerAction(item.Input)
		if err != nil {
			return "", err
		}
		if len(canonical) > ownerActionMaxDisplay {
			return "", errors.New("an item is too large to display in full; call it directly for its ordinary approval card")
		}
		draft := ownerActionDraft{Tool: item.Tool, Input: canonical}
		if ownerBuiltinWrite(item.Tool) {
			prepared, err := s.prepareOwnerBuiltin(ctx, machine, self, item.Tool, canonical)
			if err != nil {
				return "", err
			}
			draft.Display = prepared.display
			if prepared.revision != nil {
				expected := *prepared.revision
				draft.Revision = &expected
			}
			if err := advanceOwnerProfilePlan(ctx, prepared); err != nil {
				return "", err
			}
		} else {
			if s.ext == nil {
				return "", errors.New("connector tools are unavailable")
			}
			known, readOnly := s.ext.ToolAccess(item.Tool)
			if !known || readOnly || !s.ext.ConnectorTool(item.Tool) {
				return "", fmt.Errorf("%s is not a known side-effecting connector or supported built-in write", item.Tool)
			}
		}
		drafts = append(drafts, draft)
	}
	book, err := s.ownerActions()
	if err != nil {
		return "", err
	}
	batch, err := book.createPersistent(ctx, machine, self, drafts, persistent)
	if err != nil {
		return "", err
	}
	for _, item := range batch.Items {
		if _, err := ownerActionItemDisplay(item); err != nil {
			_ = book.cancelPending(context.Background(), batch.ID)
			return "", err
		}
	}
	phrases, err := ownerApprovalPhrases(cfg)
	if err != nil {
		_ = book.cancelPending(context.Background(), batch.ID)
		return "", err
	}
	batchSummary := i18n.T(s.lang(ctx), "assistant-owner_actions.batch", i18n.Args{"count": len(batch.Items), "phrases": strings.Join(phrases, " / "), "id": batch.ID})
	var builtinDetails strings.Builder
	for i, item := range batch.Items {
		if item.Display != "" {
			fmt.Fprintf(&builtinDetails, "\n\nItem %d/%d · %s\n%s", i+1, len(batch.Items), item.Tool, item.Display)
		}
	}
	batchSummary += builtinDetails.String()
	if len(batchSummary) > ownerActionMaxDisplay {
		_ = book.cancelPending(context.Background(), batch.ID)
		return "", errors.New("the batch's complete built-in before/after display is too large; split it into smaller batches")
	}
	for _, item := range batch.Items {
		display, _ := ownerActionItemDisplay(item)
		ix := proto.Interaction{
			ID:                   ownerActionInteractionID(batch.ID, item.ID),
			MachineID:            machine,
			SessionID:            self,
			Kind:                 proto.IxPermission,
			CreatedAt:            batch.CreatedAt,
			ToolName:             item.Tool,
			ToolInput:            json.RawMessage(item.Canonical),
			Summary:              display,
			ApprovalBatchID:      batch.ID,
			ApprovalItemID:       item.ID,
			OwnerProposal:        true,
			OwnerApprovalPhrases: phrases,
			Resumable:            true,
			NoExpiry:             persistent,
		}
		notKept := false
		ix.AlwaysHonored = &notKept
		// Item proposals have no process-local Apply closure. Their waiter only
		// owns timeout cleanup; approval and execution live in the durable book.
		ready := make(chan struct{})
		go func() { _, _ = s.waitInteractionReady(context.Background(), ix, nil, ready) }()
		<-ready
	}
	batchIX := proto.Interaction{
		ID:                   ownerActionInteractionID(batch.ID, ""),
		MachineID:            machine,
		SessionID:            self,
		Kind:                 proto.IxPermission,
		CreatedAt:            batch.CreatedAt,
		ToolName:             ownerActionBatchTool,
		Summary:              batchSummary,
		ApprovalBatchID:      batch.ID,
		OwnerProposal:        true,
		OwnerApprovalPhrases: phrases,
		Resumable:            true,
		NoExpiry:             persistent,
	}
	notKept := false
	batchIX.AlwaysHonored = &notKept
	decided := false
	defer func() {
		if !decided && !persistent {
			_ = book.cancelPending(context.Background(), batch.ID)
			_ = s.syncOwnerActionBatch(context.Background(), batch.ID)
		}
	}()
	syncCtx := context.WithoutCancel(ctx)
	resolution, err := s.waitOwnerActionBatch(ctx, batchIX)
	if err == nil && !resolution.Dismiss {
		if err := s.syncOwnerActionBatch(syncCtx, batch.ID); err != nil && !errors.Is(err, errNotPending) {
			return "", err
		}
	}
	if err != nil {
		return "", err
	}
	if resolution.Dismiss {
		if persistent {
			return "The exact approval batch is still pending in the original conversation and channels. Do not execute any item until its approval arrives.", nil
		}
		return "The exact approval batch closed unanswered. Nothing was approved or executed.", nil
	}
	if resolution.Allow == nil {
		return "", errors.New("exact owner-action proposals accept only Allow or Deny")
	}
	decided = true
	current, found, err := book.batch(ctx, batch.ID)
	if err != nil || !found {
		return "", errors.New("the approved batch could not be read back")
	}
	if !*resolution.Allow {
		return "The owner denied this exact batch. Do not execute any of its items.", nil
	}
	var out strings.Builder
	out.WriteString("The owner approved the following exact operations. Call each listed tool exactly once in item order with the canonical arguments below, without edits, and stop at the first error. Built-in calls consume their one-shot grants and report their item position and partial result. Any other call needs its own approval. Do not retry an item after an uncertain external result.\n")
	approved := 0
	for index, item := range current.Items {
		if item.State != ownerActionGranted && item.State != ownerActionConsumed {
			continue
		}
		approved++
		fmt.Fprintf(&out, "\nItem %d/%d · %s\nTool: %s\nArguments: %s\n", index+1, len(current.Items), item.ID, item.Tool, item.Canonical)
	}
	if approved == 0 {
		return "The batch has no remaining approved items. Execute nothing.", nil
	}
	return out.String(), nil
}

// Both waiters observe the same published card. A quick reply can retire it
// before the original tool caller resumes; publishing its ID again would lose that reply.
func (s *Service) waitOwnerActionBatch(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	type result struct {
		resolution proto.Resolution
		err        error
	}
	registered := make(chan *pending, 1)
	finished := make(chan result, 1)
	go func() {
		r, err := s.waitRegisteredInteraction(context.Background(), ix, nil, func(p *pending) { registered <- p })
		finished <- result{r, err}
	}()
	select {
	case outcome := <-finished:
		return outcome.resolution, outcome.err
	case p := <-registered:
		// An item decision may already be durable when the batch card is first published.
		err := s.syncOwnerActionBatch(context.WithoutCancel(ctx), ix.ApprovalBatchID)
		if err != nil && !errors.Is(err, errNotPending) {
			return proto.Resolution{}, err
		}
		return s.await(ctx, p)
	}
}

func ownerActionCardProof(ix proto.Interaction, source string) ownerReplyProof {
	return ownerReplyProof{
		AuthenticatedOwner: true,
		DirectParent:       true, // a press is bound to this exact card, stronger than a text parent
		ExactPhrase:        true, // Allow/Deny is an explicit typed answer
		EventID:            "card:" + source + ":" + ix.ID,
		BatchID:            ix.ApprovalBatchID,
		ItemID:             ix.ApprovalItemID,
	}
}

func (s *Service) resolveOwnerProposal(ctx context.Context, ix proto.Interaction, r proto.Resolution, proof ownerReplyProof) error {
	if !ownerActionProposal(ix) || r.Allow == nil || r.Always || r.EditedText != "" || len(r.Values) > 0 {
		return errOwnerActionProof
	}
	proof.BatchID, proof.ItemID = ix.ApprovalBatchID, ix.ApprovalItemID
	book, err := s.ownerActions()
	if err != nil {
		return err
	}
	if _, err := book.decide(ctx, proof, *r.Allow); err != nil {
		return err
	}
	return s.syncOwnerActionBatch(ctx, ix.ApprovalBatchID)
}

// syncOwnerActionBatch projects durable item decisions onto the original
// cards. It never executes an operation and is safe to call after a restart or
// a duplicate reply.
func (s *Service) syncOwnerActionBatch(ctx context.Context, batchID string) error {
	book, err := s.ownerActions()
	if err != nil {
		return err
	}
	batch, found, err := book.batch(ctx, batchID)
	if err != nil {
		return err
	}
	if !found {
		return errOwnerActionProof
	}
	result := ownerActionDecisionResult{}
	ownerActionFillResult(batch, &result)
	for _, item := range batch.Items {
		if item.State == ownerActionPending {
			continue
		}
		allow := item.State == ownerActionGranted || item.State == ownerActionConsumed
		r := proto.Resolution{Allow: &allow, Reason: "exact owner-action decision " + batch.ID}
		id := ownerActionInteractionID(batch.ID, item.ID)
		if _, pending := s.pendingIx(id); !pending {
			continue
		}
		if err := s.resolveAnswer(ctx, id, r, true, nil, nil); err != nil && !errors.Is(err, errNotPending) {
			return err
		}
	}
	if result.Complete {
		r := proto.Resolution{Allow: &result.Allow, Reason: "exact owner-action decision " + batch.ID}
		id := ownerActionInteractionID(batch.ID, "")
		if _, pending := s.pendingIx(id); pending {
			if err := s.resolveAnswer(ctx, id, r, true, nil, nil); err != nil && !errors.Is(err, errNotPending) {
				return err
			}
		}
	}
	return nil
}
