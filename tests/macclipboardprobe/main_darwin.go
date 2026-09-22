//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	darwinclipboard "myapp/internal/platform/darwin/clipboard"
)

func main() {
	paths, revision, available, err := darwinclipboard.NewFiles().ReadFiles(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := struct {
		AdapterLoaded bool   `json:"adapter_loaded"`
		Revision      uint64 `json:"revision"`
		FilesPresent  bool   `json:"files_present"`
		FileCount     int    `json:"file_count"`
		PassiveOnly   bool   `json:"passive_only"`
	}{true, revision, available, len(paths), true}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
