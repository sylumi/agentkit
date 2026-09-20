package agent

import "github.com/sylumi/agentkit/session"

// InvocationContext carries data for one invocation.
type InvocationContext struct {
	InvocationID string
	Session      session.Session
	// Streaming requests partial events from agents that support streaming.
	Streaming bool
}
