package agent

import "github.com/sylumi/agentkit/session"

// InvocationContext carries data for one invocation.
type InvocationContext struct {
	InvocationID string
	Session      session.Session
}
