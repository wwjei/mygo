//go:build windows && (amd64 || arm64)

package windows

// Child web views: WebView2 controllers showing in a rectangle over a
// window's page. Each one lives in a child window of its own, above the
// page's web view, so the page keeps its DOM and the child keeps its own
// document, cookies and history.

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"unsafe"

	"github.com/egoist/mygo/internal/platform"
)

type childWebView struct {
	w          *window
	opts       platform.ChildWebViewOptions
	hwnd       uintptr
	controller uintptr
	webview    uintptr
	visible    bool
	closed     bool
	currentURL string
}

// NewChildWebView creates a web view over the window's page. Main thread
// only.
func (w *window) NewChildWebView(opts platform.ChildWebViewOptions) (platform.ChildWebView, error) {
	if w.closed || w.destroying {
		return nil, errDestroyed
	}
	if w.b.envErr != nil {
		return nil, w.b.envErr
	}
	if w.b.env == 0 {
		return nil, fmt.Errorf("mygo: the WebView2 environment is not ready")
	}

	v := &childWebView{w: w, opts: opts, visible: true, currentURL: opts.URL}
	dpi := dpiOf(w.hwnd)
	px := func(x float64) int32 { return int32(math.Round(x * float64(dpi) / 96)) }
	cw, ch := px(opts.Rect.W), px(opts.Rect.H)
	v.hwnd = createWindow(0, "STATIC", "", wsChild|wsVisible|wsClipSiblings|wsClipChildren,
		px(opts.Rect.X), px(opts.Rect.Y), cw, ch, w.hwnd)
	if v.hwnd == 0 {
		return nil, fmt.Errorf("mygo: cannot create the child web view's window")
	}
	// HWND_TOP: above the window's page, which is a sibling created first.
	procSetWindowPos.Call(v.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)

	hr := withHandler(func(hr, controller uintptr) {
		if v.closed || w.closed || w.destroying {
			if controller != 0 {
				comCall(controller, ctlClose)
			}
			return
		}
		if failed(hr) || controller == 0 {
			v.emit("error", opts.URL, "E_CREATE", "无法创建网页视图")
			return
		}
		addRef(controller)
		v.controller = controller
		comCall(controller, ctlGetCoreWebView2, uintptr(unsafe.Pointer(&v.webview)))
		v.applyBackground()
		v.setUp()
		if opts.URL != "" {
			v.LoadURL(opts.URL)
		}
	}, func(h uintptr) uintptr { return comCall(w.b.env, envCreateController, v.hwnd, h) })
	if failed(hr) {
		v.emit("error", opts.URL, "E_CREATE", "无法创建网页视图")
	}
	return v, nil
}

func (v *childWebView) setUp() {
	var settings uintptr
	if !failed(comCall(v.webview, wvGetSettings, uintptr(unsafe.Pointer(&settings)))) && settings != 0 {
		comCall(settings, setPutIsScriptEnabled, 1)
		comCall(settings, setPutIsWebMessageEnabled, 1)
		comCall(settings, setPutAreDefaultScriptDialogsEnabled, 1)
		comCall(settings, setPutIsStatusBarEnabled, 0)
		comCall(settings, setPutAreDefaultContextMenusEnabled, 1)
		comCall(settings, setPutAreDevToolsEnabled, boolArg(v.w.opts.DevTools))
		release(settings)
	}

	var tok int64
	add := func(index int, fn func(sender, args uintptr)) {
		withHandler(fn, func(h uintptr) uintptr {
			return comCall(v.webview, index, h, uintptr(unsafe.Pointer(&tok)))
		})
	}
	add(wvAddNavigationStarting, v.navigationStarting)
	add(wvAddNavigationCompleted, v.navigationCompleted)
	add(wvAddProcessFailed, v.processFailed)
	add(wvAddNewWindowRequested, func(_, args uintptr) {
		// A popup would be a window of its own, outside the pane: the app
		// opens its address elsewhere, as with other external links.
		var p uintptr
		comCall(args, newWinGetURI, uintptr(unsafe.Pointer(&p)))
		uri := takeWstr(p)
		comCall(args, newWinPutHandled, 1)
		v.emit("popup", uri, "", "")
	})

	v.SetRect(v.opts.Rect)
	v.SetVisible(v.visible)
}

func (v *childWebView) applyBackground() {
	ctl2 := queryInterface(v.controller, &iidICoreWebView2Controller2)
	if ctl2 == 0 {
		return
	}
	defer release(ctl2)
	var c uint32 = 0xFFFFFFFF // opaque white
	if v.opts.Transparent {
		c = 0
	}
	comCall(ctl2, ctl2PutDefaultBackgroundColor, uintptr(c))
}

