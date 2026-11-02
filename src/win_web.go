//go:build windows

package main

// Small WebView2 host : a normal Win32 window with
// an embedded Edge WebView2 page and a JSON-RPC bridge to Go functions.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

type webHost struct {
	borderless bool
	hwnd       uintptr
	web        *edge.Chromium
	binds      map[string]reflect.Value
	onClose    func()
	onActivate func(active bool)
}

var (
	webHosts     = map[uintptr]*webHost{}
	webWndProcCB = windows.NewCallback(webWndProc)
)

func webWndProc(hwnd, msg, wp, lp uintptr) uintptr {
	if h := webHosts[hwnd]; h != nil {
		switch msg {
		case 0x0005: // WM_SIZE
			if h.web != nil {
				switch wp {
				case 1:
					h.web.Hide()
				case 0, 2:
					h.web.Show()
					h.web.Resize()
				}
			}
		case 0x0003: // WM_MOVE
			if h.web != nil {
				h.web.NotifyParentWindowPositionChanged()
			}
		case 0x0006: // WM_ACTIVATE
			active := wp&0xFFFF != 0
			if (wp>>16)&0xFFFF != 0 {
				break
			}
			if active && h.web != nil {
				h.web.Focus()
			}
			if h.onActivate != nil {
				h.onActivate(active)
			}
		case 0x0083: // WM_NCCALCSIZE
			if h.borderless && wp != 0 {
				return 0 // whole window is client area (keeps the DWM shadow and rounded corners)
			}
		case 0x0084: // WM_NCHITTEST
			if h.borderless {
				return 1
			}
		case 0x0010: // WM_CLOSE
			if h.onClose != nil {
				h.onClose()
				return 0
			}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func newWebHost(hwnd uintptr, dataPath string, bg edge.COREWEBVIEW2_COLOR) (*webHost, error) {
	h := &webHost{hwnd: hwnd, binds: map[string]reflect.Value{}}
	if old := webHosts[hwnd]; old != nil {
		h.borderless = old.borderless
	}
	webHosts[hwnd] = h
	c := edge.NewChromium()
	c.DataPath = dataPath
	c.MessageCallback = h.onMessage
	if !c.Embed(hwnd) {
		delete(webHosts, hwnd)
		return nil, errors.New("WebView2 couldn't start")
	}
	h.web = c
	if c.GetController() == nil {
		delete(webHosts, hwnd)
		return nil, errors.New("WebView2 didn't finish starting")
	}
	if s, err := c.GetSettings(); err == nil && s != nil {
		s.PutAreDefaultContextMenusEnabled(false)
		s.PutAreDevToolsEnabled(false)
	}
	if ctl := c.GetController(); ctl != nil {
		if c2 := ctl.GetICoreWebView2Controller2(); c2 != nil {
			c2.PutDefaultBackgroundColor(bg)
		}
	}
	c.Resize()
	return h, nil
}

func (h *webHost) bind(name string, f any) { h.binds[name] = reflect.ValueOf(f) }

func (h *webHost) load(html string) {
	names := make([]string, 0, len(h.binds))
	for k := range h.binds {
		names = append(names, k)
	}
	nb, _ := json.Marshal(names)
	h.web.Init(`(function(){
  const pending = {}; let next = 1;
  window.__mdResolve = (id, ok, v) => { const p = pending[id]; if (!p) return; delete pending[id]; ok ? p[0](v) : p[1](v); };
  for (const name of ` + string(nb) + `) {
    window[name] = (...args) => new Promise((res, rej) => {
      const id = next++; pending[id] = [res, rej];
      window.chrome.webview.postMessage(JSON.stringify({id, method: name, params: args}));
    });
  }
})();`)
	h.web.NavigateToString(html)
}

func (h *webHost) eval(js string) {
	if h != nil && h.web != nil {
		h.web.Eval(js)
	}
}

type webCall struct {
	ID     int               `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func (h *webHost) onMessage(msg string) {
	var c webCall
	if err := json.Unmarshal([]byte(msg), &c); err != nil {
		return
	}
	res, err := h.call(c)
	if err != nil {
		e, _ := json.Marshal(err.Error())
		h.eval(fmt.Sprintf("window.__mdResolve(%d,false,%s)", c.ID, e))
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		b = []byte("null")
	}
	h.eval(fmt.Sprintf("window.__mdResolve(%d,true,%s)", c.ID, b))
}

func (h *webHost) call(c webCall) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			app.logf("panic in %s: %v", c.Method, r)
			err = fmt.Errorf("internal error in %s", c.Method)
		}
	}()
	f, ok := h.binds[c.Method]
	if !ok {
		return nil, fmt.Errorf("unknown function %s", c.Method)
	}
	t := f.Type()
	if len(c.Params) != t.NumIn() {
		return nil, fmt.Errorf("%s: expected %d arguments", c.Method, t.NumIn())
	}
	args := make([]reflect.Value, t.NumIn())
	for i := range args {
		v := reflect.New(t.In(i))
		if err := json.Unmarshal(c.Params[i], v.Interface()); err != nil {
			return nil, fmt.Errorf("%s: bad argument: %v", c.Method, err)
		}
		args[i] = v.Elem()
	}
	out := f.Call(args)
	if len(out) == 0 {
		return nil, nil
	}
	return out[0].Interface(), nil
}

func registerWebClass(name string, bg uintptr) {
	hinst, _, _ := pGetModuleHandleW.Call(0)
	cur, _, _ := pLoadCursorW.Call(0, 32512)
	icon, _, _ := user32.NewProc("LoadImageW").Call(hinst, 1, 1, 32, 32, 0)
	wc := WNDCLASSEXW{
		LpfnWndProc:   webWndProcCB,
		HInstance:     hinst,
		HIcon:         icon,
		HCursor:       cur,
		HbrBackground: bg,
		LpszClassName: u16(name),
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
}
