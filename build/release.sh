#!/bin/sh
set -eu

version="${1:-0.1.0-alpha}"
mkdir -p dist

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o dist/myapp-windows-amd64.exe ./cmd/myapp
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o dist/myapp-macos-arm64 ./cmd/myapp
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o dist/myapp-linux-amd64 ./cmd/myapp
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=${version}" -o dist/myapp-linux-arm64 ./cmd/myapp
go run ./build/release -dist dist -version "$version"
sha256sum dist/MyApp-* > dist/SHA256SUMS
