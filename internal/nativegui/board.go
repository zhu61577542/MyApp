//go:build gui

package nativegui

import (
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
)

type monitorCard struct {
	widget.BaseWidget
	name     string
	detail   string
	status   string
	active   bool
	tap      func()
	drop     func(fyne.Position)
	dragging bool
}

func newMonitor(name, detail, status string, active bool, tap func()) *monitorCard {
	card := &monitorCard{name: name, detail: detail, status: status, active: active, tap: tap}
	card.ExtendBaseWidget(card)
	return card
}
func (m *monitorCard) Tapped(*fyne.PointEvent) {
	if m.tap != nil {
		m.tap()
	}
}
func (m *monitorCard) Dragged(event *fyne.DragEvent) {
	if m.drop == nil {
		return
	}
	m.dragging = true
	m.Move(m.Position().Add(fyne.NewPos(event.Dragged.DX, event.Dragged.DY)))
}
func (m *monitorCard) DragEnd() {
	if m.drop != nil && m.dragging {
		m.dragging = false
		m.drop(m.Position().Add(fyne.NewPos(m.Size().Width/2, m.Size().Height/2)))
	}
}
func (m *monitorCard) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(theme.BackgroundColor())
	background.CornerRadius = 7
	background.StrokeWidth = 1
	name := widget.NewLabelWithStyle(m.name, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	name.Truncation = fyne.TextTruncateEllipsis
	detail := widget.NewLabel(m.detail)
	detail.Alignment = fyne.TextAlignCenter
	detail.Importance = widget.LowImportance
	detail.Truncation = fyne.TextTruncateEllipsis
	status := widget.NewLabel(m.status)
	status.Alignment = fyne.TextAlignCenter
	status.Importance = widget.LowImportance
	status.Truncation = fyne.TextTruncateEllipsis
	renderer := &monitorRenderer{card: m, background: background, content: container.NewVBox(name, detail, status)}
	renderer.Refresh()
	return renderer
}

type monitorRenderer struct {
	card       *monitorCard
	background *canvas.Rectangle
	content    fyne.CanvasObject
}

func (r *monitorRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.content.Move(fyne.NewPos(4, 4))
	r.content.Resize(fyne.NewSize(size.Width-8, size.Height-8))
}
func (r *monitorRenderer) MinSize() fyne.Size { return fyne.NewSize(144, 108) }
func (r *monitorRenderer) Refresh() {
	r.background.FillColor = theme.BackgroundColor()
	r.background.StrokeColor = theme.InputBorderColor()
	if r.card.active {
		r.background.FillColor = theme.SelectionColor()
		r.background.StrokeColor = theme.PrimaryColor()
	}
	r.background.Refresh()
	r.content.Refresh()
}
func (r *monitorRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.background, r.content}
}
func (r *monitorRenderer) Destroy() {}

type boardLayout struct{ positions []string }

func (l *boardLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(480, 340) }
func (l *boardLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	card := fyne.NewSize(144, 108)
	cx, cy := size.Width/2, size.Height/2
	for i, object := range objects {
		x, y := cx, cy
		if i > 0 {
			switch l.positions[i-1] {
			case "left":
				x -= size.Width / 3
			case "right":
				x += size.Width / 3
			case "up":
				y -= 112
			case "down":
				y += 112
			}
		}
		object.Resize(card)
		object.Move(fyne.NewPos(x-card.Width/2, y-card.Height/2))
	}
}

func (v *desktopView) deviceBoard() fyne.CanvasObject {
	localStatus := "本机操作"
	if v.view.Target != "" {
		localStatus = "键鼠在远端"
		if v.cfg.Role == config.RoleAgent {
			localStatus = "接受远端输入"
		}
	}
	local := newMonitor(v.cfg.DeviceName, "本机 · "+roleName(v.cfg.Role), localStatus, v.view.Target == "", func() {
		dialog.ShowInformation("这台电脑", v.cfg.DeviceName+"\n"+roleName(v.cfg.Role)+"\n设备详情与共享偏好可在设置中更改。", v.window)
	})
	objects := []fyne.CanvasObject{local}
	var positions []string
	for i, record := range v.records {
		record := record
		id := record.DeviceID
		position := "right"
		if i == 1 {
			position = "left"
		}
		for _, peer := range v.cfg.Peers {
			if peer.ID == id && peer.Position != "" {
				position = peer.Position
			}
		}
		if v.layoutEditing {
			if draft := v.layoutDraft[id]; draft != "" {
				position = draft
			}
		}
		status := "已配对 · 离线"
		system := "已信任电脑"
		for _, peer := range v.view.Peers {
			if peer.ID == id {
				if peer.Status == "connected" {
					status = "已连接"
				}
				switch peer.OS {
				case "darwin":
					system = "macOS"
				case "windows":
					system = "Windows"
				case "linux":
					system = "Linux"
				}
			}
		}
		if v.view.Target == id {
			status = "当前控制目标"
			if v.cfg.Role == config.RoleAgent {
				status = "正在控制本机"
			}
		}
		monitor := newMonitor(peerName(id, v.view.Peers, v.records), system, status, v.view.Target == id, func() { v.showPeerDetails(id) })
		objects = append(objects, monitor)
		positions = append(positions, position)
	}
	if v.cfg.Role != config.RoleController || len(objects) > 3 {
		return surface(container.NewGridWithColumns(2, objects...))
	}
	board := container.New(&boardLayout{positions: positions}, objects...)
	if v.layoutEditing {
		for i, object := range objects {
			if i == 0 {
				continue
			}
			id := v.records[i-1].DeviceID
			monitor := object.(*monitorCard)
			if _, exists := v.layoutDraft[id]; !exists {
				continue
			}
			monitor.drop = func(point fyne.Position) {
				dx, dy := point.X-board.Size().Width/2, point.Y-board.Size().Height/2
				if math.Abs(float64(dx)) < 50 && math.Abs(float64(dy)) < 50 {
					v.notice.SetText("请将电脑放到本机屏幕四周，避免重叠。")
				} else {
					position := "right"
					if math.Abs(float64(dx)) >= math.Abs(float64(dy)) {
						if dx < 0 {
							position = "left"
						}
					} else {
						position = "down"
						if dy < 0 {
							position = "up"
						}
					}
					v.layoutDraft[id] = position
				}
				v.refresh(true)
			}
		}
	}
	return surface(board)
}
