package main

import "testing"

func TestAutoCategory(t *testing.T) {
	cases := map[string]string{
		"Visual Studio Code": "Developer", "Microsoft Edge": "Internet", "Google Chrome": "Internet", "Spotify": "Media",
		"Discord": "Social", "WhatsApp": "Social", "Steam": "Games", "Xbox": "Games", "Photoshop 2025": "Creative",
		"Adobe Photoshop 2025": "Creative", "Paint": "Creative", "Word": "Productivity", "Excel": "Productivity", "Outlook": "Productivity",
		"Calculator": "Utilities", "Settings": "System", "Snipping Tool": "Utilities", "ChatGPT": "AI", "Claude": "AI",
		"Terminal": "Developer", "Notepad": "Productivity", "Notepad++": "Developer", "Photos": "Media", "Camera": "Media",
		"File Explorer": "Utilities", "Microsoft Store": "System", "Blade Control": "Utilities", "Affinity Photo 2": "Creative",
		"OBS Studio": "Creative", "Blender": "Creative", "Windows Media Player": "Media", "Control Panel": "System",
		"Teams": "Social", "Zoom Workplace": "Social", "Firefox": "Internet", "VLC media player": "Media", "7-Zip File Manager": "Utilities",
		"Minecraft Launcher": "Games", "Docker Desktop": "Developer", "GitHub Desktop": "Developer", "Obsidian": "Productivity",
		"Clock": "Utilities", "Weather": "Utilities", "Phone Link": "Social", "Sticky Notes": "Productivity", "Task Manager": "Utilities",
		"Uninstall Steam": "Games", "Some Random App": "Other", "Helium": "Other", "Accessibility": "System",
	}
	for name, want := range cases {
		if got := autoCategory("x", name); got != want {
			t.Errorf("%q: got %s, want %s", name, got, want)
		}
	}
	if got := autoCategory(`{7C5A40EF-A0FB-4BFC-874A-C0F2E0B9FA8E}\Steam\steamapps\common\Hades\Hades.exe`, "Hades"); got != "Games" {
		t.Errorf("steam game path: got %s", got)
	}
}
