package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	directory := flag.String("dist", "dist", "发布目录")
	version := flag.String("version", "0.1.0-alpha", "版本")
	flag.Parse()
	must(packageWindows(*directory, *version))
	must(packageMac(*directory, *version))
	must(packageLinux(*directory, *version, "amd64"))
	must(packageLinux(*directory, *version, "arm64"))
}

func packageWindows(directory, version string) error {
	output, err := os.Create(filepath.Join(directory, "MyApp-"+version+"-windows-amd64.zip"))
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	files := []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{"MyApp/myapp.exe", 0o755, mustRead(filepath.Join(directory, "myapp-windows-amd64.exe"))},
		{"MyApp/install.ps1", 0o644, []byte(windowsInstall)},
		{"MyApp/uninstall.ps1", 0o644, []byte(windowsUninstall)},
		{"MyApp/README.txt", 0o644, []byte(releaseReadme)},
	}
	for _, file := range files {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate}
		header.SetMode(file.mode)
		header.Modified = time.Unix(0, 0).UTC()
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if _, err := writer.Write(file.data); err != nil {
			return err
		}
	}
	return joinClose(archive.Close(), output.Close())
}

func packageMac(directory, version string) error {
	files := []tarFile{
		{"MyApp.app/Contents/MacOS/myapp", 0o755, mustRead(filepath.Join(directory, "myapp-macos-arm64"))},
		{"MyApp.app/Contents/Info.plist", 0o644, []byte(fmt.Sprintf(infoPlist, version, version))},
		{"MyApp.app/Contents/Resources/README.txt", 0o644, []byte(releaseReadme)},
	}
	return writeTarGZ(filepath.Join(directory, "MyApp-"+version+"-macos-arm64.tar.gz"), files)
}

func packageLinux(directory, version, architecture string) error {
	files := []tarFile{
		{"MyApp/myapp", 0o755, mustRead(filepath.Join(directory, "myapp-linux-"+architecture))},
		{"MyApp/install.sh", 0o755, []byte(linuxInstall)},
		{"MyApp/uninstall.sh", 0o755, []byte(linuxUninstall)},
		{"MyApp/README.txt", 0o644, []byte(releaseReadme)},
	}
	return writeTarGZ(filepath.Join(directory, "MyApp-"+version+"-linux-"+architecture+".tar.gz"), files)
}

type tarFile struct {
	name string
	mode int64
	data []byte
}

func writeTarGZ(filename string, files []tarFile) error {
	output, err := os.Create(filename)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(output)
	archive := tar.NewWriter(compressed)
	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.data)), ModTime: time.Unix(0, 0).UTC()}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if _, err := archive.Write(file.data); err != nil {
			return err
		}
	}
	return joinClose(archive.Close(), compressed.Close(), output.Close())
}

func mustRead(filename string) []byte {
	data, err := os.ReadFile(filename)
	must(err)
	return data
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func joinClose(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

const releaseReadme = `MyApp 0.1 alpha

这是开发验证版本。请先运行 identity init、config init 和 pair，再在受控电脑运行 serve，在主电脑运行 control。
macOS 需要在系统设置中授予辅助功能和输入监控权限。Linux X11 需要 xclip；Wayland 需要 wl-clipboard，键鼠控制当前要求 X11。
`

const windowsInstall = `$ErrorActionPreference = "Stop"
$target = Join-Path $env:LOCALAPPDATA "MyApp"
New-Item -ItemType Directory -Force -Path $target | Out-Null
Copy-Item (Join-Path $PSScriptRoot "myapp.exe") (Join-Path $target "myapp.exe") -Force
Write-Host "MyApp installed to $target"
`

const windowsUninstall = `$ErrorActionPreference = "Stop"
$target = Join-Path $env:LOCALAPPDATA "MyApp"
Remove-Item $target -Recurse -Force -ErrorAction SilentlyContinue
Write-Host "MyApp removed"
`

const linuxInstall = `#!/bin/sh
set -eu
target="${HOME}/.local/bin"
mkdir -p "$target"
cp "$(dirname "$0")/myapp" "$target/myapp"
chmod 755 "$target/myapp"
printf 'MyApp installed to %s\n' "$target/myapp"
`

const linuxUninstall = `#!/bin/sh
set -eu
rm -f "${HOME}/.local/bin/myapp"
printf 'MyApp removed\n'
`

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>myapp</string>
<key>CFBundleIdentifier</key><string>local.myapp.client</string>
<key>CFBundleName</key><string>MyApp</string>
<key>CFBundleDisplayName</key><string>MyApp</string>
<key>CFBundleVersion</key><string>%s</string>
<key>CFBundleShortVersionString</key><string>%s</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSAccessibilityUsageDescription</key><string>MyApp 使用辅助功能控制已配对电脑的鼠标和键盘。</string>
<key>NSInputMonitoringUsageDescription</key><string>MyApp 读取键盘和鼠标输入并发送到已配对电脑。</string>
</dict></plist>
`