func (v *childWebView) navigationStarting(_, args uintptr) {
	var p uintptr
	comCall(args, navStartingGetURI, uintptr(unsafe.Pointer(&p)))
	uri := takeWstr(p)
	if !childAllowedURL(uri) {
		comCall(args, navStartingPutCancel, 1)
		return
	}
	v.currentURL = uri
	v.emit("loading", uri, "", "")
}

func (v *childWebView) navigationCompleted(_, args uintptr) {
	var ok int32
	comCall(args, navCompletedGetIsSuccess, uintptr(unsafe.Pointer(&ok)))
	if ok != 0 {
		v.currentURL = v.source()
		v.emit("loaded", v.currentURL, "", "")
		return
	}
	var status int32
	comCall(args, navCompletedGetWebErrorStatus, uintptr(unsafe.Pointer(&status)))
	v.emit("error", v.currentURL, "ERR_"+strconv.Itoa(int(status)), webErrorText(status))
}

func (v *childWebView) processFailed(_, args uintptr) {
	var kind int32
	comCall(args, procFailedGetKind, uintptr(unsafe.Pointer(&kind)))
	switch kind {
	case 0:
		v.emit("gone", v.currentURL, "RENDER_GONE", "browser-exited")
	case 1, 3:
		v.emit("gone", v.currentURL, "RENDER_GONE", "crashed")
	case 2:
		v.emit("gone", v.currentURL, "RENDER_GONE", "unresponsive")
	}
}

func (v *childWebView) source() string {
	var p uintptr
	if failed(comCall(v.webview, wvGetSource, uintptr(unsafe.Pointer(&p)))) {
		return v.currentURL
	}
	return takeWstr(p)
}

func (v *childWebView) emit(status, current, code, desc string) {
	if v.opts.OnState == nil {
		return
	}
	v.opts.OnState(platform.ChildWebViewState{
		URL: v.opts.URL, Status: status, CurrentURL: current, Code: code, Desc: desc,
	})
}

func (v *childWebView) SetRect(r platform.RectF) {
	v.opts.Rect = r
	if v.hwnd == 0 {
		return
	}
	dpi := dpiOf(v.w.hwnd)
	px := func(x float64) int32 { return int32(math.Round(x * float64(dpi) / 96)) }
	x, y := px(r.X), px(r.Y)
	cw, ch := px(r.W), px(r.H)
	procSetWindowPos.Call(v.hwnd, 0, uintptr(x), uintptr(y), uintptr(cw), uintptr(ch), swpNoZOrder|swpNoActivate)
	if v.controller != 0 {
		putBounds(v.controller, rect{0, 0, cw, ch})
	}
}

func (v *childWebView) SetVisible(visible bool) {
	v.visible = visible
	if v.hwnd == 0 || v.closed {
		return
	}
	cmd := uintptr(swHide)
	if visible {
		cmd = swShowNoActivate
	}
	procShowWindow.Call(v.hwnd, cmd)
	if v.controller != 0 {
		comCall(v.controller, ctlPutIsVisible, boolArg(visible))
	}
	if visible {
		procSetWindowPos.Call(v.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	}
}

func (v *childWebView) LoadURL(raw string) {
	v.opts.URL = raw
	if v.webview == 0 {
		return
	}
	comCall(v.webview, wvNavigate, uintptr(unsafe.Pointer(u16(raw))))
}

func (v *childWebView) Reload() {
	if v.webview != 0 {
		comCall(v.webview, wvReload)
	}
}

func (v *childWebView) Focus() {
	if v.controller != 0 {
		comCall(v.controller, ctlMoveFocus, 0)
	}
}

func (v *childWebView) Close() {
	if v.closed {
		return
	}
	v.closed = true
	if v.controller != 0 {
		comCall(v.controller, ctlClose)
		release(v.controller)
		v.controller, v.webview = 0, 0
	}
	if v.hwnd != 0 {
		procDestroyWindow.Call(v.hwnd)
		v.hwnd = 0
	}
}

// childAllowedURL keeps a pane on the web: the app's own schemes and local
// files stay out of it.
func childAllowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return true
	case "about":
		return u.Opaque == "blank" || u.Path == "blank"
	}
	return false
}

func webErrorText(status int32) string {
	switch status {
	case 0:
		return "未知错误"
	case 6:
		return "服务器地址无法解析"
	case 7:
		return "连接服务器失败"
	case 9:
		return "服务器没有响应"
	case 11:
		return "证书错误"
	case 12:
		return "服务器返回了错误"
	case 14:
		return "地址无效"
	}
	return "页面加载失败（" + strconv.Itoa(int(status)) + "）"
}
