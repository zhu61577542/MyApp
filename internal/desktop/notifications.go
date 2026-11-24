package desktop

import "strings"

type NotificationGate struct{ sent map[string]bool }

func (g *NotificationGate) Take(key string) bool {
	if strings.HasPrefix(key, "permission") {
		return false
	}
	if g.sent == nil {
		g.sent = make(map[string]bool)
	}
	if g.sent[key] {
		return false
	}
	g.sent[key] = true
	return true
}

func (g *NotificationGate) Resolved(key string) { delete(g.sent, key) }
