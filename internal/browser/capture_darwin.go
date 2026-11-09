//go:build darwin

package browser

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/ji233-Sun/mujicagent/internal/browser/capturepolicy"
)

const maxCaptureBytes = 4 * 1024 * 1024

type captureAPI struct {
	setClient func(uintptr, unsafe.Pointer)
	post      func(uintptr, uintptr, uintptr)
	wkString  func(string) uintptr
	data      func([]byte, uintptr) uintptr
	dataBytes func(uintptr) *byte
	dataSize  func(uintptr) uintptr
	typeID    func(uintptr) uint64
	dataType  func() uint64
	equal     func(uintptr, string) bool
	release   func(uintptr)
	callback  uintptr
}

var captureLibrary struct {
	sync.Once
	api *captureAPI
}

// Callback trampolines are allocated once, not once per view/call. Cleanup
// needs no native object, including when the host has already destroyed it.
var capturePending = struct {
	sync.Mutex
	calls map[uintptr]*captureCall
}{calls: map[uintptr]*captureCall{}}

type captureCall struct {
	nonce [16]byte
	done  chan []byte
}

type nativeCapture struct {
	path   string
	api    *captureAPI
	ready  bool
	closed chan struct{}
	stop   sync.Once
}

func (n *nativeCapture) supported() bool { return n != nil && n.ready }
func (n *nativeCapture) destroy() {
	if n != nil {
		n.stop.Do(func() { close(n.closed) })
	}
}

func captureSend(o objc.ID, method string, args ...any) objc.ID {
	return o.Send(objc.RegisterName(method), args...)
}

func loadCaptureAPI() *captureAPI {
	captureLibrary.Do(func() {
		handle, err := purego.Dlopen("/System/Library/Frameworks/WebKit.framework/WebKit", purego.RTLD_NOW)
		if err != nil {
			return
		}
		a := &captureAPI{}
		symbols := []struct {
			target any
			name   string
		}{
			{&a.setClient, "WKPageSetPageInjectedBundleClient"},
			{&a.post, "WKPagePostMessageToInjectedBundle"},
			{&a.wkString, "WKStringCreateWithUTF8CString"},
			{&a.data, "WKDataCreate"},
			{&a.dataBytes, "WKDataGetBytes"},
			{&a.dataSize, "WKDataGetSize"},
			{&a.typeID, "WKGetTypeID"},
			{&a.dataType, "WKDataGetTypeID"},
			{&a.equal, "WKStringIsEqualToUTF8CString"},
			{&a.release, "WKRelease"},
		}
		for _, symbol := range symbols {
			address, err := purego.Dlsym(handle, symbol.name)
			if err != nil {
				return
			}
			purego.RegisterFunc(symbol.target, address)
		}
		a.callback = purego.NewCallback(func(page, name, body, _ uintptr) {
			capturePending.Lock()
			call := capturePending.calls[page]
			capturePending.Unlock()
			if call == nil || !a.equal(name, "mujicagent.capture.v1.result") ||
				body == 0 || a.typeID(body) != a.dataType() {
				return
			}
			size := a.dataSize(body)
			if size < 32 || size > maxCaptureBytes+32 {
				return
			}
			pointer := a.dataBytes(body)
			if pointer == nil {
				return
			}
			raw := unsafe.Slice(pointer, int(size))
			if !bytes.Equal(raw[:5], []byte{'M', 'C', 'A', 'P', 1}) ||
				!bytes.Equal(raw[8:24], call.nonce[:]) {
				return
			}
			select {
			case call.done <- bytes.Clone(raw):
			default:
			}
		})
		captureLibrary.api = a
	})
	return captureLibrary.api
}

func newNativeCapture() *nativeCapture {
	base, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil
	}
	path := filepath.Join(base, "MujicAgentCapture.bundle")
	// go run/go test sees resources before platform directories are merged.
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(base, "darwin", "MujicAgentCapture.bundle")
	}
	if _, err := os.Stat(filepath.Join(path, "Contents", "MacOS", "MujicAgentCapture")); err != nil {
		return nil
	}
	api := loadCaptureAPI()
	if api == nil {
		return nil
	}
	return &nativeCapture{path: path, api: api, closed: make(chan struct{})}
}

