package main

import (
	"crypto/sha256"
	"fmt"
	"sync/atomic"

	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
)

type peerInputCounts struct {
	token                                                ownership.Token
	keyboardCaptured, keyboardSent                       atomic.Uint64
	keyboardReceived, keyboardInjected, keyboardRejected atomic.Uint64
	mouseSent, mouseInjected                             atomic.Uint64
	failed                                               atomic.Uint64
}

func tokenDiagnostic(token ownership.Token) map[string]any {
	hash := sha256.Sum256([]byte(token.Session))
	return map[string]any{"session": fmt.Sprintf("%x", hash[:6]), "epoch": token.Epoch}
}

func keyboardEvent(event common.Event) bool {
	return event.Kind == common.KeyDown || event.Kind == common.KeyUp
}

func (c *peerInputCounts) report(state *desktop.Session, source bool, final bool, capture common.Capturer) {
	if c == nil {
		return
	}
	fields := tokenDiagnostic(c.token)
	fields["source"], fields["final"] = source, final
	fields["keyboard_captured"], fields["keyboard_sent"] = c.keyboardCaptured.Load(), c.keyboardSent.Load()
	fields["keyboard_received"], fields["keyboard_injected"], fields["keyboard_rejected"] = c.keyboardReceived.Load(), c.keyboardInjected.Load(), c.keyboardRejected.Load()
	fields["mouse_sent"], fields["mouse_injected"], fields["failed"] = c.mouseSent.Load(), c.mouseInjected.Load(), c.failed.Load()
	if diagnostic, ok := capture.(interface{ Diagnostics() map[string]uint64 }); ok {
		fields["native"] = diagnostic.Diagnostics()
	}
	state.Diagnostic("输入链路计数", fields)
}

func (d *desktopPeerDriver) reportInputCounts() {
	d.mu.Lock()
	run, target := d.run, d.targetCounts
	d.mu.Unlock()
	if run != nil {
		run.counts.report(d.state, true, false, run.capture)
	}
	target.report(d.state, false, false, nil)
}
