//go:build !darwin && !windows

package pointer

import "errors"

func New() (Backend, error) {
	return nil, errors.New("当前平台尚未接入主屏跨机边缘切换与光标隐藏")
}
