package clipboard

import (
	"testing"
	"time"
)

func TestRequirementDuplicateContentStillAdvancesRemoteVersion(t *testing.T) {
	e, _ := NewEngine("local")
	now := time.Now()
	_, _, _ = e.Local("current", now)
	latest, _ := NewEntry("remote", 2, "current", now)
	if got := e.Remote(latest); got != IgnoredDuplicate {
		t.Fatalf("相同内容应忽略，实际 %v", got)
	}
	older, _ := NewEntry("remote", 1, "old", now)
	if got := e.Remote(older); got != IgnoredStale {
		t.Fatalf("旧版本被接受并可能覆盖新内容: decision=%v", got)
	}
}

func TestRequirementLocalCopyAfterUnobservedRemoteWrite(t *testing.T) {
	e, _ := NewEngine("local")
	now := time.Now()
	remote, _ := NewEntry("remote", 1, "A", now)
	e.Remote(remote)
	if _, got, err := e.Local("B", now); err != nil || got != Accepted {
		t.Fatalf("本地 B 未接受: %v %v", got, err)
	}
	if _, got, err := e.Local("A", now); err != nil || got != Accepted {
		t.Fatalf("重新复制 A 被过期的去环记录吞掉: %v %v", got, err)
	}
}
