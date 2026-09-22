#!/bin/sh
set -eu

export DISPLAY=:98
probe_dir=$(mktemp -d /tmp/myapp-native-x11.XXXXXX)
xvfb_pid=
xev_pid=

cleanup() {
    [ -z "$xev_pid" ] || kill "$xev_pid" 2>/dev/null || true
    [ -z "$xvfb_pid" ] || kill "$xvfb_pid" 2>/dev/null || true
    rm -rf "$probe_dir"
}
trap cleanup EXIT

Xvfb :98 -screen 0 800x600x24 -nolisten tcp >"$probe_dir/xvfb.log" 2>&1 &
xvfb_pid=$!

attempt=0
while [ "$attempt" -lt 20 ]; do
    if xdotool getmouselocation >/dev/null 2>&1; then
        break
    fi
    attempt=$((attempt + 1))
    sleep 0.1
done
[ "$attempt" -lt 20 ]

xev -name MyApp-Native-X11-Probe -geometry 240x120+20+20 >"$probe_dir/xev.log" 2>&1 &
xev_pid=$!
window=
attempt=0
while [ "$attempt" -lt 20 ]; do
    if window=$(xdotool search --onlyvisible --limit 1 --name '^MyApp-Native-X11-Probe$' 2>/dev/null); then
        break
    fi
    attempt=$((attempt + 1))
    sleep 0.1
done
[ -n "$window" ]
xdotool windowfocus --sync "$window"

go run ./tests/x11probe
xdotool getmouselocation --shell >"$probe_dir/mouse.txt"
awk -F= '
    $1 == "X" && $2 == 120 { x = 1 }
    $1 == "Y" && $2 == 200 { y = 1 }
    END { exit !(x && y) }
' "$probe_dir/mouse.txt"

attempt=0
while [ "$attempt" -lt 20 ]; do
    if grep -q 'KeyPress event' "$probe_dir/xev.log" && \
       grep -q 'KeyRelease event' "$probe_dir/xev.log" && \
       grep -q '(keysym 0x61, a)' "$probe_dir/xev.log"; then
        break
    fi
    attempt=$((attempt + 1))
    sleep 0.1
done
[ "$attempt" -lt 20 ]
printf '%s\n' 'PASS: MyApp X11 injector moved pointer and delivered a press/release'
