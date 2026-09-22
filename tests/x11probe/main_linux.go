//go:build linux

package main

import (
	"fmt"
	"os"

	common "myapp/internal/input"
	linuxinput "myapp/internal/platform/linux/input"
)

func main() {
	injector, err := linuxinput.New("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer injector.Close()
	events := []common.Event{
		{Kind: common.MouseAbsolute, Sequence: 1, X: 120, Y: 200},
		{Kind: common.KeyDown, Sequence: 2, Code: 4},
		{Kind: common.KeyUp, Sequence: 3, Code: 4},
	}
	for _, event := range events {
		if err := injector.Inject(event); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println("MyApp X11 injector completed")
}