func (n *nativeCapture) configure(configuration uintptr) {
	// Called by MyGo on the main thread before constructing this exact view.
	str := func(s string) objc.ID {
		return captureSend(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", s)
	}
	pc := captureSend(objc.ID(objc.GetClass("_WKProcessPoolConfiguration")), "new")
	if pc == 0 {
		return
	}
	defer captureSend(pc, "release")
	if captureSend(pc, "respondsToSelector:", objc.RegisterName("setInjectedBundleURL:")) == 0 ||
		captureSend(objc.ID(objc.GetClass("WKProcessPool")), "instancesRespondToSelector:", objc.RegisterName("_initWithConfiguration:")) == 0 {
		return
	}
	url := captureSend(objc.ID(objc.GetClass("NSURL")), "fileURLWithPath:", str(n.path))
	captureSend(pc, "setInjectedBundleURL:", url)
	pool := captureSend(captureSend(objc.ID(objc.GetClass("WKProcessPool")), "alloc"), "_initWithConfiguration:", pc)
	if pool != 0 {
		captureSend(objc.ID(configuration), "setProcessPool:", pool)
		captureSend(pool, "release")
		n.ready = true
	}
}

func (n *nativeCapture) transact(ctx context.Context, view *mygo.WebView, mode byte, payload []byte) ([]byte, error) {
	if n == nil || !n.ready {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := &captureCall{done: make(chan []byte, 1)}
	if _, err := rand.Read(call.nonce[:]); err != nil {
		return nil, ErrEngine
	}
	body := append([]byte{'M', 'C', 'A', 'P', 1, mode, 0, 0}, call.nonce[:]...)
	digest := capturepolicy.Digest(pageScript)
	body = append(body, digest[:]...)
	body = append(body, payload...)
	if len(body) > 65592 {
		return nil, ErrUnsupported
	}
	type client struct {
		Version                    int32
		_                          int32
		Info, Message, Synchronous uintptr
	}
	var page uintptr
	err := view.WithNative(func(handle uintptr) {
		if ctx.Err() != nil {
			return
		}
		if captureSend(objc.ID(handle), "respondsToSelector:", objc.RegisterName("_pageRefForTransitionToWKWebView")) == 0 {
			return
		}
		page = uintptr(captureSend(objc.ID(handle), "_pageRefForTransitionToWKWebView"))
		capturePending.Lock()
		if page == 0 || capturePending.calls[page] != nil {
			capturePending.Unlock()
			page = 0
			return
		}
		capturePending.calls[page] = call
		capturePending.Unlock()
		c := client{Message: n.api.callback}
		n.api.setClient(page, unsafe.Pointer(&c))
		name, data := n.api.wkString("mujicagent.capture.v1"), n.api.data(body, uintptr(len(body)))
		defer n.api.release(name)
		defer n.api.release(data)
		n.api.post(page, name, data)
	})
	if err != nil || page == 0 {
		return nil, ErrEngine
	}
	defer func() {
		capturePending.Lock()
		if capturePending.calls[page] == call {
			delete(capturePending.calls, page)
		}
		capturePending.Unlock()
	}()
	// Cancellation revokes in Runtime immediately, but Busy must drain the
	// actual native transaction. A fixed independent deadline destroys the
	// view if the WebProcess never acknowledges it.
	timer := time.NewTimer(DefaultTimeout)
	defer timer.Stop()
	select {
	case <-n.closed:
		return nil, ErrClosed
	case wire := <-call.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if wire[5] != 0 {
			return nil, ErrSensitive
		}
		if wire[6] != mode {
			return nil, ErrEngine
		}
		return wire, nil
	case <-timer.C:
		// Destroy is the ownership barrier. The per-call nonce and identity
		// check in deferred cleanup keep any late response out of a new call.
		view.Destroy()
		return nil, ErrEngine
	}
}

func (n *nativeCapture) capture(ctx context.Context, view *mygo.WebView, origin string) (Screenshot, error) {
	wire, err := n.transact(ctx, view, 0, []byte(origin))
	if err != nil {
		return Screenshot{}, err
	}
	data := wire[32:]
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	width, height := int(binary.BigEndian.Uint32(wire[24:28])), int(binary.BigEndian.Uint32(wire[28:32]))
	if err != nil || width < 1 || height < 1 || width > 8192 || height > 8192 ||
		width*height > 16777216 || cfg.Width != width || cfg.Height != height {
		return Screenshot{}, ErrEngine
	}
	return Screenshot{PNG: data, Width: width, Height: height}, nil
}

func (n *nativeCapture) eval(ctx context.Context, view *mygo.WebView, origin, action string, args any) (string, error) {
	payload, err := json.Marshal(struct {
		Origin string `json:"origin"`
		Action string `json:"action"`
		Args   any    `json:"args"`
	}{origin, action, args})
	if err != nil {
		return "", ErrInvalidArgument
	}
	wire, err := n.transact(ctx, view, 1, payload)
	if err != nil {
		return "", err
	}
	if len(wire) > 65568 {
		return "", ErrEngine
	}
	return string(wire[32:]), nil
}
