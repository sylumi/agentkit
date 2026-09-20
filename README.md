# AgentKit

A Go library for building AI agents. It handles model calls, tool execution, and session history, with optional streaming output.

Currently includes:

- A model interface and an OpenAI Responses API adapter.
- An agent loop that executes tool calls and sends results back to the model.
- A runner with session storage and streaming events.
- In-memory sessions, typed function tools, and tools for reading skills.
- An embedded model catalog sourced from [models.dev](https://models.dev).

Under development. No release yet.

Licensed under [Apache 2.0](LICENSE).
