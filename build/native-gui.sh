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
