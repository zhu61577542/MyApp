//go:build !gui

package main

import (
	"io"
)

func runWithoutArgs(stdin io.Reader, stdout, stderr io.Writer) int {
	usage(stderr)
	return 2
}

func runNativeGUI(args []string, stdout, stderr io.Writer) error {
	_, _ = io.WriteString(stderr, "当前构建未包含原生 GUI，使用 Web 诊断界面；桌面发行包需使用 GUI 构建镜像。\n")
	return runGUI(args, stdout, stderr)
}
