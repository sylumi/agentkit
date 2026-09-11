package model

// PartialOutput contains accumulated content and known usage for diagnostics.
// Parts are in stream-index order; a slice index identifies the original block.
// It is not a confirmed Result, even when every block has ended.
// Producers must not modify a delivered snapshot or its nested data.
type PartialOutput struct {
	Parts    []PartialPart     `json:"parts,omitempty"`
	Usage    Usage             `json:"usage"`
	Metadata *ResponseMetadata `json:"metadata,omitempty"`
}

// PartialPart holds text, thinking, or a tool call during generation.
// Exactly one of Text, Thinking, and ToolCall is non-nil and matches Kind.
// Text and Thinking.Text can be empty.
// Ended records a validated part_end, not the terminal outcome of a generation.
type PartialPart struct {
	Kind     PartKind         `json:"kind"`
	Text     *string          `json:"text,omitempty"`
	Thinking *ThinkingPart    `json:"thinking,omitempty"`
	ToolCall *PartialToolCall `json:"tool_call,omitempty"`
	Ended    bool             `json:"ended"`
}

// PartialToolCall preserves the accumulated ID, name, and raw argument text.
// Fields may be empty or incomplete; Arguments is not required to be valid JSON.
type PartialToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
