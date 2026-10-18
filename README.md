# SubCoder

SubCoder is an open-source AI coding agent for the terminal, written in Go.
It brings the workflow popularized by OpenCode and similar agentic consoles to
a single static binary: an interactive TUI, a scriptable `run` mode, a headless
HTTP server with a browser UI, an Agent Client Protocol (ACP) server for
editors, multi-provider model support, tools, permissions, subagents, MCP
servers, skills, hooks, custom commands, git-backed snapshots, sessions and
project rules via `AGENTS.md`.

## Features

- **Interactive TUI** (Bubble Tea) with streaming responses, tool call cards,
  slash commands, `@file` mentions, model/agent/session/theme pickers,
  permission prompts and an external editor workflow.
- **Non-interactive `run`** with `text` or NDJSON `json` output for CI and
  scripting, session continuation/forking, custom commands and file
  attachments.
- **Headless `serve`** mode exposing an HTTP + SSE API (sessions, messages,
  permission replies, interrupts, exports, global event bus) and **`web`** for
  the same API plus a built-in browser chat UI.
- **`attach`**: drive a running server from any terminal (streaming output,
  permission prompts, session switching, interrupts, compaction).
- **`acp`**: Agent Client Protocol server over stdio (initialize, session/new,
  session/prompt, session/cancel, session/update, session/request_permission)
  so editors such as Zed can drive SubCoder.
- **Providers**: OpenAI-compatible (`/chat/completions`, works with OpenAI,
  Ollama, LM Studio, OpenRouter, Groq...), native Anthropic Messages API, and a
  deterministic `mock` provider for offline development and tests.
- **Streaming tool loop** with a real agent loop: model -> tool calls ->
  results -> model, up to a configurable step budget.
- **Tools**: `bash`, `read`, `write`, `edit`, `glob`, `grep`, `ls`, `webfetch`,
  `todowrite`, `task` (subagents) and `skill`.
- **Subagents** defined as markdown files with frontmatter (`build`, `plan`,
  `general`, `explore` built in).
- **Skills**: `SKILL.md` bundles discovered from `.subcoder/skill/<name>/` and
  the global config directory, loaded on demand through the `skill` tool.
- **Custom commands**: markdown slash commands in `.subcoder/command/*.md`
  with `$ARGUMENTS`, `$1..$9`, `` !`shell` `` interpolation and `@file`
  includes; runnable from the TUI and with `run --command` / `commands`.
- **Hooks**: shell hooks for `session.start`, `session.end`, `tool.before`
  (can block) and `tool.after`, receiving JSON on stdin and `SUBCODER_*`
  environment variables.
- **Permission engine** with glob rules per tool (`allow` / `ask` / `deny`).
- **Snapshots**: every prompt snapshots the work tree in a shadow git repo;
  `/undo` reverts both the conversation and the files it changed, `/redo`
  restores them.
- **MCP client** for stdio and streamable HTTP servers; every MCP tool is
  registered into the agent toolset as `<server>_<tool>`.
- **Sessions** persisted as JSON, resumable, forkeable, exportable (with
  `--sanitize`), importable, with token and cost accounting.
- **Project rules**: `AGENTS.md` discovery walking up the tree plus a global
  instructions file.
- **Compaction** (`/compact`) to summarize long conversations.
- **GitHub Actions agent** (`github install|run`) and `pr` checkout workflow.
- **Tests everywhere**: unit tests per package plus CLI/TUI/server/ACP
  integration tests with fake providers, fake servers and MCP servers.

## Install

```sh
go install github.com/arthurkushman/subcoder@latest
```

or build from source (dependencies are vendored, no network needed):

```sh
git clone git@github.com:arthurkushman/subcoder.git
cd subcoder
go build -o subcoder .
```

## Quick start

```sh
# store an API key (or set OPENAI_API_KEY / ANTHROPIC_API_KEY)
subcoder auth login --provider anthropic --key sk-ant-...

# interactive TUI (starts a new session)
subcoder

# continue the most recent session in this directory
subcoder --continue

# one-shot, scriptable
subcoder run "add tests for the config package"

# run a custom command
subcoder run --command review main.go --file go.mod

# machine-readable NDJSON events
subcoder run --format json "explain the build system" | jq .type

# headless server + browser UI
subcoder serve --port 4096
subcoder web --port 4096

# drive a running server from another terminal
subcoder attach http://localhost:4096 --session ses_...

# editor integration over ACP (stdio)
subcoder acp
```

## CLI commands

