package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"myapp/internal/pointer"
	"os"
	"runtime"
	"time"
)

func runPointerProbe(args []string, output, stderr io.Writer) error {
	set := flag.NewFlagSet("pointer-probe", flag.ContinueOnError)
	set.SetOutput(stderr)
	hide := set.Bool("hide", false, "短暂隐藏并恢复光标")
	duration := set.Duration("duration", 5*time.Second, "隐藏时长，最多 8 秒")
	abrupt := set.Bool("abrupt", false, "隐藏后立即退出，用于看护恢复验证")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *duration < 0 || *duration > 8*time.Second {
		return errors.New("光标探针时长必须在 0 到 8 秒之间")
	}
	if *abrupt && runtime.GOOS != "windows" {
		return errors.New("异常退出探针仅适用于独立 Windows 光标看护")
	}
	backend, err := pointer.New()
	if err != nil {
		return err
	}
	screen, err := backend.Screen()
	if err != nil {
		return err
	}
	position, err := backend.Position()
	if err != nil {
		return err
	}
	inspector, ok := backend.(pointer.Inspector)
	if !ok {
		return errors.New("当前后端没有光标检查接口")
	}
	before, err := inspector.InspectCursor()
	if err != nil {
		return err
	}
	result := map[string]any{"screen": screen, "position": position, "before": before}
	if topology, e := pointer.TopologyFor(backend); e == nil {
		result["topology"] = topology
	}
	if *hide {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		restore, err := backend.Hide(ctx)
		if err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		during, err := inspector.InspectCursor()
		if err != nil {
			restore()
			return err
		}
		result["during"] = during
		if during.Visible {
			_ = restore()
			_ = json.NewEncoder(output).Encode(result)
			return errors.New("原生探针没有确认光标隐藏")
		}
		if *abrupt {
			json.NewEncoder(output).Encode(result)
			os.Exit(23)
		}
		time.Sleep(*duration)
		if err := restore(); err != nil {
			return err
		}
		time.Sleep(150 * time.Millisecond)
		after, err := inspector.InspectCursor()
		if err != nil {
			return err
		}
		result["after"] = after
		if before.Visible && !after.Visible {
			_ = json.NewEncoder(output).Encode(result)
			return errors.New("光标恢复后仍不可见")
		}
	}
	return json.NewEncoder(output).Encode(result)
}
