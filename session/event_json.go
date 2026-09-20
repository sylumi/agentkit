package session

import (
	"encoding/json"
	"fmt"

	"github.com/sylumi/agentkit/model"
)

// Model events are an in-process union. Session events add the discriminator
// needed to carry those same values over a JSON stream.
type eventJSON Event

type deltaJSON struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	var delta *deltaJSON
	if e.Partial != (e.Delta != nil) {
		return nil, fmt.Errorf("session: partial events require a delta; complete events cannot carry one")
	}
	if e.Delta != nil {
		if err := model.ValidateEvent(e.Delta); err != nil {
			return nil, err
		}
		var kind string
		switch e.Delta.(type) {
		case model.PartStart:
			kind = "part_start"
		case model.TextDelta:
			kind = "text_delta"
		case model.ThinkingDelta:
			kind = "thinking_delta"
		case model.ToolCallDelta:
			kind = "tool_call_delta"
		case model.PartEnd:
			kind = "part_end"
		default:
			return nil, fmt.Errorf("session: unsupported delta %T", e.Delta)
		}
		data, err := json.Marshal(e.Delta)
		if err != nil {
			return nil, err
		}
		delta = &deltaJSON{Type: kind, Data: data}
	}
	return json.Marshal(struct {
		eventJSON
		Delta *deltaJSON `json:"delta,omitempty"`
	}{eventJSON(e), delta})
}

func (e *Event) UnmarshalJSON(data []byte) error {
	var wire struct {
		eventJSON
		Delta *deltaJSON `json:"delta"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Partial != (wire.Delta != nil) {
		return fmt.Errorf("session: partial events require a delta; complete events cannot carry one")
	}
	if d := wire.Delta; d != nil {
		var err error
		switch d.Type {
		case "part_start":
			wire.eventJSON.Delta, err = decodeDelta[model.PartStart](d.Data)
		case "text_delta":
			wire.eventJSON.Delta, err = decodeDelta[model.TextDelta](d.Data)
		case "thinking_delta":
			wire.eventJSON.Delta, err = decodeDelta[model.ThinkingDelta](d.Data)
		case "tool_call_delta":
			wire.eventJSON.Delta, err = decodeDelta[model.ToolCallDelta](d.Data)
		case "part_end":
			wire.eventJSON.Delta, err = decodeDelta[model.PartEnd](d.Data)
		default:
			return fmt.Errorf("session: unknown delta type %q", d.Type)
		}
		if err != nil {
			return err
		}
	}
	*e = Event(wire.eventJSON)
	return nil
}

func decodeDelta[T model.Event](data []byte) (model.Event, error) {
	var delta T
	if err := json.Unmarshal(data, &delta); err != nil {
		return nil, err
	}
	return delta, model.ValidateEvent(delta)
}