| Command | Description |
| --- | --- |
| `subcoder [project]` | Start the interactive TUI (`-c/--continue`, `-s/--session`, `--fork`, `--prompt`) |
| `subcoder tui` | Same as above |
| `subcoder run [message...]` | Run the agent non-interactively (`--format text\|json`, `-c/--continue`, `-s/--session`, `--fork`, `--command`, `-f/--file`, `--title`) |
| `subcoder serve` | Start the HTTP API (`--hostname`, `--port`) |
| `subcoder web` | Start the HTTP API with a browser UI (`--no-open`) |
| `subcoder attach <url>` | Attach a terminal to a running server (`-s/--session`, `-p/--prompt`, `--auto`) |
| `subcoder acp` | Run an Agent Client Protocol server over stdio |
| `subcoder auth login\|list\|logout` | Manage provider credentials (stored 0600 in the data dir) |
| `subcoder models [provider]` | List models (`--verbose` for context/cost) |
| `subcoder agent create\|list` | Create markdown agents, list loaded agents |
| `subcoder commands` | List custom markdown commands (`--json`) |
| `subcoder session list\|new\|show\|delete` | Manage sessions (`--format json`, `-n/--max-count`) |
| `subcoder export [id] [-o file] [--sanitize]` | Export a session as JSON |
| `subcoder import <file>` | Import an exported session |
| `subcoder mcp list\|add\|remove\|test` | Manage MCP servers |
| `subcoder stats [--days N]` | Aggregate token/cost statistics |
| `subcoder github install\|run` | Install/run the GitHub Actions agent (`--event`, `--prompt`) |
| `subcoder pr <number>` | Check out a GitHub PR with `gh` and start the TUI |
| `subcoder debug paths\|config\|env\|providers` | Troubleshooting helpers (secrets redacted) |
| `subcoder db path` | Print the session store path |
| `subcoder upgrade [version]` | Upgrade via `go install` (`--check`, `--dry-run`) |
| `subcoder uninstall` | Remove config, data and the binary (`--dry-run`, `-f`) |
| `subcoder init` | Generate an `AGENTS.md` for the project |
| `subcoder version` | Print the version |

Global flags: `--directory`, `--model/-m`, `--agent/-a`, `--auto-approve`,
`--print-logs`, `--log-level`.

### Slash commands (TUI)

`/help` `/init` `/new` `/sessions` `/models` `/agents` `/themes` `/commands`
`/editor` `/undo` `/redo` `/compact` `/details` `/thinking` `/timestamps`
`/export` `/exit`, custom commands from `.subcoder/command/*.md`, file
mentions with `@path`, and `!command` shell escapes. Shortcuts: `enter` send,
`ctrl+j` newline, `tab` switch agent, `esc` interrupt, `ctrl+c` quit.

`/undo` and `/redo` restore the files touched by the reverted turn using the
git-backed snapshot taken before each prompt.

## Custom commands

Drop markdown files in `<project>/.subcoder/command/` or
`~/.config/subcoder/command/`:

```markdown
---
description: Review a file against the project conventions
agent: plan
model: anthropic/claude-sonnet-4-5
---
Review $ARGUMENTS against the conventions in @AGENTS.md.

Current branch: !`git branch --show-current`
```

- `$ARGUMENTS` expands to everything after the command name, `$1`..`$9` to
  individual arguments.
- `` !`command` `` runs the command in the project directory and inlines the
  output.
- `@path` embeds the file into the prompt.
- Without an arguments placeholder the arguments are appended.

Commands run from the TUI (`/review main.go`) and from the CLI
(`subcoder run --command review main.go`).

### Skills

Skills follow the `SKILL.md` convention:

```
.subcoder/skill/release/SKILL.md
```

```markdown
---
name: release
description: Cut a release
---
1. Run the test suite.
2. Tag the release and push.
```

The system prompt lists available skills and the agent loads the full
instructions through the `skill` tool when a task matches.

### Hooks

Hooks are shell commands configured per event. They receive the event JSON on
stdin and `SUBCODER_EVENT`, `SUBCODER_SESSION_ID`, `SUBCODER_DIRECTORY`,
`SUBCODER_AGENT`, `SUBCODER_TOOL` in the environment. A failing `tool.before`
hook blocks the tool call.

```jsonc
{
  "hooks": {
    "tool.before": [
      { "command": "./scripts/check-policy.sh", "timeout": 5 }
    ],
    "session.end": [{ "command": "notify-send 'SubCoder finished'" }]
  }
}
```

## Configuration

Global config: `~/.config/subcoder/subcoder.json[c]` (or `$SUBCODER_CONFIG`).
Project config: nearest `subcoder.json[c]` walking up from the working
directory; project values override global ones. JSONC comments and trailing
commas are supported.

