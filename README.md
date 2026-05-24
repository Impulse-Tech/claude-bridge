# claude-bridge

**OpenAI-compatible HTTP shim over the `claude` CLI.**

Lets any tool that speaks OpenAI's Chat Completions API (Hermes Agent, Open WebUI, LibreChat, Cursor, your own scripts) talk to Claude via your **Claude Code subscription** — using your Max plan base allowance instead of API credits or "extra usage" credits.

## Why this exists

Anthropic's own `claude` CLI uses session-based auth tied to your Claude.ai subscription. When you call `api.anthropic.com` directly (via OAuth or API key), [third-party usage rules apply](https://hermes-agent.nousresearch.com/docs/user-guide/features/providers#anthropic-direct) — base Max plan allowance doesn't cover it, and you need separately-purchased "extra usage" credits.

The CLI route doesn't have that gate. So: shell out to the CLI, parse its JSON output, return OpenAI-formatted responses.

## Run

```bash
go build -o claude-bridge ./cmd/claude-bridge
./claude-bridge      # serves :9180
```

Or as a user systemd service (after `go build`):

```bash
systemctl --user enable --now claude-bridge.service
```

## API

| | |
|---|---|
| `POST /v1/chat/completions` | OpenAI-compatible. Translates messages → `claude -p`. |
| `GET /v1/models` | Static list: sonnet, opus, haiku, plus full IDs. |
| `GET /api/health` | Liveness. |

## Env

| Var | Default |
|---|---|
| `PORT` | `9180` |
| `CLAUDE_BIN` | autodetected from `~/.local/bin/claude` or `$PATH` |
| `DEFAULT_MODEL` | `sonnet` |

## Hermes integration

In `~/.hermes/config.yaml`:

```yaml
model:
  default: sonnet
  provider: custom
  base_url: http://localhost:9180/v1
  api_key: bridge   # any non-empty string; bridge ignores it
```

Then `systemctl --user restart hermes-gateway`. Hermes now routes through your Claude subscription instead of api.anthropic.com.

## Limitations

- **No streaming yet.** Add via `--output-format stream-json`.
- **Tool definitions are dropped.** `claude` CLI has its own built-in tools — it doesn't accept tool schemas from callers. Calls to `tool_calls` in your incoming request are silently dropped. Hermes' skills that depend on tool injection won't fire here; Claude uses its own Read/Grep/Bash tools instead.
- **No auth.** Trust your localhost binding. Don't expose the port.

## License

MIT.
