package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func patch(data []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	text := string(data)
	if !regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>local\.myapp\.client</string>`).MatchString(text) {
		return nil, errors.New("应用标识不是 local.myapp.client")
	}
	for _, item := range [][2]string{
		{"NSAccessibilityUsageDescription", "MyApp 使用辅助功能控制已配对电脑的鼠标和键盘。"},
		{"NSInputMonitoringUsageDescription", "MyApp 读取键盘和鼠标输入并发送到已配对电脑。"},
		{"NSLocalNetworkUsageDescription", "MyApp 搜索并连接同一局域网内由你确认配对的电脑。"},
	} {
		re := regexp.MustCompile(`<key>` + item[0] + `</key>\s*<string>(?s:.*?)</string>`)
		var escaped bytes.Buffer
		xml.EscapeText(&escaped, []byte(item[1]))
		entry := "<key>" + item[0] + "</key><string>" + escaped.String() + "</string>"
		if re.MatchString(text) {
			text = re.ReplaceAllString(text, entry)
			continue
		}
		index := strings.LastIndex(text, "</dict>")
		if index < 0 {
			return nil, errors.New("缺少应用属性字典")
		}
		text = text[:index] + entry + "\n" + text[index:]
	}
	return []byte(text), nil
}

func main() {
	app := flag.String("app", "", "MyApp.app 路径")
	flag.Parse()
	if *app == "" {
		fmt.Fprintln(os.Stderr, "必须提供 -app")
		os.Exit(1)
	}
	filename := filepath.Join(*app, "Contents", "Info.plist")
	data, err := os.ReadFile(filename)
	if err == nil {
		data, err = patch(data)
	}
	if err == nil {
		err = os.WriteFile(filename, data, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
