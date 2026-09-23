#!/bin/sh
set -eu

root="/tmp/myapp-three-$$"
mkdir -p "$root/main" "$root/one" "$root/two"
pids=""
cleanup() {
    status=$?
    if [ "$status" -ne 0 ]; then
        for log in "$root"/*.log; do
            [ ! -f "$log" ] || cat "$log"
        done
    fi
    for pid in $pids; do kill "$pid" 2>/dev/null || true; done
    rm -rf "$root"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

go build -o "$root/myapp" ./cmd/myapp
for name in main one two; do
    "$root/myapp" identity init -dir "$root/$name/identity" -name "$name" > "$root/$name/id"
    role=agent
    [ "$name" != main ] || role=controller
    "$root/myapp" config init -path "$root/$name/config.json" -name "$name" -role "$role" >/dev/null
done
sed -i 's/:24800/:24801/' "$root/two/config.json"
one_id=$(cat "$root/one/id")
two_id=$(cat "$root/two/id")
sed -i "s/\"max_devices\": 3,/\"max_devices\": 3, \"peers\": [{\"id\": \"$one_id\", \"address\": \"127.0.0.1:24800\"}, {\"id\": \"$two_id\", \"address\": \"127.0.0.1:24801\"}],/" "$root/main/config.json"

for name in one two; do
    printf 'yes\n' | "$root/myapp" pair listen -addr 127.0.0.1:24810 -identity-dir "$root/$name/identity" -trust-dir "$root/$name/trust" > "$root/pair-$name.log" 2>&1 &
    pair_pid=$!
    pids="$pids $pair_pid"
    sleep 0.2
    printf 'yes\n' | "$root/myapp" pair connect -addr 127.0.0.1:24810 -identity-dir "$root/main/identity" -trust-dir "$root/main/trust" > "$root/pair-main-$name.log" 2>&1
    wait "$pair_pid"
done
for display in 97 98 99; do
    Xvfb ":$display" -screen 0 1024x768x24 > "$root/xvfb-$display.log" 2>&1 &
    pids="$pids $!"
    attempt=0
    while [ ! -S "/tmp/.X11-unix/X$display" ]; do
        attempt=$((attempt + 1))
        [ "$attempt" -lt 100 ] || exit 1
        sleep 0.05
    done
done
for name in one two; do
    display=:98
    [ "$name" != two ] || display=:97
    DISPLAY="$display" xev -geometry 320x200+20+20 > "$root/xev-$name.log" 2>&1 &
    pids="$pids $!"
    sleep 0.2
    window=$(DISPLAY="$display" xdotool search --name 'Event Tester' | head -n 1)
    DISPLAY="$display" xdotool windowfocus "$window"
    DISPLAY="$display" "$root/myapp" serve -config "$root/$name/config.json" -identity-dir "$root/$name/identity" -trust-dir "$root/$name/trust" > "$root/serve-$name.log" 2>&1 &
    serve_pid=$!
    pids="$pids $serve_pid"
    [ "$name" != one ] || one_pid=$serve_pid
done
sleep 0.3
DISPLAY=:99 "$root/myapp" control -config "$root/main/config.json" -identity-dir "$root/main/identity" -trust-dir "$root/main/trust" > "$root/control.log" 2>&1 &
control_pid=$!
pids="$pids $control_pid"
sleep 0.8
DISPLAY=:99 xdotool key a
DISPLAY=:99 xdotool key ctrl+alt+2
DISPLAY=:99 xdotool key b
sleep 0.2
grep -q 'keysym 0x61, a' "$root/xev-one.log"
grep -q 'keysym 0x62, b' "$root/xev-two.log"
if grep -q 'keysym 0x62, b' "$root/xev-one.log"; then exit 1; fi
if grep -q 'keysym 0x61, a' "$root/xev-two.log"; then exit 1; fi

wait_text() {
    display="$1"
    expected="$2"
    attempt=0
    while [ "$(DISPLAY="$display" xclip -selection clipboard -out 2>/dev/null || true)" != "$expected" ]; do
        attempt=$((attempt + 1))
        [ "$attempt" -lt 60 ] || return 1
        sleep 0.1
    done
}
printf '子电脑一的中文' | DISPLAY=:98 xclip -selection clipboard -in
wait_text :99 '子电脑一的中文'
wait_text :97 '子电脑一的中文'
printf '子电脑二的回复' | DISPLAY=:97 xclip -selection clipboard -in
wait_text :99 '子电脑二的回复'
wait_text :98 '子电脑二的回复'
printf '主电脑广播' | DISPLAY=:99 xclip -selection clipboard -in
wait_text :98 '主电脑广播'
wait_text :97 '主电脑广播'

mkdir -p "$root/共享目录/空目录"
printf '三机文件内容' > "$root/共享目录/中文.txt"
: > "$root/共享目录/empty.txt"
printf 'file://%s/共享目录\r\n' "$root" | DISPLAY=:98 xclip -selection clipboard -t text/uri-list -in
attempt=0
while :; do
    received=$(DISPLAY=:97 xclip -selection clipboard -t text/uri-list -out 2>/dev/null | tr -d '\r\n' || true)
    case "$received" in "file://$root/two/cache/files/"*) break ;; esac
    attempt=$((attempt + 1))
    [ "$attempt" -lt 60 ] || exit 1
    sleep 0.1
done
# 文件 URL 可能编码中文，因此通过接收缓存验证内容。
received_file=$(find "$root/two/cache/files" -name '中文.txt' -type f | head -n 1)
cmp "$root/共享目录/中文.txt" "$received_file"
test -d "$(dirname "$received_file")/空目录"
test -f "$(dirname "$received_file")/empty.txt"
DISPLAY=:99 xdotool key ctrl+alt+Escape
wait "$control_pid"
printf 'PASS: three CLI nodes paired; target switch routed a/b separately; bidirectional text and agent-to-agent directory relay passed; emergency exit completed\n'

DISPLAY=:99 "$root/myapp" control -config "$root/main/config.json" -identity-dir "$root/main/identity" -trust-dir "$root/main/trust" > "$root/fault.log" 2>&1 &
fault_pid=$!
pids="$pids $fault_pid"
attempt=0
while ! grep -q '已连接 2 台' "$root/fault.log"; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.1
done
kill "$one_pid"
attempt=0
while kill -0 "$fault_pid" 2>/dev/null; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 20 ] || exit 1
    sleep 0.1
done
if wait "$fault_pid"; then
    printf 'FAIL: lost connection reported as successful exit\n'
    exit 1
fi
printf 'PASS: losing one agent stopped the controller group and reported failure\n'
