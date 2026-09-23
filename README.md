<h1 align="center">Agent Development Kit</h1>

<p align="center">
  <img src="assets/agentkit-logo.svg" alt="AgentKit logo" width="160" height="160">
</p>

<p align="center">
  A Go library for building AI agents.
</p>

---

Currently includes:

- A model interface and an OpenAI Responses API adapter.
- An agent loop that executes tool calls and sends results back to the model.
- A runner with session storage and streaming events.
- In-memory sessions, typed function tools, and tools for reading skills.
- Toolsets with automatic discovery and optional model request preprocessing.
- An embedded model catalog sourced from [models.dev](https://models.dev).

Under development. No release yet.

The agent discovers each toolset once per Run. Tool names must be unique across
static tools and toolsets. Toolsets that implement `tool.RequestProcessor` can
enrich each fresh model request; shared toolsets must support concurrent runs.

Licensed under [Apache 2.0](LICENSE).