```jsonc
{
  "model": "anthropic/claude-sonnet-4-5",
  "smallModel": "openai/gpt-5.2-mini",
  "theme": "dracula",
  "maxSteps": 50,
  "autoApprove": false,
  "snapshots": true,
  "provider": {
    "anthropic": { "apiKey": "..." },
    "openai": { "baseURL": "https://api.openai.com/v1" },
    "ollama": {
      "type": "openai",
      "baseURL": "http://localhost:11434/v1",
      "models": { "qwen3-coder": { "name": "Qwen3 Coder", "context": 256000 } }
    },
    "mock": {}
  },
  "permission": {
    "bash": { "git status": "allow", "git push*": "ask", "*": "ask" },
    "write": "allow",
    "read": "allow"
  },
  "hooks": {
    "tool.before": [{ "command": "echo \"$SUBCODER_TOOL\" >> .subcoder/tools.log" }]
  },
  "mcp": {
    "filesystem": {
      "type": "local",
      "command": ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    },
    "remote": { "type": "remote", "url": "https://example.com/mcp" }
  },
  "agent": {
    "review": {
      "description": "Code review bot",
      "mode": "subagent",
      "model": "anthropic/claude-opus-4-6",
      "tools": ["read", "grep", "glob"],
      "prompt": "You review code changes critically."
    }
  },
  "instructions": ["Always run gofmt before finishing."]
}
```

Credential resolution order: provider `apiKey` -> auth store ->
`SUBCODER_<PROVIDER>_API_KEY` -> provider env vars (`OPENAI_API_KEY`,
`ANTHROPIC_API_KEY`).

Themes: `default`, `dark`, `light`, `dracula`, `nord`, `gruvbox`,
`tokyonight`, `mono` (switch at runtime with `/themes`).

### Markdown agents

Drop `*.md` files in `<project>/.subcoder/agent/` or
`~/.config/subcoder/agent/`:

```markdown
---
description: Security reviewer
mode: subagent
model: anthropic/claude-sonnet-4-5
tools: [read, grep, glob]
---
You audit code for security issues and report findings with file:line references.
```

### Project rules

`AGENTS.md` files are discovered from the working directory up to the root,
then the global config directory. Their contents are injected into the system
prompt. `subcoder init` (or `/init`) generates a starter file based on a quick
project scan.

## Server API

`subcoder serve` exposes:

- `GET /health`, `GET /version`, `GET /models`, `GET /agents`
- `GET|POST /sessions`, `GET|DELETE /sessions/{id}`
- `POST /sessions/{id}/messages` - runs the agent, streams SSE events
- `POST /sessions/{id}/compact`, `POST /sessions/{id}/interrupt`
- `POST /sessions/{id}/permissions/{reqID}` - reply `once` / `always` / `reject`
- `GET /sessions/{id}/export`
- `GET /event` - global SSE event bus

`subcoder web` serves the same API plus a browser chat UI at `/`. Set
`SUBCODER_SERVER_TOKEN` to require `Authorization: Bearer <token>`.

## Architecture

```
main.go                  entrypoint
cmd/                     cobra commands and wiring
internal/
  acp/                   Agent Client Protocol server (JSON-RPC over stdio)
  agent/                 agent loop, system prompt, subagents, compaction
  auth/                  credential store
  command/               custom markdown commands and template expansion
  config/                JSONC config with global/project merge
  frontmatter/           shared markdown frontmatter parser
  hook/                  lifecycle hook engine
  id/                    prefixed random IDs
  mcp/                   JSON-RPC MCP clients (stdio, streamable HTTP)
  permission/            allow/ask/deny engine with glob rules
  prompt/                @file mention extraction and embedding
  provider/              provider interface, SSE, model registry
    openai/              OpenAI-compatible streaming
    anthropic/           Anthropic Messages streaming
    mock/                scripted provider for tests/offline
  remote/                HTTP client used by attach
  rules/                 AGENTS.md discovery and init generation
  server/                HTTP + SSE API, permission broker and web UI
  session/               message model and JSON store
  skill/                 SKILL.md discovery
  snapshot/              git-backed work tree snapshots
  tool/                  tool interface, registry, builtin tools
  tui/                   Bubble Tea application
  version/
```

The agent loop streams provider events into session messages, executes tool
calls through the permission engine (and hooks), feeds results back, and
repeats until the model stops calling tools or `maxSteps` is reached. Every
mutation is emitted as a typed `session.Event`, which the TUI subscribes to
directly, the server fans out over SSE, the ACP server forwards as
`session/update` notifications, and `attach` prints in the terminal.

## Development

```sh
make build              # or: go build -o subcoder .
make test               # go test -p 1 -count=1 ./...
make test-race          # race detector
make lint               # gofmt + go vet
```

`make test` runs packages sequentially (`-p 1`); on heavily throttled VMs
parallel test binaries can starve and hit the `go test` timeout, while the
whole suite completes in seconds sequentially. A plain `go test ./...` works
as usual on normal machines and CI.

Tests use the scripted `mock` provider, `httptest` SSE servers for the OpenAI
and Anthropic clients, a fake stdio MCP server spawned from the test binary,
in-memory bubbletea model tests, a shadow git repository for snapshot tests,
and an in-process ACP client/server pair. Dependencies are vendored, so builds
and tests work offline.

## Compatibility notes

SubCoder mirrors the command surface and workflows of OpenCode-style agentic
consoles. Plugins are intentionally not implemented as a JavaScript runtime;
custom commands, skills, hooks, MCP servers and subagents cover the same
extension points with plain files and shell scripts.

## License

MIT
