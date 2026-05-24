// claude-bridge — OpenAI-compatible HTTP shim over the `claude` CLI.
//
// Why: Hermes Agent (and other tools) speak the OpenAI Chat Completions API.
// Anthropic's own `claude` CLI uses your Claude Code subscription (Max plan
// base allowance) instead of API credits / extra-usage credits. This shim
// glues them together: HTTP request in → `claude -p` subprocess → HTTP
// response out. Hermes never knows it's talking to a CLI.
//
// What it supports:
//   POST /v1/chat/completions  — non-streaming. Translates Hermes messages
//                                into a single prompt + system prompt and
//                                runs `claude -p --output-format json`.
//   GET  /v1/models            — static list (sonnet, opus, haiku).
//   GET  /api/health           — process health.
//
// What it doesn't do (yet):
//   - Streaming. Hermes accepts non-streaming responses fine but UX is
//     slower. Add later via `--output-format stream-json`.
//   - Tool calls. The `claude` CLI doesn't accept tool definitions from
//     callers — it has its own built-in tools. Any `tools:` array in the
//     incoming request is dropped at the wire. Hermes won't see tool_calls
//     in responses. (Workaround: claude's own Read/Grep tools can search
//     the vault directly when prompted to.)
//   - Auth. The shim trusts its localhost binding. Anyone who can reach
//     the port can spend your Claude subscription. Don't expose it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	port := envDefault("PORT", "9180")
	claudeBin := envDefault("CLAUDE_BIN", findClaudeBin())
	defaultModel := envDefault("DEFAULT_MODEL", "sonnet")

	if claudeBin == "" {
		log.Fatalf("[claude-bridge] could not locate `claude` binary. Set CLAUDE_BIN=/path/to/claude or install Claude Code.")
	}
	log.Printf("[claude-bridge] using claude binary: %s", claudeBin)

	srv := &server{
		claudeBin:    claudeBin,
		defaultModel: defaultModel,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", srv.health)
	mux.HandleFunc("GET /v1/models", srv.models)
	mux.HandleFunc("POST /v1/chat/completions", srv.chatCompletions)
	mux.HandleFunc("GET /", srv.root)

	httpSrv := &http.Server{Addr: ":" + port, Handler: mux}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("[claude-bridge] ready at http://localhost:%s  (default model: %s)", port, defaultModel)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[claude-bridge] server failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("[claude-bridge] shutdown")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}

// ─── server ────────────────────────────────────────────────────────────

type server struct {
	claudeBin    string
	defaultModel string
}

func (s *server) root(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "claude-bridge — OpenAI shim over Claude CLI\n\nEndpoints:\n  GET  /api/health\n  GET  /v1/models\n  POST /v1/chat/completions\n")
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"status":     "ok",
		"service":    "claude-bridge",
		"claude_bin": s.claudeBin,
	})
}

func (s *server) models(w http.ResponseWriter, r *http.Request) {
	ids := []string{"sonnet", "opus", "haiku", "claude-sonnet-4-6", "claude-opus-4-7", "claude-haiku-4-5"}
	data := make([]map[string]any, len(ids))
	for i, id := range ids {
		data[i] = map[string]any{
			"id":       id,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "anthropic",
		}
	}
	writeJSON(w, 200, map[string]any{"object": "list", "data": data})
}

// ─── chat completions ──────────────────────────────────────────────────

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []any           `json:"tools,omitempty"`
	Stream   bool            `json:"stream,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type claudeJSONResult struct {
	Type         string  `json:"type"`
	Subtype      string  `json:"subtype"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	StopReason   string  `json:"stop_reason"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (s *server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var req openAIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", "could not parse JSON body: "+err.Error())
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, 400, "invalid_request", "messages array is required and non-empty")
		return
	}

	model := req.Model
	if model == "" {
		model = s.defaultModel
	}

	systemPrompt, userPrompt := flattenMessages(req.Messages)

	args := []string{
		"-p",
		"--print",
		"--output-format", "json",
		"--model", model,
	}
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.claudeBin, args...)
	cmd.Stdin = strings.NewReader(userPrompt)

	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		log.Printf("[claude-bridge] claude failed: %v stderr=%q", err, stderr)
		writeError(w, 502, "upstream_error",
			fmt.Sprintf("claude CLI failed: %v — stderr: %s", err, stderr))
		return
	}

	var result claudeJSONResult
	if err := json.Unmarshal(out, &result); err != nil {
		log.Printf("[claude-bridge] could not parse claude JSON: %v (raw: %s)", err, truncate(string(out), 500))
		writeError(w, 502, "parse_error", "claude output was not valid JSON: "+err.Error())
		return
	}
	if result.IsError {
		writeError(w, 502, "claude_error", "claude returned error: "+result.Subtype)
		return
	}

	resp := map[string]any{
		"id":      "chatcmpl-" + result.SessionID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": result.Result,
				},
				"finish_reason": mapStopReason(result.StopReason),
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     result.Usage.InputTokens,
			"completion_tokens": result.Usage.OutputTokens,
			"total_tokens":      result.Usage.InputTokens + result.Usage.OutputTokens,
		},
		"_bridge": map[string]any{
			"cost_usd":    result.TotalCostUSD,
			"session_id":  result.SessionID,
			"stop_reason": result.StopReason,
		},
	}

	log.Printf("[claude-bridge] %s: in=%d out=%d cost=$%.4f stop=%s",
		model, result.Usage.InputTokens, result.Usage.OutputTokens,
		result.TotalCostUSD, result.StopReason)

	writeJSON(w, 200, resp)
}

func flattenMessages(msgs []openAIMessage) (system, prompt string) {
	var sysBuf, transcriptBuf strings.Builder
	lastUserIdx := -1
	for i, m := range msgs {
		if m.Role == "user" {
			lastUserIdx = i
		}
	}
	for i, m := range msgs {
		text := stringifyContent(m.Content)
		if text == "" {
			continue
		}
		if m.Role == "system" {
			if sysBuf.Len() > 0 {
				sysBuf.WriteString("\n\n")
			}
			sysBuf.WriteString(text)
			continue
		}
		if i == lastUserIdx {
			continue
		}
		if transcriptBuf.Len() > 0 {
			transcriptBuf.WriteString("\n\n")
		}
		transcriptBuf.WriteString(strings.ToUpper(m.Role))
		transcriptBuf.WriteString(":\n")
		transcriptBuf.WriteString(text)
	}
	var finalUser string
	if lastUserIdx >= 0 {
		finalUser = stringifyContent(msgs[lastUserIdx].Content)
	}
	if transcriptBuf.Len() > 0 {
		prompt = "Prior conversation:\n" + transcriptBuf.String() + "\n\n---\n\nCurrent message:\n" + finalUser
	} else {
		prompt = finalUser
	}
	return sysBuf.String(), prompt
}

func stringifyContent(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if m["type"] == "text" {
					if t, ok := m["text"].(string); ok {
						b.WriteString(t)
					}
				}
			}
		}
		return b.String()
	}
	return ""
}

func mapStopReason(claude string) string {
	switch claude {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func findClaudeBin() string {
	candidates := []string{
		os.ExpandEnv("$HOME/.local/bin/claude"),
		"/usr/local/bin/claude",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errType, msg string) {
	writeJSON(w, code, map[string]any{
		"error": map[string]any{
			"type":    errType,
			"message": msg,
			"code":    code,
		},
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
