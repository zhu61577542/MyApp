#!/bin/sh
set -eu

root="/tmp/myapp-e2e-$$"
mkdir -p "$root/controller" "$root/agent"
agent_pid=""
control_pid=""
xvfb_agent_pid=""
xvfb_controller_pid=""
xev_pid=""

cleanup() {
    status=$?
    if [ "$status" -ne 0 ]; then
        for log in pair-agent.log pair-controller.log xvfb-agent.log xvfb-controller.log xev.log serve.log control.log; do
            if [ -f "$root/$log" ]; then
                printf '%s\n' "--- $log"
                cat "$root/$log"
            fi
        done
    fi
    for pid in "$control_pid" "$agent_pid" "$xev_pid" "$xvfb_controller_pid" "$xvfb_agent_pid"; do
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    rm -rf "$root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

go build -o "$root/myapp" ./cmd/myapp
controller_id=$("$root/myapp" identity init -dir "$root/controller/identity" -name controller)
agent_id=$("$root/myapp" identity init -dir "$root/agent/identity" -name agent)
"$root/myapp" config init -path "$root/controller/config.json" -name controller -role controller >/dev/null
"$root/myapp" config init -path "$root/agent/config.json" -name agent -role agent >/dev/null

printf 'yes\n' | "$root/myapp" pair listen -addr 127.0.0.1:24810 -identity-dir "$root/agent/identity" -trust-dir "$root/agent/trust" >"$root/pair-agent.log" 2>&1 &
pair_pid=$!
sleep 0.2
printf 'yes\n' | "$root/myapp" pair connect -addr 127.0.0.1:24810 -identity-dir "$root/controller/identity" -trust-dir "$root/controller/trust" >"$root/pair-controller.log" 2>&1
wait "$pair_pid"

Xvfb :98 -screen 0 1024x768x24 >"$root/xvfb-agent.log" 2>&1 &
xvfb_agent_pid=$!
attempt=0
while [ ! -S /tmp/.X11-unix/X98 ]; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 100 ]; then
        cat "$root/xvfb-agent.log"
        exit 1
    fi
    sleep 0.05
done
Xvfb :99 -screen 0 1024x768x24 >"$root/xvfb-controller.log" 2>&1 &
xvfb_controller_pid=$!
attempt=0
while [ ! -S /tmp/.X11-unix/X99 ]; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 100 ]; then
        cat "$root/xvfb-controller.log"
        exit 1
    fi
    sleep 0.05
done

DISPLAY=:98 xev -geometry 320x200+20+20 >"$root/xev.log" 2>&1 &
xev_pid=$!
sleep 0.2
target_window=$(DISPLAY=:98 xdotool search --name 'Event Tester' | head -n 1)
DISPLAY=:98 xdotool windowfocus "$target_window"

DISPLAY=:98 "$root/myapp" serve -config "$root/agent/config.json" -identity-dir "$root/agent/identity" -trust-dir "$root/agent/trust" >"$root/serve.log" 2>&1 &
agent_pid=$!
sleep 0.3
DISPLAY=:99 "$root/myapp" control -config "$root/controller/config.json" -identity-dir "$root/controller/identity" -trust-dir "$root/controller/trust" -peer "$agent_id" -addr 127.0.0.1:24800 >"$root/control.log" 2>&1 &
control_pid=$!
sleep 0.8

DISPLAY=:99 xdotool mousemove 600 400
DISPLAY=:99 xdotool key a
sleep 0.3

printf 'MyApp 中文同步' | DISPLAY=:99 xclip -selection clipboard -in
attempt=0
while [ "$(DISPLAY=:98 xclip -selection clipboard -out 2>/dev/null || true)" != 'MyApp 中文同步' ]; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.1
done
mkdir -p "$root/source/empty"
printf 'file content' > "$root/source/中文.txt"
: > "$root/source/zero.txt"
printf 'file://%s/source\r\n' "$root" | DISPLAY=:99 xclip -selection clipboard -t text/uri-list -in
attempt=0
while :; do
    received_uri=$(DISPLAY=:98 xclip -selection clipboard -t text/uri-list -out 2>/dev/null | tr -d '\r\n' || true)
    case "$received_uri" in
        "file://$root/agent/cache/files/"*) break ;;
    esac
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.1
done
received_path=${received_uri#file://}
cmp "$root/source/中文.txt" "$received_path/中文.txt"
test -d "$received_path/empty"
test -f "$received_path/zero.txt"
printf '反向复制成功' | DISPLAY=:98 xclip -selection clipboard -in
attempt=0
while [ "$(DISPLAY=:99 xclip -selection clipboard -out 2>/dev/null || true)" != '反向复制成功' ]; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.1
done
DISPLAY=:99 xdotool key ctrl+alt+Escape
wait "$control_pid"
control_pid=""

if ! grep -A 8 'KeyPress event' "$root/xev.log" | grep -q 'keysym 0x61, a'; then
    cat "$root/serve.log"
    cat "$root/control.log"
    cat "$root/xev.log"
    exit 1
fi

printf 'PASS: paired controller %s with agent %s and delivered key a through MyApp TLS session\n' "$controller_id" "$agent_id"
printf 'PASS: text and directory clipboard transferred through the CLI session\n'
