package mygo

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/egoist/mygo/internal/platform"
)

// ChildWebViewOptions configure Window.NewChildWebView.
type ChildWebViewOptions struct {
	// URL is loaded right away. The page is an ordinary web page: it has
	// no access to the app's frontend or to the app's Go methods.
	URL string
	// Rect is where the web view shows, in DIPs from the top-left corner
	// of the window's client area.
	Rect Rectangle
	// Transparent shows what the window's page paints under the web view
	// through the transparent parts of its page.
	Transparent bool
}

// ChildWebViewState is what a child web view reports through OnState.
type ChildWebViewState struct {
	// URL is the address the web view was created with (or last loaded).
	URL string
	// Status is "loading", "loaded", "error" or "gone".
	Status string
	// CurrentURL is the page's address, which differs from URL once the
	// page navigates.
	CurrentURL string
	// Code and Desc say why the page did not load, on "error" and "gone".
	Code string
	Desc string
}

// ChildWebView is a web page showing in a rectangle over a window's page:
// a side panel, a preview or a tab beside the app's own content.
//
//	web, err := win.NewChildWebView(mygo.ChildWebViewOptions{
//		URL:  "https://example.com",
//		Rect: mygo.Rectangle{X: 800, Y: 100, Width: 400, Height: 600},
//	})
//	web.OnState(func(s mygo.ChildWebViewState) { ... })
//	web.SetRect(mygo.Rectangle{X: 820, Y: 100, Width: 400, Height: 600})
//
// Its page stays loaded while it is hidden, and it is destroyed with
// Destroy or with its window.
type ChildWebView struct {
	win       *Window
	native    platform.ChildWebView
	destroyed atomic.Bool
	mu        sync.Mutex
	onState   func(ChildWebViewState)
}

// childWebViewHost is the part of a backend that creates child web views.
// Backends without one, or windows of native UI, report that they can't.
type childWebViewHost interface {
	NewChildWebView(platform.ChildWebViewOptions) (platform.ChildWebView, error)
}

// NewChildWebView creates a web view over the window's page, at Rect. The
// window must show a web page, not native UI.
func (w *Window) NewChildWebView(opts ChildWebViewOptions) (*ChildWebView, error) {
	if w == nil {
		return nil, errors.New("mygo: nil window")
	}
	v := &ChildWebView{win: w}
	var err error
	onMain(func() {
		if w.native == nil || w.destroyed.Load() {
			err = errors.New("mygo: the window is closed")
			return
		}
		host, ok := w.native.(childWebViewHost)
		if !ok {
			err = errors.New("mygo: child web views are not supported on this platform")
			return
		}
		v.native, err = host.NewChildWebView(platform.ChildWebViewOptions{
			URL:         opts.URL,
			Rect:        platform.RectF{X: float64(opts.Rect.X), Y: float64(opts.Rect.Y), W: float64(opts.Rect.Width), H: float64(opts.Rect.Height)},
			Transparent: opts.Transparent,
			OnState:     func(s platform.ChildWebViewState) { v.emit(s) },
		})
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Window returns the window the web view is in.
func (v *ChildWebView) Window() *Window { return v.win }

// SetRect moves and resizes the web view, in DIPs from the top-left corner
// of the window's client area.
func (v *ChildWebView) SetRect(r Rectangle) {
	v.do(func(n platform.ChildWebView) {
		n.SetRect(platform.RectF{X: float64(r.X), Y: float64(r.Y), W: float64(r.Width), H: float64(r.Height)})
	})
}

// SetVisible shows or hides the web view, keeping its page, its session and
// its scroll position.
func (v *ChildWebView) SetVisible(visible bool) {
	v.do(func(n platform.ChildWebView) { n.SetVisible(visible) })
}

// LoadURL navigates the web view to raw.
func (v *ChildWebView) LoadURL(raw string) {
	v.do(func(n platform.ChildWebView) { n.LoadURL(raw) })
}

// Reload reloads the page.
func (v *ChildWebView) Reload() {
	v.do(func(n platform.ChildWebView) { n.Reload() })
}

// Focus gives the web view the keyboard.
func (v *ChildWebView) Focus() {
	v.do(func(n platform.ChildWebView) { n.Focus() })
}

// OnState sets the listener that hears the page as it loads, navigates and
// fails.
func (v *ChildWebView) OnState(fn func(ChildWebViewState)) {
	v.mu.Lock()
	v.onState = fn
	v.mu.Unlock()
}

// Destroy closes the web view and its page.
func (v *ChildWebView) Destroy() {
	if v == nil || v.destroyed.Swap(true) {
		return
	}
	onMain(func() {
		if v.native != nil {
			v.native.Close()
			v.native = nil
		}
	})
}

// IsDestroyed reports whether the web view was destroyed.
func (v *ChildWebView) IsDestroyed() bool { return v == nil || v.destroyed.Load() }

func (v *ChildWebView) do(fn func(platform.ChildWebView)) {
	if v == nil || v.destroyed.Load() {
		return
	}
	onMain(func() {
		if v.native != nil && !v.destroyed.Load() {
			fn(v.native)
		}
	})
}

func (v *ChildWebView) emit(s platform.ChildWebViewState) {
	v.mu.Lock()
	fn := v.onState
	v.mu.Unlock()
	if fn != nil {
		fn(ChildWebViewState{URL: s.URL, Status: s.Status, CurrentURL: s.CurrentURL, Code: s.Code, Desc: s.Desc})
	}
}
