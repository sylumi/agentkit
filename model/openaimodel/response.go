package openaimodel

import (
	"errors"
	"fmt"

	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

func (s *responseStream) readUsage(usage responses.ResponseUsage) error {
	next := model.Usage{}
	if usage.JSON.InputTokens.Valid() {
		value := usage.InputTokens
		next.InputTokens = &value
	}
	if usage.JSON.OutputTokens.Valid() {
		value := usage.OutputTokens
		next.OutputTokens = &value
	}
	if usage.InputTokensDetails.JSON.CachedTokens.Valid() {
		value := usage.InputTokensDetails.CachedTokens
		next.CachedInputTokens = &value
	}
	if usage.OutputTokensDetails.JSON.ReasoningTokens.Valid() {
		value := usage.OutputTokensDetails.ReasoningTokens
		next.ReasoningOutputTokens = &value
	}
	if err := next.Validate(); err != nil {
		return errors.Join(model.ErrInvalidStream, err)
	}
	s.usage = next
	return nil
}

func (s *responseStream) readMetadata(r responses.Response) error {
	next := model.ResponseMetadata{Provider: s.provider}
	if s.metadata != nil {
		next = *s.metadata
	}
	for _, field := range []struct {
		raw    string
		dest   *string
		stable bool
	}{
		{r.JSON.ID.Raw(), &next.ResponseID, true},
		{r.JSON.Model.Raw(), &next.Model, false},
	} {
		if field.raw == "" || field.raw == "null" {
			continue
		}
		value, err := protocolString(field.raw)
		if err != nil {
			return err
		}
		if field.stable && *field.dest != "" && *field.dest != value {
			return protocolError("response ID changed during generation")
		}
		*field.dest = value
	}
	s.metadata = &next
	return nil
}

// completeResponse assembles the same final result for JSON responses and SSE
// terminal snapshots. Existing blocks additionally enforce streamed prefixes;
// a state without a yield callback assembles without emitting intermediate events.
func (s *responseStream) completeResponse(r responses.Response) error {
	if err := s.readMetadata(r); err != nil {
		return err
	}
	reason := model.StopReasonStop
	switch r.Status {
	case "completed":
	case "incomplete":
		switch r.IncompleteDetails.Reason {
		case "max_output_tokens":
			reason = model.StopReasonLength
		case "content_filter":
			reason = model.StopReasonBlocked
		default:
			return protocolError("unsupported incomplete reason %q", r.IncompleteDetails.Reason)
		}
	case "failed":
		if err := s.readUsage(r.Usage); err != nil {
			return err
		}
		return fmt.Errorf("responses: response failed (%s): %s", r.Error.Code, r.Error.Message)
	default:
		return protocolError("unexpected response status %q", r.Status)
	}
	if r.ID == "" || !r.JSON.Output.Valid() {
		return protocolError("terminal response requires an ID and output array")
	}
	if err := s.readUsage(r.Usage); err != nil {
		return err
	}
	seen := make(map[blockKey]bool)
	var parts []model.Part
	for index, item := range r.Output {
		if err := s.item(int64(index), item, false); err != nil {
			return err
		}
		accept := func(content int, kind, text, id, name string) error {
			key := blockKey{int64(index), int64(content), kind}
			seen[key] = true
			b := s.byKey[key]
			if b == nil {
				var err error
				b, err = s.open(key, id, name)
				if err != nil {
					return err
				}
			} else if b.id != id || b.name != name {
				return protocolError("changed tool metadata")
			}
			if err := s.finish(b, text, reason == model.StopReasonLength || reason == model.StopReasonBlocked); err != nil {
				return err
			}
			if b.ended {
				parts = append(parts, b.part())
			}
			return nil
		}
		switch item.Type {
		case "message":
			for j, content := range item.Content {
				if len(content.Annotations) != 0 {
					return protocolError("output annotations are not supported")
				}
				raw := content.JSON.Text.Raw()
				if content.Type == "refusal" {
					raw = content.JSON.Refusal.Raw()
				}
				text, err := protocolString(raw)
				if err != nil {
					return err
				}
				if err := accept(j, content.Type, text, "", ""); err != nil {
					return err
				}
			}
		case "reasoning":
			for j, content := range item.Content {
				if content.Type != "reasoning_text" {
					return protocolError("unsupported reasoning content %q", content.Type)
				}
				if len(content.Annotations) != 0 {
					return protocolError("output annotations are not supported")
				}
				text, err := protocolString(content.JSON.Text.Raw())
				if err != nil {
					return err
				}
				if err := accept(j, "reasoning_text", text, "", ""); err != nil {
					return err
				}
			}
			for j, summary := range item.Summary {
				if summary.Type != "summary_text" {
					return protocolError("unsupported reasoning summary")
				}
				text, err := protocolString(summary.JSON.Text.Raw())
				if err != nil {
					return err
				}
				if err := accept(j, "summary_text", text, "", ""); err != nil {
					return err
				}
			}
		case "function_call":
			if err := accept(0, "function_call", item.Arguments.OfString, item.CallID, item.Name); err != nil {
				return err
			}
		}
	}
	if len(seen) != len(s.blocks) || len(s.items) != len(r.Output) {
		return protocolError("final response omitted streamed output")
	}
	if reason == model.StopReasonStop {
		if s.blocked {
			reason = model.StopReasonBlocked
		} else {
			for _, part := range parts {
				if part.Kind == model.PartToolCall {
					reason = model.StopReasonToolCalls
					break
				}
			}
		}
	}
	result := model.Result{StopReason: reason, Usage: s.usage, Metadata: s.metadata}
	if len(parts) != 0 {
		result.Message = &model.Message{Role: model.RoleAssistant, Parts: parts}
	}
	if err := result.Validate(); err != nil {
		return errors.Join(model.ErrInvalidStream, err)
	}
	s.result = &result
	return nil
}
