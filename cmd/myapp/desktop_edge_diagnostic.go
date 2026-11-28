package main

import (
	"fmt"
	"time"

	"myapp/internal/desktop"
	"myapp/internal/pointer"
)

type edgeDiagnostic struct {
	next                time.Time
	reason, side        string
	samples             map[string]uint64
	edges               map[string]uint64
	minimumDistance     map[string]int
	positionSource      string
	positionDifferences uint64
	lastSignature       string
	lastReport          time.Time
	screenKey           string
}

func (d *edgeDiagnostic) screen(screen pointer.Screen) {
	if d.screenKey == screen.Key() {
		return
	}
	d.screenKey = screen.Key()
	d.edges, d.minimumDistance = nil, nil
	d.positionDifferences = 0
	d.positionSource = ""
}

func (d *edgeDiagnostic) record(reason string) {
	d.reason = reason
	if d.samples == nil {
		d.samples = make(map[string]uint64)
	}
	d.samples[reason]++
}

func (d *edgeDiagnostic) position(point pointer.Point, screen pointer.Screen) {
	d.screen(screen)
	if !screen.Valid() || !screen.Contains(point) {
		return
	}
	if d.edges == nil {
		d.edges = make(map[string]uint64)
	}
	if d.minimumDistance == nil {
		d.minimumDistance = make(map[string]int)
	}
	for side, distance := range map[string]int{"left": point.X - screen.X, "right": screen.X + screen.Width - 1 - point.X, "up": point.Y - screen.Y, "down": screen.Y + screen.Height - 1 - point.Y} {
		previous, seen := d.minimumDistance[side]
		if !seen || distance < previous {
			d.minimumDistance[side] = distance
		}
	}
	if point.X-screen.X < 2 {
		d.edges["left"]++
	}
	if point.X-screen.X >= screen.Width-2 {
		d.edges["right"]++
	}
	if point.Y-screen.Y < 2 {
		d.edges["up"]++
	}
	if point.Y-screen.Y >= screen.Height-2 {
		d.edges["down"]++
	}
}

func (d *edgeDiagnostic) report(c *desktopCapture, state desktop.Snapshot, screen pointer.Screen) {
	if time.Now().Before(d.next) {
		return
	}
	d.next = time.Now().Add(2 * time.Second)
	d.screen(screen)
	fields := map[string]any{"reason": d.reason, "side": d.side, "phase": state.ControlPhase, "edge_enabled": state.EdgeEnabled, "paused": state.Paused, "target_set": state.Target != "", "screen_width": screen.Width, "screen_height": screen.Height}
	fields["layout_id"] = screen.ID
	copyCounts := func(counts map[string]uint64) map[string]uint64 {
		out := make(map[string]uint64, len(counts))
		for k, v := range counts {
			out[k] = v
		}
		return out
	}
	fields["samples"], fields["edge_samples"] = copyCounts(d.samples), copyCounts(d.edges)
	fields["position_source"], fields["position_difference_samples"] = d.positionSource, d.positionDifferences
	distances := make(map[string]int, len(d.minimumDistance))
	for side, value := range d.minimumDistance {
		distances[side] = value
	}
	fields["minimum_edge_distance"] = distances
	if c.activity != nil {
		a := c.activity
		a.mu.Lock()
		fields["listener_ready"], fields["physical_moves"] = a.ready, a.moves.Load()
		observer := a.observer
		a.mu.Unlock()
		if native, ok := observer.(interface{ Diagnostics() map[string]uint64 }); ok {
			fields["native"] = native.Diagnostics()
		}
	}
	signature := fmt.Sprint(d.reason, d.side, state.ControlPhase, state.EdgeEnabled, state.Paused, state.Target != "", screen.Key(), fields["listener_ready"], fields["physical_moves"])
	if native, ok := fields["native"].(map[string]uint64); ok {
		signature += fmt.Sprint(native["mouse_messages"], native["keyboard_messages"])
	}
	if signature == d.lastSignature && time.Since(d.lastReport) < 30*time.Second {
		return
	}
	d.lastSignature, d.lastReport = signature, time.Now()
	c.state.Diagnostic("实体边缘监视", fields)
}
