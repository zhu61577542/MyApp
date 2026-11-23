//go:build darwin

package darwininput

import "github.com/ebitengine/purego"

// RequestAccess 请求系统提示，最终授权仍由用户决定。
func RequestAccess(listen bool) error {
	library, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	defer purego.Dlclose(library)
	var preflightListen, preflightPost, requestListen, requestPost func() bool
	purego.RegisterLibFunc(&preflightListen, library, "CGPreflightListenEventAccess")
	purego.RegisterLibFunc(&preflightPost, library, "CGPreflightPostEventAccess")
	purego.RegisterLibFunc(&requestListen, library, "CGRequestListenEventAccess")
	purego.RegisterLibFunc(&requestPost, library, "CGRequestPostEventAccess")
	if !preflightPost() {
		requestPost()
	}
	if listen && !preflightListen() {
		requestListen()
	}
	return nil
}
