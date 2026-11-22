//go:build gui && !darwin && !windows

package nativegui

import "errors"

func loginItemEnabled() (bool, error)  { return false, nil }
func setLoginItem(bool, Options) error { return errors.New("当前平台暂未接入登录启动") }
