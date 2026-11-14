package main

import "testing"

var realApps = []string{"Google Chrome", "Microsoft Edge", "Firefox", "Visual Studio Code", "Visual Studio 2022", "Visual Studio Installer",
	"Spotify", "Steam", "Discord", "Notepad", "Notepad++", "Calculator", "Command Prompt", "Windows PowerShell", "PowerShell 7 (x64)",
	"Terminal", "Task Manager", "Control Panel", "Settings", "File Explorer", "Microsoft Store", "Paint", "Paint 3D", "Photos",
	"Snipping Tool", "Microsoft Teams", "Outlook", "Word", "Excel", "PowerPoint", "OneNote", "OneDrive", "WhatsApp", "Telegram Desktop",
	"Xbox", "Xbox Game Bar", "Epic Games Launcher", "Battle.net", "EA app", "OBS Studio", "VLC media player", "7-Zip File Manager",
	"Adobe Photoshop 2025", "Adobe Acrobat", "Blender", "GIMP 2.10.38", "Razer Synapse", "Blade Control", "NVIDIA Control Panel",
	"NVIDIA App", "Device Manager", "Disk Management", "Registry Editor", "Remote Desktop Connection", "Character Map", "Clock",
	"Camera", "Mail", "Calendar", "Weather", "Microsoft To Do", "Sticky Notes", "Media Player", "Windows Security", "Get Help",
	"Copilot", "Phone Link", "Quick Assist", "Sound Recorder", "Magnifier", "On-Screen Keyboard", "Character Map", "Event Viewer",
	"Services", "System Information", "Resource Monitor", "Task Scheduler", "Windows Tools", "YouTube Music", "Netflix", "Prime Video",
	"Git Bash", "Git GUI", "Docker Desktop", "Postman", "IntelliJ IDEA Community Edition 2025.2", "Android Studio", "Python 3.13 (64-bit)",
	"IDLE (Python 3.13 64-bit)", "Node.js command prompt", "WinRAR", "qBittorrent", "Zoom Workplace", "Slack", "Notion", "Obsidian", "Figma"}

