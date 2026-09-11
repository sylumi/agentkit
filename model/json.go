package model

import (
	"encoding/json"
	"fmt"
)

// decodeJSONObject rejects null and keeps nested values as raw JSON so checking
// the root never converts numbers to float64. Callers add their own field paths.
func decodeJSONObject(data json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("expected a complete JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("expected a JSON object, got null")
	}
	return object, nil
}
