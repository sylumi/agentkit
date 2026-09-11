package model

import "fmt"

// StopReason identifies the confirmed terminal outcome of one generation.
// Transport failures, cancellation, and invalid responses are call errors.
type StopReason string

const (
	StopReasonStop      StopReason = "stop"
	StopReasonToolCalls StopReason = "tool_calls"
	StopReasonLength    StopReason = "length"
	StopReasonBlocked   StopReason = "blocked"
)

// Usage contains known token counts for one generation, without summing retries.
// Each nil counter is unknown; a pointer to zero is a known zero count.
// InputTokens includes CachedInputTokens; OutputTokens includes
// ReasoningOutputTokens. Detail counts must not be added to these totals.
type Usage struct {
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	CachedInputTokens     *int64 `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens,omitempty"`
}

// ResponseMetadata identifies the provider response, not the HTTP request.
// Model is the model name returned by the service; empty fields are unknown.
// ResponseID does not enable server-side conversation continuation.
type ResponseMetadata struct {
	Provider   string `json:"provider,omitempty"`
	ResponseID string `json:"response_id,omitempty"`
	Model      string `json:"model,omitempty"`
}

// Result contains the complete content blocks of a confirmed generation outcome.
// A nil Message means no complete output blocks; it differs from empty text.
// Length and blocked outcomes may contain complete calls, but do not authorize
// their execution. Producers must stop modifying nested data after delivery.
// Message may include thinking without answer text.
// A non-nil Message must have RoleAssistant. Standard JSON decoding does not
// enforce this or other result constraints; call Validate after decoding.
type Result struct {
	Message    *Message          `json:"message,omitempty"`
	StopReason StopReason        `json:"stop_reason"`
	Usage      Usage             `json:"usage"`
	Metadata   *ResponseMetadata `json:"metadata,omitempty"`
}

// Validate checks known counts without filling unknown values or computing totals.
func (u Usage) Validate() error {
	if u.InputTokens != nil && *u.InputTokens < 0 {
		return fmt.Errorf("input_tokens: must be non-negative")
	}
	if u.OutputTokens != nil && *u.OutputTokens < 0 {
		return fmt.Errorf("output_tokens: must be non-negative")
	}
	for _, detail := range []struct {
		name         string
		value, total *int64
	}{
		{"cached_input_tokens", u.CachedInputTokens, u.InputTokens},
		{"reasoning_output_tokens", u.ReasoningOutputTokens, u.OutputTokens},
	} {
		if detail.value != nil {
			if *detail.value < 0 {
				return fmt.Errorf("%s: must be non-negative", detail.name)
			}
			if detail.total != nil && *detail.value > *detail.total {
				return fmt.Errorf("%s: must not exceed its total", detail.name)
			}
		}
	}
	return nil
}

// Validate checks the reason, message, their combination, and usage in that order.
// It is read-only and preserves nested error paths and causes. It cannot confirm
// a provider's terminal signal or authorize tool execution.
func (r Result) Validate() error {
	switch r.StopReason {
	case StopReasonStop, StopReasonToolCalls, StopReasonLength, StopReasonBlocked:
	default:
		return fmt.Errorf("stop_reason: unknown stop reason %q", r.StopReason)
	}

	hasToolCall := false
	if r.Message != nil {
		if r.Message.Role != RoleAssistant {
			return fmt.Errorf("message.role: expected assistant, got %q", r.Message.Role)
		}
		if err := r.Message.Validate(); err != nil {
			return fmt.Errorf("message.%w", err)
		}
		for _, part := range r.Message.Parts {
			if part.Kind == PartToolCall {
				hasToolCall = true
				break
			}
		}
	}

	switch r.StopReason {
	case StopReasonStop:
		if hasToolCall {
			return fmt.Errorf("message: tool calls are not allowed for stop reason %q", r.StopReason)
		}
	case StopReasonToolCalls:
		if !hasToolCall {
			return fmt.Errorf("message: at least one complete tool call is required for stop reason %q", r.StopReason)
		}
	}
	if err := r.Usage.Validate(); err != nil {
		return fmt.Errorf("usage.%w", err)
	}
	return nil
}