func TestSearchAccuracy(t *testing.T) {
	var es []searchEntry
	for i, n := range realApps {
		es = append(es, searchEntry{Kind: "app", ID: "a" + string(rune('A'+i%26)) + n, Title: n})
	}
	for _, s := range []searchEntry{
		{Title: "Wi-Fi", ID: "s:wifi", Words: "network internet wireless"},
		{Title: "Bluetooth & devices", ID: "s:bt", Words: "headphones mouse keyboard pair"},
		{Title: "Display", ID: "s:display", Words: "screen resolution scale brightness monitor refresh rate hz"},
		{Title: "Sound", ID: "s:sound", Words: "audio volume speakers microphone output"},
		{Title: "Colours", ID: "s:colors", Words: "accent dark mode light mode colors"},
		{Title: "Installed apps", ID: "s:apps", Words: "uninstall programs apps"},
		{Title: "Windows Update", ID: "s:wu", Words: "updates upgrade patch"},
		{Title: "Power & battery", ID: "s:power", Words: "battery saver power sleep energy"},
	} {
		s.Kind = "set"
		es = append(es, s)
	}
	for _, f := range []string{"cv_final.docx", "chrome_installer.exe", "notes on spotify playlists.txt", "Screenshot 2026-11-01.png", "steam_keys.txt", "Teams meeting notes.docx", "word list.txt", "DG-Dock-Setup-1.0.0-build171.exe"} {
		es = append(es, searchEntry{Kind: "file", ID: "f:" + f, Title: f, Sub: "Downloads"})
	}
	es = append(es, searchEntry{Kind: "recent", ID: "r:excel budget.xlsx", Title: "budget.xlsx", Boost: 30})
	cases := [][2]string{
		{"chrome", "Google Chrome"}, {"chr", "Google Chrome"}, {"edge", "Microsoft Edge"}, {"vsc", "Visual Studio Code"}, {"code", "Visual Studio Code"},
		{"vs code", "Visual Studio Code"}, {"cmd", "Command Prompt"}, {"powershell", "PowerShell 7 (x64)"},
		{"task", "Task Manager"}, {"taskmgr", "Task Manager"}, {"control", "Control Panel"}, {"word", "Word"}, {"excel", "Excel"}, {"ppt", "PowerPoint"},
		{"teams", "Microsoft Teams"}, {"store", "Microsoft Store"}, {"explorer", "File Explorer"}, {"files", "File Explorer"}, {"calc", "Calculator"},
		{"spotfy", "Spotify"}, {"spotifu", "Spotify"}, {"discrod", "Discord"}, {"chorme", "Google Chrome"}, {"notpad", "Notepad"},
		{"photoshop", "Adobe Photoshop 2025"}, {"vlc", "VLC media player"}, {"obs", "OBS Studio"},
		{"7zip", "7-Zip File Manager"}, {"7-zip", "7-Zip File Manager"}, {"whatsapp", "WhatsApp"}, {"wa", "WhatsApp"}, {"yt music", "YouTube Music"},
		{"youtube", "YouTube Music"}, {"regedit", "Registry Editor"}, {"onedrive", "OneDrive"}, {"one drive", "OneDrive"}, {"snip", "Snipping Tool"},
		{"screenshot", "Snipping Tool"}, {"nvidia", "NVIDIA App"}, {"python", "Python 3.13 (64-bit)"}, {"intellij", "IntelliJ IDEA Community Edition 2025.2"},
		{"idea", "IntelliJ IDEA Community Edition 2025.2"}, {"wifi", "Wi-Fi"}, {"wi fi", "Wi-Fi"}, {"bluetooth", "Bluetooth & devices"}, {"brightness", "Display"},
		{"volume", "Sound"}, {"dark mode", "Colours"}, {"uninstall", "Installed apps"}, {"update", "Windows Update"}, {"battery", "Power & battery"},
		{"steam", "Steam"}, {"st", "Steam"}, {"term", "Terminal"}, {"settings", "Settings"}, {"paint", "Paint"}, {"cam", "Camera"},
		{"blade", "Blade Control"}, {"razer", "Razer Synapse"}, {"budget", "budget.xlsx"}, {"cv", "cv_final.docx"}, {"git", "Git GUI"},
		{"node", "Node.js command prompt"}, {"docker", "Docker Desktop"}, {"calendar", "Calendar"}, {"mail", "Mail"}, {"outlook", "Outlook"},
		{"todo", "Microsoft To Do"}, {"to do", "Microsoft To Do"}, {"sticky", "Sticky Notes"}, {"notes", "Sticky Notes"}, {"zoom", "Zoom Workplace"},
		{"device manager", "Device Manager"}, {"devmgmt", "Device Manager"}, {"rdp", "Remote Desktop Connection"}, {"remote", "Remote Desktop Connection"},
		{"security", "Windows Security"}, {"defender", "Windows Security"}, {"epic", "Epic Games Launcher"}, {"battlenet", "Battle.net"},
		{"teh", ""}, {"xbox", "Xbox"}, {"game bar", "Xbox Game Bar"}, {"pdf", "Adobe Acrobat"}, {"acrobat", "Adobe Acrobat"},
	}
	fail := 0
	for _, c := range cases {
		r := rankSearch(es, c[0], nil, 5)
		got := ""
		if len(r) > 0 {
			got = r[0].Title
		}
		if c[1] != "" && got != c[1] {
			fail++
			alts := []string{}
			for _, x := range r {
				alts = append(alts, x.Title)
			}
			t.Errorf("MISS %-14q want %-28q got %q  %v", c[0], c[1], got, alts)
		}
	}
	t.Logf("%d / %d wrong", fail, len(cases))
}
