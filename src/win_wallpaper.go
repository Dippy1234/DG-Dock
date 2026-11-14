//go:build windows

package main

import (
	"os"
	"path/filepath"
)

// tidyOldWallpaper removes what Builds 182–186 kept for the dock's live wallpaper,
// which is now only the separate DG-Wallpaper app.
func tidyOldWallpaper() {
	os.RemoveAll(filepath.Join(dataDir(), "Wallpaper"))
	os.Remove(filepath.Join(dataDir(), "DG-Wallpaper.exe")) // fails harmlessly if it's running
}
