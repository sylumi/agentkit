package openaimodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/sylumi/agentkit/model"
)

var errConsumerStopped = errors.New("consumer stopped")

type blockKey struct {
	output, content int64
	typeName        string
}

type outputItem struct{ id, kind string }

type responseBlock struct {
	index        int
	kind         model.PartKind
	thinkingKind model.ThinkingKind
	id, name     string
	data         strings.Builder
	ended        bool
}

type responseStream struct {
	yield    func(model.Event) bool
	items    map[int64]outputItem
	blocks   []*responseBlock
	byKey    map[blockKey]*responseBlock
	usage    model.Usage
	blocked  bool
	result   *model.Result
	metadata *model.ResponseMetadata
}

func protocolError(format string, args ...any) error {
	return fmt.Errorf("%w: responses: %s", model.ErrInvalidStream, fmt.Sprintf(format, args...))
}

// The SDK can coerce JSON numbers into strings. Check raw content fields before
// exposing them as text or tool arguments.
func protocolString(raw string) (string, error) {
	var value *string
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return "", protocolError("expected a JSON string")
	}
	return *value, nil
}

func (s *responseStream) emit(event model.Event) error {
	// Ordinary responses reuse block assembly without exposing incremental events.
	if s.yield == nil {
		return nil
	}
	if !s.yield(event) {
		return errConsumerStopped
	}
	return nil
}

func (s *responseStream) item(index int64, item responses.ResponseOutputItemUnion, added bool) error {
	if index < 0 || item.ID == "" {
		return protocolError("invalid output item index or ID")
	}
	if item.Type != "message" && item.Type != "function_call" && item.Type != "reasoning" {
		return protocolError("unsupported output type %q", item.Type)
	}
	if raw := item.JSON.EncryptedContent.Raw(); raw != "" && raw != "null" {
		content, err := protocolString(raw)
		if err != nil {
			var value any
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				return protocolError("encrypted_content is invalid JSON")
			}
			return protocolError("encrypted_content must be a string or null, got %T", value)
		}
		if content != "" {
			return protocolError("encrypted reasoning output is not supported (encrypted_content: non-empty string, %d bytes; item_type=%s, content_parts=%d, summary_parts=%d)", len(content), item.Type, len(item.Content), len(item.Summary))
		}
	}
	if item.Type == "function_call" {
		if _, err := protocolString(item.JSON.Arguments.Raw()); err != nil {
			return protocolError("function arguments must be a JSON string")
		}
	}
	if item.Type == "message" && item.Role != "assistant" {
		return protocolError("output message must be assistant")
	}
	if s.items == nil {
		s.items = make(map[int64]outputItem)
	}
	value := outputItem{item.ID, item.Type}
	if previous, ok := s.items[index]; ok {
		if added || previous != value {
			return protocolError("duplicate or changed output item %d", index)
		}
	} else {
		s.items[index] = value
	}
	return nil
}

func (s *responseStream) open(key blockKey, id, name string) (*responseBlock, error) {
	if key.output < 0 || key.content < 0 {
		return nil, protocolError("negative content index")
	}
	item, exists := s.items[key.output]
	if !exists {
		return nil, protocolError("content before output item")
	}
	kind := model.PartText
	thinkingKind := model.ThinkingUnknown
	switch key.typeName {
	case "output_text", "refusal":
		if item.kind != "message" {
			return nil, protocolError("text on non-message item")
		}
	case "reasoning_text", "summary_text":
		if item.kind != "reasoning" {
			return nil, protocolError("thinking on non-reasoning item")
		}
		kind = model.PartThinking
		thinkingKind = model.ThinkingText
		if key.typeName == "summary_text" {
			thinkingKind = model.ThinkingSummary
		}
	case "function_call":
		if item.kind != "function_call" || strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" {
			return nil, protocolError("invalid function call metadata")
		}
		kind = model.PartToolCall
	default:
		return nil, protocolError("unsupported content type %q", key.typeName)
	}
	if s.byKey == nil {
		s.byKey = make(map[blockKey]*responseBlock)
	}
	if s.byKey[key] != nil {
		return nil, protocolError("duplicate content block")
	}
	b := &responseBlock{index: len(s.blocks), kind: kind, thinkingKind: thinkingKind, id: id, name: name}
	s.byKey[key] = b
	s.blocks = append(s.blocks, b)
	if key.typeName == "refusal" {
		s.blocked = true
	}
	if err := s.emit(model.PartStart{Index: b.index, Kind: kind, ThinkingKind: thinkingKind}); err != nil {
		return b, err
	}
	if kind == model.PartToolCall {
		if err := s.emit(model.ToolCallDelta{Index: b.index, ID: id, Name: name}); err != nil {
			return b, err
		}
	}
	return b, nil
}

