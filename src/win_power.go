//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Power options in Launchpad (Sleep, Lock, Sign out, Restart, Shut down).

var (
	powrprof         = windows.NewLazySystemDLL("powrprof.dll")
	pSetSuspendState = powrprof.NewProc("SetSuspendState")
	pLockWorkStation = user32.NewProc("LockWorkStation")
)

const (
	ewxLogoff         = 0x00000000
	ewxShutdown       = 0x00000001
	ewxReboot         = 0x00000002
	ewxPowerOff       = 0x00000008
	ewxHybridShutdown = 0x00400000 // Fast startup, as the Start menu's Shut down does
	// SHTDN_REASON_MAJOR_OTHER | SHTDN_REASON_MINOR_OTHER | SHTDN_REASON_FLAG_PLANNED
	shtdnReasonPlanned = 0x80000000
)

// enableShutdownPrivilege lets this process restart or shut Windows down.
func enableShutdownPrivilege() {
	var tok windows.Token
	if windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok) != nil {
		return
	}
	defer tok.Close()
	var luid windows.LUID
	name, _ := windows.UTF16PtrFromString("SeShutdownPrivilege")
	if windows.LookupPrivilegeValue(nil, name, &luid) != nil {
		return
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	windows.AdjustTokenPrivileges(tok, false, &tp, uint32(unsafe.Sizeof(tp)), nil, nil)
}

// powerAction carries out one of the Launchpad power options.
func (a *App) powerAction(action string) {
	switch action {
	case "lock":
		a.hideLaunchpad()
		pLockWorkStation.Call()
	case "sleep":
		a.hideLaunchpad()
		// hibernate=FALSE, force=FALSE, wakeupEventsDisabled=FALSE
		pSetSuspendState.Call(0, 0, 0)
	case "signout":
		a.hideLaunchpad()
		windows.ExitWindowsEx(ewxLogoff, shtdnReasonPlanned)
	case "restart":
		a.hideLaunchpad()
		enableShutdownPrivilege()
		windows.ExitWindowsEx(ewxReboot, shtdnReasonPlanned)
	case "shutdown":
		a.hideLaunchpad()
		enableShutdownPrivilege()
		// Fast startup like the Start menu; plain power-off if that isn't available
		if windows.ExitWindowsEx(ewxShutdown|ewxPowerOff|ewxHybridShutdown, shtdnReasonPlanned) != nil {
			windows.ExitWindowsEx(ewxShutdown|ewxPowerOff, shtdnReasonPlanned)
		}
	}
}
