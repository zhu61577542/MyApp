package instance

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrRunning = errors.New("MyApp 已在运行")

type Guard struct {
	Activations <-chan struct{}
	close       func() error
	once        sync.Once
	err         error
}

func Acquire(name string) (*Guard, error) {
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\\/\x00") {
		return nil, fmt.Errorf("实例名称无效")
	}
	return acquire(name)
}

func (g *Guard) Close() error {
	g.once.Do(func() { g.err = g.close() })
	return g.err
}
