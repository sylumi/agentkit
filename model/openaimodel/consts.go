package openaimodel

// Responses stream event types handled by the adapter.
const (
	responseCreated    = "response.created"
	responseInProgress = "response.in_progress"
	responseQueued     = "response.queued"
	responseCompleted  = "response.completed"
	responseIncomplete = "response.incomplete"
	responseFailed     = "response.failed"
	errorEvent         = "error"

	responseOutputItemAdded           = "response.output_item.added"
	responseOutputItemDone            = "response.output_item.done"
	responseContentPartAdded          = "response.content_part.added"
	responseContentPartDone           = "response.content_part.done"
	responseReasoningSummaryPartAdded = "response.reasoning_summary_part.added"
	responseReasoningSummaryPartDone  = "response.reasoning_summary_part.done"

	responseOutputTextDelta            = "response.output_text.delta"
	responseOutputTextDone             = "response.output_text.done"
	responseRefusalDelta               = "response.refusal.delta"
	responseRefusalDone                = "response.refusal.done"
	responseReasoningTextDelta         = "response.reasoning_text.delta"
	responseReasoningTextDone          = "response.reasoning_text.done"
	responseReasoningSummaryTextDelta  = "response.reasoning_summary_text.delta"
	responseReasoningSummaryTextDone   = "response.reasoning_summary_text.done"
	responseFunctionCallArgumentsDelta = "response.function_call_arguments.delta"
	responseFunctionCallArgumentsDone  = "response.function_call_arguments.done"
)
