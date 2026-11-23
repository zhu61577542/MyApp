//go:build gui

package nativegui

import (
	"context"
	"fmt"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/identity"
	"myapp/internal/trust"
)

func openPairing(application fyne.App, options Options, manager *pairingManager) {
	manager.mu.Lock()
	show := manager.showWindow
	manager.mu.Unlock()
	if show != nil {
		show()
		return
	}
	window := application.NewWindow("MyApp · 添加电脑")
	window.Resize(fyne.NewSize(620, 560))
	local, _ := identity.Load(options.IdentityDirectory)
	browser := startBrowser(context.Background(), local.DeviceID, manager)
	holder := container.NewStack()
	message := paragraph("")
	var manual bool
	address := widget.NewEntry()
	address.SetPlaceHolder("192.168.1.20 或 192.168.1.20:24802")
	var render func()
	var signature string
	var pausedBrowser bool
	var hidden, transition bool
	ensureListening := func() {
		current := manager.snapshot()
		if current.Running || strings.HasPrefix(current.Status, "配对完成") {
			return
		}
		cfg, err := config.Load(options.ConfigPath)
		if err != nil {
			return
		}
		records, err := trust.New(options.TrustDirectory).List()
		if err == nil && len(records)+1 < cfg.MaxDevices {
			if err := manager.start(options, "listen", defaultPairAddress(options)); err != nil {
				message.SetText("无法允许配对：" + err.Error())
			}
		}
	}
	start := func(mode, target string) {
		if transition {
			return
		}
		current := manager.snapshot()
		if mode == "connect" && current.Running && current.Mode == "listen" && current.Code == "" {
			transition = true
			go func() {
				ctx, done := context.WithTimeout(context.Background(), 4*time.Second)
				defer done()
				err := manager.stopAndWait(ctx)
				if err == nil {
					err = manager.start(options, mode, target)
				}
				fyne.Do(func() {
					transition = false
					if err != nil {
						message.SetText("无法连接：" + pairingOutputMessage(err.Error()))
					}
					signature = ""
					render()
				})
			}()
			return
		}
		if err := manager.start(options, mode, target); err != nil {
			message.SetText("无法开始配对：" + pairingOutputMessage(err.Error()))
		}
		signature = ""
		render()
	}
	stopPairing := func() {
		snapshot := manager.snapshot()
		doStop := func() { manager.stop(); message.SetText("正在停止配对任务…"); signature = ""; render() }
		if snapshot.Confirmed {
			dialog.ShowConfirm("停止等待另一台电脑？", "若双方已经完成确认，停止任务不会撤销已建立的信任。你可以在权限与安全中检查和撤销。", func(ok bool) {
				if ok {
					doStop()
				}
			}, window)
		} else {
			doStop()
		}
	}
	render = func() {
		snapshot := manager.snapshot()
		if hidden && !snapshot.Running && !pausedBrowser {
			browser.stop()
			pausedBrowser = true
		}
		cfg, cfgErr := config.Load(options.ConfigPath)
		records, trustErr := trust.New(options.TrustDirectory).List()
		nearby, failure := browser.snapshot()
		remaining := 0
		if !snapshot.Deadline.IsZero() {
			remaining = int(math.Max(0, math.Ceil(time.Until(snapshot.Deadline).Seconds())))
		}
		var deviceKey strings.Builder
		for _, device := range nearby {
			deviceKey.WriteString(device.ID + device.Address)
		}
		key := fmt.Sprintf("%t/%s/%s/%t/%d/%s/%t/%d/%s/%s/%s/%t", snapshot.Running, snapshot.Status, snapshot.Code, snapshot.Confirmed, remaining, deviceKey.String(), manual, len(records), cfg.Role, failure, snapshot.DiscoveryError, transition)
		if key == signature {
			return
		}
		signature = key
		box := container.NewVBox()
		if cfgErr != nil {
			box.Add(heading("请先设置这台电脑"))
			box.Add(paragraph("回到主窗口，为设备命名并选择用途后再添加电脑。"))
		} else if trustErr != nil {
			box.Add(heading("无法读取信任状态"))
			box.Add(paragraph(trustErr.Error()))
		} else if snapshot.Running && snapshot.Code != "" {
			title := "核对两台电脑的号码"
			if snapshot.Confirmed {
				title = "等待另一台电脑确认"
			}
			box.Add(heading(title))
			name := snapshot.RemoteName
			if name == "" {
				name = "另一台电脑"
			}
			box.Add(paragraph("正在与“" + name + "”配对"))
			code := widget.NewRichText(&widget.TextSegment{Text: snapshot.Code[:3] + "  " + snapshot.Code[3:], Style: widget.RichTextStyle{Alignment: fyne.TextAlignCenter, TextStyle: fyne.TextStyle{Monospace: true, Bold: true}, SizeName: theme.SizeNameHeadingText}})
			box.Add(surface(container.NewPadded(code)))
			box.Add(paragraph("两台电脑分别确认同一号码。号码不一致时，请停止配对。"))
			if snapshot.Confirmed {
				box.Add(secondary("本机已确认；对端确认前不会建立信任。"))
			} else {
				box.Add(primary("号码一致，确认配对", func() {
					if err := manager.confirm(); err != nil {
						message.SetText(err.Error())
					}
					signature = ""
					render()
				}))
			}
			box.Add(secondary(fmt.Sprintf("本次配对剩余约 %d 秒", remaining)))
			box.Add(widget.NewButton("停止配对", stopPairing))
		} else if snapshot.Running && snapshot.Mode != "listen" {
			title := "正在连接另一台电脑"
			if snapshot.Mode == "listen" {
				title = "允许其他电脑发现我"
			}
			box.Add(heading(title))
			box.Add(paragraph("在另一台电脑打开 MyApp → 添加电脑，选择本机。连接成功后，两端会显示同一配对码。"))
			if snapshot.Mode == "listen" {
				box.Add(widget.NewAccordion(widget.NewAccordionItem("也可以通过 IP 添加", paragraph(localPairAddresses(snapshot.Address)))))
			}
			box.Add(secondary(fmt.Sprintf("本次允许配对剩余约 %d 秒；关闭窗口后任务继续。", remaining)))
			box.Add(widget.NewButton("停止配对", stopPairing))
		} else if strings.HasPrefix(snapshot.Status, "配对完成") {
			box.Add(heading("电脑已添加"))
			box.Add(paragraph(snapshot.Status))
			box.Add(secondary("配对不会自动捕获键鼠。回到设备页，确认位置并主动开始控制。"))
			box.Add(primary("完成", func() {
				window.Hide()
				if !pausedBrowser {
					browser.stop()
					pausedBrowser = true
				}
			}))
			box.Add(quiet("继续添加电脑", func() {
				manager.mu.Lock()
				manager.status = ""
				manager.lines = nil
				manager.mu.Unlock()
				ensureListening()
				signature = ""
				render()
			}))
		} else {
			box.Add(heading("添加你的另一台电脑"))
			box.Add(paragraph("两台电脑需在同一局域网，并在两端分别确认配对码。"))
			if snapshot.Running {
				box.Add(secondary(fmt.Sprintf("本机已允许配对，剩余约 %d 秒。双方都可以搜索并选择对方。", remaining)))
				box.Add(quiet("停止允许配对", stopPairing))
			}
			if snapshot.DiscoveryError != "" {
				box.Add(secondary(snapshot.DiscoveryError))
			}
			if snapshot.Status != "" && !snapshot.Running {
				box.Add(paragraph(snapshot.Status))
				if snapshot.Status == "配对未完成" {
					box.Add(paragraph(pairingFailureMessage(strings.Join(snapshot.Lines, "\n"))))
				}
			}
			if len(records)+1 >= cfg.MaxDevices {
				box.Add(paragraph("已达到最多三台电脑，包含本机。请先在权限与安全中撤销不再使用的电脑。"))
			} else {
				box.Add(sectionTitle("附近允许配对的电脑"))
				found := false
				for _, device := range nearby {
					device := device
					known := false
					for _, record := range records {
						if record.DeviceID == device.ID {
							known = true
						}
					}
					if known {
						continue
					}
					found = true
					pair := primary("配对", func() { start("connect", device.Address) })
					if transition {
						pair.Disable()
					}
					note := roleName(device.Role)
					if device.Role == cfg.Role {
						pair.Disable()
						note += " · 需将一端设为子电脑，另一端设为主电脑"
					}
					box.Add(surface(container.NewBorder(nil, nil, nil, pair, container.NewVBox(sectionTitle(device.Name), secondary(note)))))
				}
				if !found {
					box.Add(secondary("暂未找到。请在另一台电脑打开“添加电脑”；程序会同时监听和搜索。组播受限时会尝试主动发现，也可通过 IP 添加。"))
				}
				if failure != "" {
					box.Add(secondary(failure))
				}
				box.Add(container.NewHBox(widget.NewButton("重新查找", func() {
					browser.stop()
					browser = startBrowser(context.Background(), local.DeviceID, manager)
					pausedBrowser = false
					ensureListening()
					signature = ""
					render()
				}), widget.NewButton("重新允许配对", func() {
					if !manager.snapshot().Running {
						start("listen", defaultPairAddress(options))
					}
				})))
				if manual {
					box.Add(widget.NewForm(widget.NewFormItem("对端地址", address)))
					box.Add(primary("连接并核对配对码", func() {
						target, err := normalizePairAddress(address.Text)
						if err != nil {
							message.SetText(err.Error())
							return
						}
						start("connect", target)
					}))
				} else {
					box.Add(quiet("通过 IP 添加", func() { manual = true; signature = ""; render() }))
				}
			}
		}
		box.Add(message)
		holder.Objects = []fyne.CanvasObject{box}
		holder.Refresh()
	}
	closed := make(chan struct{})
	var closeOnce sync.Once
	manager.mu.Lock()
	manager.window = window
	manager.showWindow = func() {
		hidden = false
		if pausedBrowser {
			browser = startBrowser(context.Background(), local.DeviceID, manager)
			pausedBrowser = false
		}
		ensureListening()
		signature = ""
		render()
		window.Show()
		window.RequestFocus()
	}
	manager.mu.Unlock()
	window.SetCloseIntercept(func() {
		window.Hide()
		hidden = true
		if !pausedBrowser && !manager.snapshot().Running {
			browser.stop()
			pausedBrowser = true
		}
	})
	window.SetOnClosed(func() {
		closeOnce.Do(func() { close(closed) })
		browser.stop()
		manager.mu.Lock()
		manager.window = nil
		manager.showWindow = nil
		manager.mu.Unlock()
	})
	window.SetContent(container.NewPadded(container.NewVScroll(holder)))
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-closed:
				return
			case <-ticker.C:
				fyne.Do(func() {
					if !pausedBrowser {
						render()
					}
				})
			}
		}
	}()
	ensureListening()
	render()
	window.Show()
}

func localPairAddresses(address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	addresses, _ := net.InterfaceAddrs()
	var lines []string
	for _, address := range addresses {
		if item, ok := address.(*net.IPNet); ok && !item.IP.IsLoopback() && item.IP.To4() != nil {
			lines = append(lines, net.JoinHostPort(item.IP.String(), port))
		}
	}
	if len(lines) == 0 {
		return "未检测到局域网地址，请检查网络连接。"
	}
	return strings.Join(lines, "\n")
}
