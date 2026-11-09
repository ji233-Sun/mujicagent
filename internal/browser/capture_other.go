//go:build !darwin

package browser

import (
	"context"

	"github.com/egoist/mygo"
)

type nativeCapture struct{}

func newNativeCapture() *nativeCapture   { return nil }
func (*nativeCapture) configure(uintptr) {}
func (*nativeCapture) supported() bool   { return false }
func (*nativeCapture) destroy()          {}
func (*nativeCapture) eval(context.Context, *mygo.WebView, string, string, any) (string, error) {
	return "", ErrUnsupported
}
func (*nativeCapture) capture(context.Context, *mygo.WebView, string) (Screenshot, error) {
	return Screenshot{}, ErrUnsupported
}
