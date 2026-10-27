package platform

// ChildWebViewOptions configures a web view that shows in a rectangle over
// the page of a window, as a page beside other content: a side panel, a
// preview, a tab.
//
// Unlike WebView, which native UI places, a child web view does not depend
// on the window's content: the app gives it the rectangle, in DIPs relative
// to the window's page, and moves it with SetRect as its pane moves.
type ChildWebViewOptions struct {
	// URL is loaded right away. Unlike a window's page, it is not subject
	// to the app's frontend: the web view loads what it is given, and its
	// page cannot call the app's Go methods.
	URL string
	// Rect is where the web view shows, in DIPs from the top-left corner
	// of the window's client area.
	Rect RectF
	// Transparent shows what the window's page paints under the web view
	// through the transparent parts of its page.
	Transparent bool
	// OnState reports the page as it loads, navigates and fails. It is
	// called on the main thread.
	OnState func(ChildWebViewState)
}

// ChildWebViewState is what a child web view reports through OnState.
type ChildWebViewState struct {
	// URL is the address the web view was created with.
	URL string
	// Status is "loading", "loaded", "error", "gone" or "popup": the page
	// asked for a window of its own, whose address is CurrentURL, which
	// the app opens elsewhere.
	Status string
	// CurrentURL is the page's address, which differs from URL after the
	// page navigates.
	CurrentURL string
	// Code and Desc say why the page could not load, on "error" and "gone".
	Code string
	Desc string
}

// ChildWebView is a web page showing in a rectangle over a window's page.
type ChildWebView interface {
	// SetRect moves and resizes the web view, in DIPs.
	SetRect(RectF)
	// SetVisible shows or hides the web view, keeping its page.
	SetVisible(bool)
	LoadURL(string)
	Reload()
	// Focus gives the web view the keyboard.
	Focus()
	// Close closes the web view and its page.
	Close()
}
