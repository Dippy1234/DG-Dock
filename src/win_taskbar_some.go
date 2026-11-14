//go:build windows

package main

// unclipTaskbars gives every taskbar its whole window back. (v2.0.1–2.0.6 could keep
// the taskbar on chosen displays by cutting the others down to nothing; that option
// was taken out in 2.0.7, and this undoes anything it left behind.)
func unclipTaskbars() {
	for _, h := range trayWindows() {
		pSetWindowRgn.Call(h, 0, 1)
	}
}
