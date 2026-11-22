//go:build gui

package nativegui

import (
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type desktopTheme struct{ appearance string }

var systemFontCache sync.Map

func (t desktopTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.appearance == "light" {
		variant = theme.VariantLight
	} else if t.appearance == "dark" {
		variant = theme.VariantDark
	}
	dark := variant == theme.VariantDark
	choose := func(light, darkColor color.NRGBA) color.Color {
		if dark {
			return darkColor
		}
		return light
	}
	switch name {
	case theme.ColorNamePrimary, theme.ColorNameFocus:
		return choose(color.NRGBA{R: 8, G: 106, B: 221, A: 255}, color.NRGBA{R: 113, G: 177, B: 250, A: 255})
	case theme.ColorNameBackground:
		return choose(color.NRGBA{R: 252, G: 252, B: 253, A: 255}, color.NRGBA{R: 31, G: 33, B: 37, A: 255})
	case theme.ColorNameInputBackground:
		return choose(color.NRGBA{R: 243, G: 245, B: 248, A: 255}, color.NRGBA{R: 42, G: 45, B: 50, A: 255})
	case theme.ColorNameForeground:
		return choose(color.NRGBA{R: 36, G: 40, B: 47, A: 255}, color.NRGBA{R: 239, G: 241, B: 245, A: 255})
	case theme.ColorNameDisabled:
		return choose(color.NRGBA{R: 101, G: 109, B: 121, A: 255}, color.NRGBA{R: 167, G: 176, B: 189, A: 255})
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		return choose(color.NRGBA{R: 224, G: 228, B: 234, A: 255}, color.NRGBA{R: 68, G: 73, B: 82, A: 255})
	case theme.ColorNameSelection:
		return choose(color.NRGBA{R: 225, G: 237, B: 255, A: 255}, color.NRGBA{R: 42, G: 64, B: 94, A: 255})
	}
	return theme.DefaultTheme().Color(name, variant)
}
func (t desktopTheme) Font(style fyne.TextStyle) fyne.Resource {
	name := ""
	switch runtime.GOOS {
	case "darwin":
		name = "/System/Library/Fonts/SFNS.ttf"
		if style.Italic {
			name = "/System/Library/Fonts/SFNSItalic.ttf"
		}
		if style.Bold {
			name = "/System/Library/Fonts/Supplemental/Arial Bold.ttf"
			if style.Italic {
				name = "/System/Library/Fonts/Supplemental/Arial Bold Italic.ttf"
			}
		}
		if style.Monospace {
			name = "/System/Library/Fonts/SFNSMono.ttf"
			if style.Italic {
				name = "/System/Library/Fonts/SFNSMonoItalic.ttf"
			}
		}
	case "windows":
		font := "segoeui.ttf"
		if style.Bold {
			font = "segoeuib.ttf"
		}
		if style.Italic {
			font = "segoeuii.ttf"
			if style.Bold {
				font = "segoeuiz.ttf"
			}
		}
		if style.Monospace {
			font = "consola.ttf"
			if style.Bold {
				font = "consolab.ttf"
			}
		}
		name = filepath.Join(os.Getenv("WINDIR"), "Fonts", font)
	}
	if name != "" && !style.Symbol {
		if cached, exists := systemFontCache.Load(name); exists {
			return cached.(fyne.Resource)
		}
		resource, err := fyne.LoadResourceFromPath(name)
		if err == nil {
			systemFontCache.Store(name, resource)
			return resource
		}
		fallback := theme.DefaultTheme().Font(style)
		systemFontCache.Store(name, fallback)
		return fallback
	}
	return theme.DefaultTheme().Font(style)
}
func (t desktopTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}
func (t desktopTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameCaptionText:
		return 12
	case theme.SizeNameHeadingText:
		return 23
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNamePadding, theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInputBorder:
		return 1
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		if runtime.GOOS == "windows" {
			return 4
		}
		return 7
	}
	return theme.DefaultTheme().Size(name)
}

func heading(text string) *widget.RichText {
	return widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}, SizeName: theme.SizeNameHeadingText}})
}
func sectionTitle(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}
func secondary(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	label.Importance = widget.LowImportance
	return label
}
func paragraph(text string) *widget.Label {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	return label
}
func surface(content fyne.CanvasObject) fyne.CanvasObject {
	group := &surfaceGroup{content: container.NewPadded(content)}
	group.ExtendBaseWidget(group)
	return group
}

type surfaceGroup struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func (g *surfaceGroup) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(theme.InputBackgroundColor())
	background.CornerRadius = 9
	return &surfaceRenderer{group: g, background: background}
}

type surfaceRenderer struct {
	group      *surfaceGroup
	background *canvas.Rectangle
}

func (r *surfaceRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.group.content.Resize(size)
}
func (r *surfaceRenderer) MinSize() fyne.Size { return r.group.content.MinSize() }
func (r *surfaceRenderer) Refresh() {
	r.background.FillColor = theme.InputBackgroundColor()
	r.background.Refresh()
	r.group.content.Refresh()
}
func (r *surfaceRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.background, r.group.content}
}
func (r *surfaceRenderer) Destroy() {}
func primary(text string, action func()) *widget.Button {
	button := widget.NewButton(text, action)
	button.Importance = widget.HighImportance
	return button
}
func quiet(text string, action func()) *widget.Button {
	button := widget.NewButton(text, action)
	button.Importance = widget.LowImportance
	return button
}
