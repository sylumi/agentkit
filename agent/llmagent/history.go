package llmagent

import (
	"fmt"
	"sort"

	"github.com/sylumi/agentkit/model"
	"github.com/sylumi/agentkit/session"
)

// buildHistory groups tool results after their calls and drops unpaired tool
// parts from model requests. Saved events and their nested data stay read-only.
func buildHistory(events session.Events) ([]model.Message, error) {
	var messages []model.Message
	firstCalls := make(map[string]historyPosition)
	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		if event == nil {
			return nil, fmt.Errorf("llmagent: history[%d]: event is nil", i)
		}
		if event.Partial || event.Message == nil {
			continue
		}
		if err := event.Message.Validate(); err != nil {
			return nil, fmt.Errorf("llmagent: history[%d]: message: %w", i, err)
		}
		for j, part := range event.Message.Parts {
			if part.Kind == model.PartToolCall {
				if _, exists := firstCalls[part.ToolCall.ID]; !exists {
					firstCalls[part.ToolCall.ID] = historyPosition{message: len(messages), part: j}
				}
			}
		}
		messages = append(messages, *event.Message)
	}

	// Associate each result with the latest preceding occurrence of its call ID.
	// Like ADK Python, a result preceding every matching call belongs to the first.
	// Repeated results update that occurrence rather than answering another call.
	latestCalls := make(map[string]historyPosition)
	results := make(map[historyPosition]historyToolResult)
	order := 0
	for i, message := range messages {
		for j, part := range message.Parts {
			switch part.Kind {
			case model.PartToolCall:
				latestCalls[part.ToolCall.ID] = historyPosition{message: i, part: j}
			case model.PartToolResult:
				id := part.ToolResult.CallID
				call, exists := latestCalls[id]
				if !exists {
					call, exists = firstCalls[id]
				}
				if exists {
					results[call] = historyToolResult{part: part, order: order}
				}
			}
			order++
		}
	}

	var history []model.Message
	for i, message := range messages {
		if message.Role == model.RoleTool {
			continue // Matched results are emitted with their call message below.
		}
		var parts []model.Part
		var responses []historyToolResult
		for j, part := range message.Parts {
			if part.Kind == model.PartToolCall {
				result, exists := results[historyPosition{message: i, part: j}]
				if !exists {
					continue
				}
				responses = append(responses, result)
			}
			parts = append(parts, part)
		}
		if len(parts) > 0 {
			history = append(history, model.Message{Role: message.Role, Parts: parts})
		}
		if len(responses) > 0 {
			// Preserve the relative order of the selected results when merging them.
			sort.Slice(responses, func(i, j int) bool { return responses[i].order < responses[j].order })
			resultParts := make([]model.Part, len(responses))
			for j, result := range responses {
				resultParts[j] = result.part
			}
			history = append(history, model.Message{Role: model.RoleTool, Parts: resultParts})
		}
	}
	return history, nil
}

// A call ID can be reused, so results belong to a particular occurrence.
type historyPosition struct {
	message int
	part    int
}

type historyToolResult struct {
	part  model.Part
	order int
}