func (s *responseStream) append(b *responseBlock, delta string) error {
	if b == nil || b.ended {
		return protocolError("delta for missing or ended block")
	}
	if delta == "" {
		return nil
	}
	b.data.WriteString(delta)
	switch b.kind {
	case model.PartThinking:
		return s.emit(model.ThinkingDelta{Index: b.index, Delta: delta})
	case model.PartToolCall:
		return s.emit(model.ToolCallDelta{Index: b.index, Arguments: delta})
	default:
		return s.emit(model.TextDelta{Index: b.index, Delta: delta})
	}
}

func (b *responseBlock) part() model.Part {
	text := strings.Clone(b.data.String())
	switch b.kind {
	case model.PartThinking:
		part := model.ThinkingPart{Kind: b.thinkingKind, Text: text}
		return model.Part{Kind: model.PartThinking, Thinking: &part}
	case model.PartToolCall:
		return model.Part{Kind: model.PartToolCall, ToolCall: &model.ToolCallPart{ID: b.id, Name: b.name, Arguments: json.RawMessage(text)}}
	default:
		return model.NewTextPart(text)
	}
}

// finish reconciles a protocol snapshot with its emitted prefix. Only the
// missing suffix is emitted; a changed prefix is a protocol failure.
func (s *responseStream) finish(b *responseBlock, full string, allowIncomplete bool) error {
	if b == nil {
		return protocolError("completion before content start")
	}
	previous := b.data.String()
	if !strings.HasPrefix(full, previous) || (b.ended && full != previous) {
		return protocolError("completed content differs from streamed prefix")
	}
	if b.ended {
		return nil
	}
	if err := s.append(b, full[len(previous):]); err != nil {
		return err
	}
	if err := b.part().Validate(); err != nil {
		if allowIncomplete && b.kind == model.PartToolCall {
			return nil
		}
		return errors.Join(model.ErrInvalidStream, fmt.Errorf("parts[0].%w", err))
	}
	b.ended = true
	return s.emit(model.PartEnd{Index: b.index})
}

