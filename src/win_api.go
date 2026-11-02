//go:build windows

package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	version  = windows.NewLazySystemDLL("version.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pSetTimer                      = user32.NewProc("SetTimer")
	pUpdateLayeredWindow           = user32.NewProc("UpdateLayeredWindow")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pShowWindowAsync               = user32.NewProc("ShowWindowAsync")
	pIsWindowVisible               = user32.NewProc("IsWindowVisible")
	pIsWindow                      = user32.NewProc("IsWindow")
	pGetWindow                     = user32.NewProc("GetWindow")
	pGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	pGetWindowTextW                = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW          = user32.NewProc("GetWindowTextLengthW")
	pGetClassNameW                 = user32.NewProc("GetClassNameW")
	pEnumWindows                   = user32.NewProc("EnumWindows")
	pGetWindowThreadProcessId      = user32.NewProc("GetWindowThreadProcessId")
	pGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pIsIconic                      = user32.NewProc("IsIconic")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pMonitorFromPoint              = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pFindWindowW                   = user32.NewProc("FindWindowW")
	pFindWindowExW                 = user32.NewProc("FindWindowExW")
	pSetWindowRgn                  = user32.NewProc("SetWindowRgn")
	pSetWindowCompositionAttribute = user32.NewProc("SetWindowCompositionAttribute")
	pKeybdEvent                    = user32.NewProc("keybd_event")
	pSetCapture                    = user32.NewProc("SetCapture")
	pReleaseCapture                = user32.NewProc("ReleaseCapture")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pRegisterWindowMessageW        = user32.NewProc("RegisterWindowMessageW")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pGetAncestor                   = user32.NewProc("GetAncestor")
	pScreenToClient                = user32.NewProc("ScreenToClient")

	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	pSelectObject       = gdi32.NewProc("SelectObject")
	pDeleteObject       = gdi32.NewProc("DeleteObject")
	pDeleteDC           = gdi32.NewProc("DeleteDC")
	pGetObjectW         = gdi32.NewProc("GetObjectW")
	pGetDIBits          = gdi32.NewProc("GetDIBits")
	pCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	pGetStockObject     = gdi32.NewProc("GetStockObject")
	pCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")

	pSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	pSHGetPropertyStoreForWindow = shell32.NewProc("SHGetPropertyStoreForWindow")
	pShellExecuteW               = shell32.NewProc("ShellExecuteW")
	pSHAppBarMessage             = shell32.NewProc("SHAppBarMessage")
	pDragAcceptFiles             = shell32.NewProc("DragAcceptFiles")
	pDragQueryFileW              = shell32.NewProc("DragQueryFileW")
	pDragQueryPoint              = shell32.NewProc("DragQueryPoint")
	pDragFinish                  = shell32.NewProc("DragFinish")
	pSHFileOperationW            = shell32.NewProc("SHFileOperationW")
	pSHEmptyRecycleBinW          = shell32.NewProc("SHEmptyRecycleBinW")
	pSHQueryRecycleBinW          = shell32.NewProc("SHQueryRecycleBinW")
	pSHGetKnownFolderPath        = shell32.NewProc("SHGetKnownFolderPath")

	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoCreateInstance = ole32.NewProc("CoCreateInstance")
	pCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	pPropVariantClear = ole32.NewProc("PropVariantClear")

	pOpenProcess                = kernel32.NewProc("OpenProcess")
	pCloseHandle                = kernel32.NewProc("CloseHandle")
	pQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	pGetApplicationUserModelId  = kernel32.NewProc("GetApplicationUserModelId")
	pCreateMutexW               = kernel32.NewProc("CreateMutexW")
	pGetModuleHandleW           = kernel32.NewProc("GetModuleHandleW")

	pDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
	pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

	pGetFileVersionInfoSizeW = version.NewProc("GetFileVersionInfoSizeW")
	pGetFileVersionInfoW     = version.NewProc("GetFileVersionInfoW")
	pVerQueryValueW          = version.NewProc("VerQueryValueW")
)

