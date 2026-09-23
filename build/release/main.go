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
		{"MyApp/myapp-gui.ps1", 0o644, []byte(windowsGUI)},
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
		{"MyApp.app/Contents/MacOS/myapp", 0o755, []byte(macLauncher)},
		{"MyApp.app/Contents/Info.plist", 0o644, []byte(fmt.Sprintf(infoPlist, version, version))},
		{"MyApp.app/Contents/Resources/myapp-bin", 0o755, mustRead(filepath.Join(directory, "myapp-macos-arm64"))},
		{"MyApp.app/Contents/Resources/myapp-gui.command", 0o755, []byte(macGUI)},
		{"MyApp.app/Contents/Resources/README.txt", 0o644, []byte(releaseReadme)},
	}
	return writeTarGZ(filepath.Join(directory, "MyApp-"+version+"-macos-arm64.tar.gz"), files)
}

func packageLinux(directory, version, architecture string) error {
	files := []tarFile{
		{"MyApp/myapp", 0o755, mustRead(filepath.Join(directory, "myapp-linux-"+architecture))},
		{"MyApp/install.sh", 0o755, []byte(linuxInstall)},
		{"MyApp/myapp-gui", 0o755, []byte(linuxGUI)},
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

这是开发验证版本。安装后运行 myapp gui（或随包提供的 myapp-gui 启动脚本）打开控制中心。首次启动可在页面初始化身份和配置；配对仍可通过 CLI 完成。
请先完成配对，再在受控电脑运行 serve，在主电脑运行 control。
macOS 需要在系统设置中授予辅助功能和输入监控权限。Linux X11 需要 xclip；Wayland 需要 wl-clipboard，键鼠控制当前要求 X11。
`

const windowsInstall = `$ErrorActionPreference = "Stop"
$target = Join-Path $env:LOCALAPPDATA "MyApp"
New-Item -ItemType Directory -Force -Path $target | Out-Null
Copy-Item (Join-Path $PSScriptRoot "myapp.exe") (Join-Path $target "myapp.exe") -Force
Set-Content -Path (Join-Path $target "myapp-gui.ps1") -Value ('& "' + $target + '\\myapp.exe" gui')
Write-Host "MyApp installed to $target"
`

const windowsGUI = `& "$env:LOCALAPPDATA\\MyApp\\myapp.exe" gui
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
cat > "$target/myapp-gui" <<'EOF'
#!/bin/sh
exec "$(dirname "$0")/myapp" gui "$@"
EOF
chmod 755 "$target/myapp-gui"
printf 'MyApp installed to %s\n' "$target/myapp"
`

const linuxGUI = `#!/bin/sh
exec "$(dirname "$0")/myapp" gui "$@"
`

const macGUI = `#!/bin/sh
exec "$(dirname "$0")/myapp-bin" gui "$@"
`

const macLauncher = `#!/bin/sh
set -eu
directory="$(dirname "$0")"
"$directory/../Resources/myapp-bin" gui "$@" >/tmp/myapp-gui.log 2>&1 &
pid=$!
open http://127.0.0.1:24880
wait "$pid"
`

const linuxUninstall = `#!/bin/sh
set -eu
rm -f "${HOME}/.local/bin/myapp"
rm -f "${HOME}/.local/bin/myapp-gui"
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