func (s *responseStream) apply(e responses.ResponseStreamEventUnion) (bool, error) {
	switch e.Type {
	case responseCreated, responseInProgress, responseQueued:
		if err := s.readMetadata(e.Response); err != nil {
			return false, err
		}
		if e.Response.JSON.Usage.Valid() {
			return false, s.readUsage(e.Response.Usage)
		}
		return false, nil
	case responseOutputItemAdded:
		if err := s.item(e.OutputIndex, e.Item, true); err != nil {
			return false, err
		}
		if e.Item.Type == "function_call" {
			b, err := s.open(blockKey{e.OutputIndex, 0, "function_call"}, e.Item.CallID, e.Item.Name)
			if err != nil {
				return false, err
			}
			return false, s.append(b, e.Item.Arguments.OfString)
		}
		return false, nil
	case responseContentPartAdded, responseReasoningSummaryPartAdded:
		if len(e.Part.Annotations) != 0 {
			return false, protocolError("output annotations are not supported")
		}
		index := e.ContentIndex
		if e.Type == responseReasoningSummaryPartAdded {
			index = e.SummaryIndex
		}
		if item, ok := s.items[e.OutputIndex]; !ok || item.id != e.ItemID {
			return false, protocolError("content item ID mismatch")
		}
		raw := e.Part.JSON.Text.Raw()
		if e.Part.Type == "refusal" {
			raw = e.Part.JSON.Refusal.Raw()
		}
		text, err := protocolString(raw)
		if err != nil {
			return false, err
		}
		b, err := s.open(blockKey{e.OutputIndex, index, e.Part.Type}, "", "")
		if err != nil {
			return false, err
		}
		return false, s.append(b, text)
	case responseOutputTextDelta, responseRefusalDelta, responseReasoningTextDelta, responseReasoningSummaryTextDelta, responseFunctionCallArgumentsDelta,
		responseOutputTextDone, responseRefusalDone, responseReasoningTextDone, responseReasoningSummaryTextDone, responseFunctionCallArgumentsDone:
		kind, index, raw := "output_text", e.ContentIndex, e.JSON.Text.Raw()
		switch e.Type {
		case responseRefusalDelta, responseRefusalDone:
			kind, raw = "refusal", e.JSON.Refusal.Raw()
		case responseReasoningTextDelta, responseReasoningTextDone:
			kind = "reasoning_text"
		case responseReasoningSummaryTextDelta, responseReasoningSummaryTextDone:
			kind, index = "summary_text", e.SummaryIndex
		case responseFunctionCallArgumentsDelta, responseFunctionCallArgumentsDone:
			kind, index, raw = "function_call", 0, e.JSON.Arguments.Raw()
		}
		if item, ok := s.items[e.OutputIndex]; !ok || item.id != e.ItemID {
			return false, protocolError("delta item ID mismatch")
		}
		b := s.byKey[blockKey{e.OutputIndex, index, kind}]
		if strings.HasSuffix(e.Type, ".delta") {
			delta, err := protocolString(e.JSON.Delta.Raw())
			if err != nil {
				return false, err
			}
			return false, s.append(b, delta)
		}
		full, err := protocolString(raw)
		if err != nil {
			return false, err
		}
		return false, s.finish(b, full, kind == "function_call")
	case responseContentPartDone, responseReasoningSummaryPartDone, responseOutputItemDone:
		// Text/argument done events and the final response carry the same content.
		return false, nil
	case responseCompleted, responseIncomplete:
		if e.Type != "response."+string(e.Response.Status) {
			return false, protocolError("terminal event and response status differ")
		}
		if err := s.completeResponse(e.Response); err != nil {
			return false, err
		}
		return true, s.emit(model.ResultEvent{Result: *s.result})
	case responseFailed:
		if err := s.readMetadata(e.Response); err != nil {
			return false, err
		}
		if err := s.readUsage(e.Response.Usage); err != nil {
			return false, err
		}
		return false, fmt.Errorf("responses: response failed (%s): %s", e.Response.Error.Code, e.Response.Error.Message)
	case errorEvent:
		return false, fmt.Errorf("responses: stream error (%s): %s", e.Code, e.Message)
	default:
		return false, protocolError("unsupported stream event %q", e.Type)
	}
}

func (s *responseStream) snapshot() model.PartialOutput {
	out := model.PartialOutput{}
	if s.metadata != nil {
		metadata := *s.metadata
		out.Metadata = &metadata
	}
	if s.usage.InputTokens != nil {
		n := *s.usage.InputTokens
		out.Usage.InputTokens = &n
	}
	if s.usage.OutputTokens != nil {
		n := *s.usage.OutputTokens
		out.Usage.OutputTokens = &n
	}
	if s.usage.CachedInputTokens != nil {
		n := *s.usage.CachedInputTokens
		out.Usage.CachedInputTokens = &n
	}
	if s.usage.ReasoningOutputTokens != nil {
		n := *s.usage.ReasoningOutputTokens
		out.Usage.ReasoningOutputTokens = &n
	}
	for _, b := range s.blocks {
		partial := model.PartialPart{Kind: b.kind, Ended: b.ended}
		text := strings.Clone(b.data.String())
		switch b.kind {
		case model.PartText:
			partial.Text = &text
		case model.PartThinking:
			partial.Thinking = &model.ThinkingPart{Kind: b.thinkingKind, Text: text}
		case model.PartToolCall:
			partial.ToolCall = &model.PartialToolCall{ID: b.id, Name: b.name, Arguments: text}
		}
		out.Parts = append(out.Parts, partial)
	}
	return out
}
