#!/usr/bin/env bash
# claude-bridge — kill → compile → start → poll /api/health
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$PROJECT_DIR/claude-bridge"
PID_FILE="$PROJECT_DIR/claude-bridge.pid"
PORT="${PORT:-9180}"
LOG_FILE="$PROJECT_DIR/claude-bridge.log"

# Ensure Go toolchain is on PATH regardless of how this script was invoked.
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

echo "[claude-bridge] stopping..."
if [[ -f "$PID_FILE" ]]; then
  OLD_PID="$(cat "$PID_FILE")"
  kill "$OLD_PID" 2>/dev/null && sleep 0.5 || true
fi
# basename match handles both ./binary and /absolute/path invocations
pkill -f "$(basename "$BINARY")" 2>/dev/null && sleep 0.3 || true

echo "[claude-bridge] compiling..."
cd "$PROJECT_DIR"
go build -o "$BINARY" ./cmd/claude-bridge

echo "[claude-bridge] starting on port $PORT..."
PORT="$PORT" nohup "$BINARY" > "$LOG_FILE" 2>&1 &
NEW_PID=$!
echo "$NEW_PID" > "$PID_FILE"

echo "[claude-bridge] waiting for /api/health..."
for i in $(seq 1 30); do
  sleep 0.5
  if ! kill -0 "$NEW_PID" 2>/dev/null; then
    echo "✗ Process $NEW_PID died (port conflict or crash)"
    tail -5 "$LOG_FILE" 2>/dev/null | sed 's/^/  /' || true
    exit 1
  fi
  if curl -sf "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
    echo "✓ Server ready at http://localhost:$PORT"
    exit 0
  fi
done
echo "✗ Server did not start — check $LOG_FILE"
exit 1
