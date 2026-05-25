<div align="center">

# 🌉 claude-bridge

### OpenAI-compatible HTTP API over the Claude CLI. Use your Claude Max subscription with any OpenAI-API tool.

[![Go 1.23](https://img.shields.io/badge/Go-1.23-00ADD8?style=flat-square&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square)](LICENSE)
[![Status: Working](https://img.shields.io/badge/Status-Working-green?style=flat-square)](#)

</div>

---

A tiny Go HTTP server that exposes `POST /v1/chat/completions` (OpenAI Chat Completions API) on the front, and subprocesses the official `claude` CLI on the back. Any tool that speaks OpenAI's API can now reach Claude **through your existing Claude Code subscription** — no API keys, no separate billing, no third-party "extra usage" credits required.

```
┌─────────────────────────┐        OpenAI API         ┌──────────────────────────┐
│  Your OpenAI client     │  ───────────────────────► │  claude-bridge :9180     │
│  - Hermes Agent         │                            │                          │
│  - Open WebUI           │                            │  for each request:        │
│  - LibreChat            │                            │   subprocess `claude -p`  │
│  - Cursor (custom URL)  │                            │   parse JSON output       │
│  - Your own scripts     │                            │   return OpenAI response  │
└─────────────────────────┘                            └──────────────┬───────────┘
                                                                       │
                                                                       ▼
                                                    ┌──────────────────────────┐
                                                    │  claude CLI               │
                                                    │  uses ~/.claude/.cred...  │
                                                    │  → Claude Max subscription│
                                                    └──────────────────────────┘
```

## Why this exists

Anthropic's official `claude` CLI is the only path that uses your Claude Code subscription's **base plan allowance**. Every other path (raw API keys, OAuth via third-party apps) draws from separately-purchased "extra usage" credits that the Max plan doesn't include.

If you've ever seen this error trying to use Claude from a non-Anthropic tool:

> `Third-party apps now draw from your extra usage, not your plan limits. Add more at claude.ai/settings/usage and keep going.`

…this fixes it. The bridge subprocesses `claude` the same way you would in a terminal, and the request flows through Claude Code's auth path → your subscription.

## What works

- ✅ `POST /v1/chat/completions` — both streaming (SSE) and non-streaming
- ✅ `GET /v1/models` — curated model list (sonnet, opus, haiku + full IDs)
- ✅ Multi-turn conversations (assistant/user message history flattened to a transcript)
- ✅ System prompts (`--append-system-prompt`)
- ✅ Configurable directory access (`--add-dir`) so Claude can read your project files
- ✅ Optional permission bypass for trusted environments
- ✅ Drop-in for Hermes Agent, Open WebUI, LibreChat, anything else OpenAI-compatible

## What doesn't (yet)

- ❌ **Tool definitions are dropped.** The `claude` CLI doesn't accept tool schemas from callers — it has its own built-in tools (Read, Grep, Bash, Edit, etc.). If your OpenAI client sends a `tools:` array, the bridge silently ignores it. Claude does its work with its own tools instead, which is often equivalent (e.g. searching files) but won't fire your client's custom skills.
- ❌ **No auth.** The bridge trusts its localhost binding. Anyone who can hit the port can spend your subscription. Don't expose it past `127.0.0.1`.
- ❌ **Subprocess overhead.** Each request spawns `claude` fresh (~1–3s of startup). Slower than direct API calls. Worth it for the cost savings.

## Install

### Prereqs

- **[Claude Code](https://claude.ai/code)** installed (`claude --version` should work)
- **Go 1.23+** (only if building from source)

### Quick install (build from source)

```bash
git clone https://github.com/niski84/claude-bridge.git
cd claude-bridge
go build -o claude-bridge ./cmd/claude-bridge
./claude-bridge   # listens on :9180
```

### Or with `go install`

```bash
go install github.com/niski84/claude-bridge/cmd/claude-bridge@latest
~/go/bin/claude-bridge
```

### Run as a systemd user service (Linux)

```bash
mkdir -p ~/.config/systemd/user
cat > ~/.config/systemd/user/claude-bridge.service <<EOF
[Unit]
Description=claude-bridge — OpenAI-compatible HTTP shim over Claude CLI
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$(pwd)/claude-bridge
WorkingDirectory=$(pwd)
Environment="PATH=$HOME/.local/bin:/usr/local/bin:/usr/bin:/bin"
Environment="PORT=9180"
Environment="DEFAULT_MODEL=sonnet"
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now claude-bridge

# Tail the logs:
journalctl --user -u claude-bridge -f
```

## Configuration

All via environment variables.

| Var | Default | Purpose |
|---|---|---|
| `PORT` | `9180` | HTTP listen port |
| `CLAUDE_BIN` | auto (`~/.local/bin/claude` → `$PATH`) | Path to the `claude` binary |
| `DEFAULT_MODEL` | `sonnet` | Model alias when client doesn't specify (`sonnet`, `opus`, `haiku`, or full IDs like `claude-sonnet-4-6`) |
| `CLAUDE_ALLOWED_DIRS` | `$HOME/Documents:$HOME/.hermes:$HOME/goprojects` | Colon-separated dirs passed as `--add-dir` so Claude can read them |
| `CLAUDE_BYPASS_PERMISSIONS` | `false` | If `true`, passes `--dangerously-skip-permissions` instead. Use only on personal machines. |

## Use it

### From curl

```bash
# Non-streaming
curl http://localhost:9180/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "sonnet",
    "messages": [{"role": "user", "content": "what is 2+2?"}]
  }'

# Streaming (SSE)
curl -N http://localhost:9180/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "sonnet",
    "stream": true,
    "messages": [{"role": "user", "content": "write a haiku"}]
  }'
```

### From the OpenAI Python SDK

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:9180/v1",
    api_key="not-used",  # the bridge doesn't check
)

resp = client.chat.completions.create(
    model="sonnet",
    messages=[{"role": "user", "content": "hello"}],
)
print(resp.choices[0].message.content)
```

### Wire into [Hermes Agent](https://github.com/NousResearch/hermes-agent)

**Easiest path — use the companion plugin:**

There's a small Hermes plugin that registers a first-class `claude-cli` provider so the bridge shows up in `hermes model` next to Anthropic, OpenRouter, etc. — no manual config editing required.

```bash
curl -fsSL https://raw.githubusercontent.com/niski84/hermes-claude-cli/main/scripts/install.sh | bash
```

That installer handles claude-bridge + the Hermes plugin + a systemd unit in one shot. After it finishes, run `hermes model` and pick "Claude CLI (Max subscription)". Repo: **[niski84/hermes-claude-cli](https://github.com/niski84/hermes-claude-cli)**.

There's also an [upstream PR pending](https://github.com/NousResearch/hermes-agent/pull/31796) to ship the provider as part of Hermes core — if it merges, future Hermes users get the picker entry for free and only need to install claude-bridge.

**Manual path — edit config directly:**

In `~/.hermes/config.yaml`:

```yaml
model:
  default: sonnet
  provider: custom
  base_url: http://localhost:9180/v1
  api_key: bridge   # any non-empty string
```

Then `systemctl --user restart hermes-gateway`. Hermes is now powered by your Claude subscription. (This works but you won't see "Claude CLI" labeled in `hermes model` — the picker only labels named providers.)

### Wire into Open WebUI

In Settings → Connections → OpenAI API:
- **API Base URL:** `http://localhost:9180/v1`
- **API Key:** anything (the bridge doesn't check)

### Wire into Cursor (Custom API)

Settings → Models → OpenAI API Key → toggle "Override OpenAI Base URL":
- **Base URL:** `http://localhost:9180/v1`
- **API Key:** anything

## Endpoints

| | |
|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions (streaming and non-streaming) |
| `GET /v1/models` | List of available model aliases |
| `GET /api/health` | Liveness + config status |
| `GET /` | Plain-text usage hint |

## Cost accounting

The bridge logs each request with the `claude` CLI's reported usage:

```
[claude-bridge] sonnet: in=3 out=458 cost=$0.0705 stop=end_turn stream=true
```

⚠️ **The `cost` field is the API-equivalent cost — what the call *would* cost at standard API rates.** It is **not** what Claude charges you. Claude Code's billing model:

1. First eaten by your **Claude Max base plan allowance** (no charge)
2. Then by **extra usage credits** if you've purchased any
3. Then by raw API billing if you've enabled fallback

The cost number is a usage gauge, not a bill.

## Architecture

Single Go file (~350 LOC). Reads requests via `net/http`, flattens OpenAI's message array into a `--append-system-prompt` + stdin prompt for `claude -p`, parses claude's `--output-format json` response, returns it as either a single OpenAI completion or a 3-chunk SSE stream depending on `req.stream`.

That's it. No dependencies beyond the Go standard library.

```
cmd/claude-bridge/main.go    # everything's here
go.mod                        # module + go version
README.md                     # this file
```

## Troubleshooting

### `could not locate claude binary`

```bash
which claude   # should print /home/you/.local/bin/claude or similar
# If empty, install Claude Code: https://claude.ai/code
# Or set CLAUDE_BIN=/explicit/path/to/claude
```

### `permission denied` errors from claude

Claude CLI requires explicit permission for filesystem reads outside CWD. Either:

- Add the relevant paths: `CLAUDE_ALLOWED_DIRS=/path1:/path2`
- Or trust the bridge entirely (localhost only): `CLAUDE_BYPASS_PERMISSIONS=true`

### `Empty response (no content)` from your OpenAI client

You're probably on an older bridge build that didn't support streaming. Pull the latest — SSE streaming is supported as of the initial v0.1 release.

### Claude responds with permission prompts (e.g. "I need access to…")

Same fix as above — add `--add-dir` paths via `CLAUDE_ALLOWED_DIRS` or set `CLAUDE_BYPASS_PERMISSIONS=true`.

## Security notes

- **Localhost only.** The bridge does not bind to `0.0.0.0` by default and there's no auth layer. If you must expose it past loopback, put a reverse proxy with auth in front.
- **No request body validation beyond JSON parsing.** Any local process can drive your subscription.
- **Subprocesses inherit the bridge's env.** Don't put untrusted env vars in the systemd unit.
- **`CLAUDE_BYPASS_PERMISSIONS=true` gives Claude full filesystem access** as your user. Fine for a personal dev machine. Don't do it on a shared server.

## License

MIT. See [LICENSE](LICENSE).

## Related projects

- **[Claude Code](https://claude.ai/code)** — Anthropic's official CLI, what this wraps.
- **[Hermes Agent](https://github.com/NousResearch/hermes-agent)** — Nous Research's open-source AI agent framework. Tested integration target.

## Contributing

Issues and PRs welcome. Particularly interested in:

- **Tool-call translation** — passing OpenAI `tools:` arrays through to Claude (currently dropped). Probably needs prompt-engineering Claude to return structured tool calls + parsing them out of its text response.
- **Token streaming** — currently the bridge waits for `claude -p` to finish then emits one big content chunk. Real token-by-token streaming would need `claude -p --output-format stream-json` parsing.
- **Auth** — optional bearer-token gate via env var, for users who want to expose past loopback.
- **Vision** — Claude accepts images; OpenAI clients send them in the `content` array. Translate `[{type:"image_url", image_url:{url:"data:..."}}]` to a temp file and pass to `claude` via `--file` or stdin.