const (
	WS_POPUP          = 0x80000000
	WS_EX_LAYERED     = 0x00080000
	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_NOACTIVATE  = 0x08000000
	WS_EX_ACCEPTFILES = 0x00000010
	WS_EX_APPWINDOW   = 0x00040000

	WM_DESTROY         = 0x0002
	WM_CLOSE           = 0x0010
	WM_NULL            = 0x0000
	WM_QUERYENDSESSION = 0x0011
	WM_ENDSESSION      = 0x0016
	WM_SETTINGCHANGE   = 0x001A
	WM_DISPLAYCHANGE   = 0x007E
	WM_TIMER           = 0x0113
	WM_MOUSEMOVE       = 0x0200
	WM_LBUTTONDOWN     = 0x0201
	WM_LBUTTONUP       = 0x0202
	WM_RBUTTONUP       = 0x0205
	WM_MBUTTONUP       = 0x0208
	WM_DROPFILES       = 0x0233
	WM_DPICHANGED      = 0x02E0
	WM_MOUSEACTIVATE   = 0x0021
	WM_SYSCOMMAND      = 0x0112
	WM_APP             = 0x8000
	MA_NOACTIVATE      = 3

	SW_HIDE           = 0
	SW_SHOWNORMAL     = 1
	SW_MINIMIZE       = 6
	SW_SHOWNOACTIVATE = 4
	SW_SHOWNA         = 8
	SW_RESTORE        = 9

	SWP_NOSIZE     = 0x0001
	SWP_NOMOVE     = 0x0002
	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040

	GW_OWNER     = 4
	GA_ROOTOWNER = 3
	GWL_EXSTYLE  = ^uintptr(19) // -20

	MF_STRING    = 0x0000
	MF_GRAYED    = 0x0001
	MF_CHECKED   = 0x0008
	MF_POPUP     = 0x0010
	MF_SEPARATOR = 0x0800

	TPM_RETURNCMD   = 0x0100
	TPM_BOTTOMALIGN = 0x0020
	TPM_CENTERALIGN = 0x0004
	TPM_RIGHTBUTTON = 0x0002

	ULW_ALPHA = 2
)

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }
type SIZE struct{ CX, CY int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	_       uint32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MONITORINFO struct {
	CbSize    uint32
	RcMonitor RECT
	RcWork    RECT
	DwFlags   uint32
}

type BITMAPINFOHEADER struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type BITMAPINFO struct {
	Header BITMAPINFOHEADER
	Colors [4]uint32
}

type BITMAP struct {
	BmType       int32
	BmWidth      int32
	BmHeight     int32
	BmWidthBytes int32
	BmPlanes     uint16
	BmBitsPixel  uint16
	BmBits       uintptr
}

type BLENDFUNCTION struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

type APPBARDATA struct {
	CbSize           uint32
	HWnd             uintptr
	UCallbackMessage uint32
	UEdge            uint32
	Rc               RECT
	LParam           uintptr
}

type ACCENTPOLICY struct {
	AccentState   uint32
	AccentFlags   uint32
	GradientColor uint32
	AnimationId   uint32
}

type WINCOMPATTRDATA struct {
	Attrib uint32
	Data   uintptr
	Size   uintptr
}

type SHFILEOPSTRUCTW struct {
	Hwnd          uintptr
	Func          uint32
	From          *uint16
	To            *uint16
	Flags         uint16
	AnyAborted    int32
	NameMappings  uintptr
	ProgressTitle *uint16
}

type SHQUERYRBINFO struct {
	CbSize      uint32
	I64Size     int64
	I64NumItems int64
}

type PROPERTYKEY struct {
	Fmtid windows.GUID
	Pid   uint32
}

type PROPVARIANT struct {
	Vt   uint16
	R1   uint16
	R2   uint16
	R3   uint16
	Val  uintptr
	Val2 uintptr
}

// ---------- helpers ----------

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func up(p *uint16) uintptr { return uintptr(unsafe.Pointer(p)) }

func u16ToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p)))
}

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// comCall invokes vtable method idx on a COM object.
func comCall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func comRelease(obj uintptr) {
	if obj != 0 {
		comCall(obj, 2)
	}
}

func hresultOK(hr uintptr) bool { return int32(hr) >= 0 }

func getCursorPos() (int32, int32) {
	var p POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p.X, p.Y
}

func windowText(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+2)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf)
}

func className(hwnd uintptr) string {
	buf := make([]uint16, 256)
	pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	return windows.UTF16ToString(buf)
}

func messageBox(text, title string) {
	pMessageBoxW.Call(0, up(u16(text)), up(u16(title)), 0x40)
}
