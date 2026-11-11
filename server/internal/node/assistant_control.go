package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const assistantControlTimeout = 15 * time.Second

// assistantControlUnconfirmed keeps transport failures distinguishable from Claude's explicit refusal,
// while preserving the coded error through RPC. A failed initial launch can still use its startup retries.
type assistantControlUnconfirmed struct{ error }

func (e *assistantControlUnconfirmed) Unwrap() error { return e.error }

type assistantControlResponse struct {
	RequestID string          `json:"request_id"`
	Subtype   string          `json:"subtype"`
	Error     string          `json:"error"`
	Response  json.RawMessage `json:"response"` // what a request answers besides success (cancel_async_message's cancelled)
}

// control waits for this request's verdict, never treating a successful stdin write as acceptance.
func (p *assistantProc) control(ctx context.Context, request map[string]any) error {
	_, err := p.controlAnswer(ctx, request)
	return err
}

// controlAnswer is control with the answer Claude gave, null when it gave none.
func (p *assistantProc) controlAnswer(ctx context.Context, request map[string]any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, assistantControlTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id := fmt.Sprintf("persona-%d", p.reqSeq.Add(1))
	result := make(chan assistantControlResponse, 1)
	p.mu.Lock()
	if p.controls == nil {
		p.controls = map[string]chan assistantControlResponse{}
	}
	p.controls[id] = result
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.controls, id)
		p.mu.Unlock()
	}()
	line, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": request})
	// A child that stops reading stdin must not hold the settings RPC past its deadline.
	// The caller retires the process on transport failure, closing stdin and releasing this writer.
	written := make(chan error, 1)
	go func() { written <- p.write(line) }()
	select {
	case err := <-written:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.exited:
		return nil, io.EOF
	}
	select {
	case response := <-result:
		if response.Subtype == "success" {
			return response.Response, nil
		}
		return nil, refusal("assistant_control_failed", response.Error, nil)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.exited:
		return nil, io.EOF
	}
}

func (p *assistantProc) controlResponse(response assistantControlResponse) {
	if response.Subtype != "success" && response.Subtype != "error" {
		return
	}
	p.mu.Lock()
	result := p.controls[response.RequestID]
	delete(p.controls, response.RequestID)
	p.mu.Unlock()
	if result != nil {
		result <- response
	}
}
