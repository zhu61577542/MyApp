package platform

import (
	"errors"
	common "myapp/internal/input"
)

func NewPhysicalObserver() (common.PhysicalObserver, error) {
	capture, err := NewCapturer()
	if err != nil {
		return nil, err
	}
	observer, ok := capture.(common.PhysicalObserver)
	if !ok {
		_ = capture.Close()
		return nil, errors.New("当前平台尚不支持实体输入监听")
	}
	return observer, nil
}
