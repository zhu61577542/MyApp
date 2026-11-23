#!/bin/sh
set -eu

target="${TARGET:-linux}"
arch="${ARCH:-amd64}"
version="${VERSION:-0.1.4-alpha}"
app_version="${version%%-*}"
app_build="${APP_BUILD:-1}"
cache="${PWD}/.fyne-cache"

go install github.com/fyne-io/fyne-cross@v1.6.3
set -- /go/bin/fyne-cross "$target" \
	-arch="$arch" \
	-cache="$cache" \
	-name=MyApp \
	-app-id=local.myapp.client \
	-app-version="$app_version" \
	-app-build="$app_build" \
	-tags=gui \
	-env=GOTOOLCHAIN=auto \
	-icon=images/generated/myapp.png \
	"${PWD}/cmd/myapp"
if [ "$target" = "darwin" ]; then
	: "${MACOS_SDK_PATH:?darwin 构建需要设置 MACOS_SDK_PATH，并将该路径挂载到 Docker}"
	set -- "$@" -macosx-sdk-path="$MACOS_SDK_PATH"
fi
"$@"
if [ "$target" = "darwin" ]; then
	patched=0
	for app in fyne-cross/dist/darwin-*/MyApp.app; do
		[ -d "$app" ] || continue
		go run ./build/macpermissions -app "$app"
		patched=$((patched + 1))
	done
	if [ "$patched" -eq 0 ]; then
		echo "未找到生成的 MyApp.app，无法登记权限用途" >&2
		exit 1
	fi
fi
