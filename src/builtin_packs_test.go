package main

import (
	"image/png"
	"os"
	"testing"
)

// Windows' own Start menu names must find their icon in the built-in packs.
func TestBuiltinPackMatches(t *testing.T) {
	for _, dir := range []string{"builtin_packs/bw"} {
		p, err := LoadIconPack(dir)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"Calculator": "calculator", "Camera": "camera", "Character Map": "charactermap", "Clock": "clock",
			"Command Prompt": "commandprompt", "Component Services": "componentservices", "Computer Management": "computermanagement",
			"Control Panel": "controlpanel", "Defragment and Optimise Drives": "defragmentandoptimisedrives",
			"Defragment and Optimize Drives": "defragmentandoptimisedrives", "Disk Clean-up": "diskcleanup",
			"Event Viewer": "eventviewer", "File Explorer": "fileexplorer", "Game Bar": "xbox", "Get Started": "getstarted",
			"iSCSI Initiator": "iscsiinitiator", "Live captions": "livecaptions", "Local Security Policy": "localsecuritypolicy",
			"Magnifier": "magnifier", "Media Player": "mediaplayer", "Microsoft Store": "microsoftstore", "Narrator": "narrator",
			"Notepad": "notepad", "ODBC Data Sources (64-bit)": "odbcdatasources", "On-Screen Keyboard": "onscreenkeyboard",
			"Outlook (new)": "outlook", "Paint": "paint", "Performance Monitor": "performancemonitor", "Phone Link": "phonelink",
			"Photos": "photos", "Print Management": "printmanagement", "Recovery Drive": "recoverydrive",
			"Registry Editor": "registryeditor", "Resource Monitor": "resourcemonitor", "Run": "run", "Services": "services",
			"Settings": "settings", "Snipping Tool": "snippingtool", "Steps Recorder": "stepsrecorder",
			"System Configuration": "systemconfiguration", "System Information": "systeminformation", "Task Manager": "taskmanager",
			"Task Scheduler": "taskscheduler", "Terminal": "terminal", "Voice access": "voiceaccess", "Windows Backup": "windowsbackup",
			"Windows Defender Firewall with Advanced Security": "windowsdefenderfirewall", "Windows Media Player Legacy": "windowsmediaplayerlegacy",
			"Windows Memory Diagnostic": "windowsmemorydiagnostic", "Windows PowerShell": "windowspowershell",
			"Windows PowerShell (x86)": "windowspowershell", "Windows PowerShell ISE": "windowspowershellise",
			"Windows PowerShell ISE (x86)": "windowspowershellise", "Windows Security": "windowssecurity", "Windows Tools": "windowstools",
			"Microsoft Edge": "microsoftedge", "Copilot": "copilot", "Microsoft To Do": "microsofttodo", "Sticky Notes": "stickynotes",
			"Weather": "weather", "Click to Do": "clicktodo", "Microsoft 365 (Office)": "microsoft365", "OneDrive": "onedrive",
			"Microsoft Clipchamp": "clipchamp", "Xbox": "xbox", "Solitaire & Casual Games": "solitaire", "Sound Recorder": "soundrecorder",
			"Quick Assist": "quickassist", "Remote Desktop Connection": "remotedesktopconnection", "Feedback Hub": "feedbackhub",
			"Microsoft Teams": "microsoftteams", "Dev Home": "devhome", "NVIDIA app": "nvidiaapp", "NVIDIA Control Panel": "nvidiacontrolpanel",
			"Recycle Bin": "recyclebin", "Downloads": "downloads", "Documents": "documents", "Mail": "mail", "Calendar": "calendar",
			"Windows Fax and Scan": "windowsfaxandscan", "Films & TV": "films", "Paint 3D": "paint", "Maps": "maps",
		}
		for name, icon := range want {
			if got := p.Match(name); got != icon {
				t.Errorf("%s: %q → %q, want %q", dir, name, got, icon)
			}
		}
		// other apps from the Start menu: generic glyphs by kind of app
		more := map[string]string{
			"AMD Bug Report Tool": "bugreport", "AMD Install Manager": "installer", "AMD Software: Adrenalin Edition": "graphicschip",
			"Blend for Visual Studio 2022": "blend", "Claude": "aichat", "Developer Command Prompt for VS 2022": "developercommandprompt",
			"Developer PowerShell for VS 2022": "developerpowershell", "DG-Dock": "dgdock", "Fedora Media Writer": "usbwriter",
			"Helium": "browser", "Intel® Graphics Command Center": "graphicschip", "Intel® Optane™ Memory and Storage Management": "storagemanager",
			"Ollama": "aibot", "OmniConsole": "gamelauncher", "Orbit Launcher": "launcher", "Realtek Audio Console": "audioconsole",
			"Visual Studio Installer": "installer", "Visual Studio 2022": "codeeditor", "x64 Native Tools Command Prompt for VS 2022": "developercommandprompt",
			"x64_x86 Cross Tools Command Prompt for VS 2022": "developercommandprompt", "x86 Native Tools Command Prompt for VS 2022": "developercommandprompt",
			"x86_x64 Cross Tools Command Prompt for VS 2022": "developercommandprompt", "Steam": "gamelauncher", "Google Chrome": "browser",
			"Word": "wordprocessor", "Excel": "spreadsheet", "PowerPoint": "presentation", "LibreOffice": "officesuite",
			"Mozilla Thunderbird": "email", "Notion": "notes", "Obsidian": "notes", "Trello": "kanban", "Miro": "whiteboard",
			"GIMP 2.10": "photoeditor", "Adobe Photoshop 2025": "photoeditor", "Inkscape": "vectoreditor", "Figma": "vectoreditor",
			"DaVinci Resolve": "videoeditor", "HandBrake": "videoeditor", "Adobe After Effects": "motiongraphics", "Blender": "model3d",
			"Autodesk Fusion": "cad", "Unity Hub": "gameengine", "Godot Engine": "gameengine", "FL Studio 2024": "daw", "Audacity": "audioeditor",
			"Kindle": "ebook", "Netflix": "tv", "Twitch": "livestream", "Dropbox": "cloudstorage", "Google Drive": "cloudstorage",
			"Bitwarden": "passwordmanager", "KeePassXC": "passwordmanager", "NordVPN": "vpn", "Proton VPN": "vpn", "Malwarebytes": "antivirus",
			"qBittorrent": "torrent", "FileZilla": "ftp", "TeamViewer": "remoteaccess", "AnyDesk": "remoteaccess", "Oracle VirtualBox": "virtualmachine",
			"Postman": "apitester", "Docker Desktop": "container", "PuTTY": "sshclient", "HWiNFO64": "hwmonitor", "MSI Afterburner": "hwmonitor",
			"Logitech G HUB": "peripheral", "Razer Synapse": "peripheral", "OpenRGB": "rgb", "Armoury Crate": "pcsuite", "Lenovo Vantage": "pcsuite",
			"Revo Uninstaller": "uninstaller", "Ditto": "clipboard", "AutoHotkey Dash": "automation", "Rainmeter": "customise",
			"Wallpaper Engine": "customise", "f.lux": "nightlight", "DeepL": "translator", "ImgBurn": "discburner", "WinDirStat": "diskanalyzer",
			"TeraCopy": "filecopy", "Everything": "magnifier", "ShareX": "snippingtool", "Power BI Desktop": "analytics", "Instagram": "social",
			"Minecraft Launcher": "gamelauncher", "Notepad++": "notepad", "PyCharm Community Edition": "codeeditor", "Python 3.13": "codeeditor",
			"WhatsApp": "chat", "Zoom": "videocall", "Mozilla Firefox": "browser", "Brave": "browser", "IrfanView 64": "photos",
			"Discord": "chat", "Spotify": "musicapp", "VLC media player": "videoplayer", "OBS Studio": "recorder", "7-Zip File Manager": "filearchive",
		}
		for name, icon := range more {
			if got := p.Match(name); got != icon {
				t.Errorf("%s: %q → %q, want %q", dir, name, got, icon)
			}
		}
		// and nothing grabs an unrelated app
		for _, name := range []string{"Waffle Icons", "Taco Taco", "Some Unknown Tool"} {
			if got := p.Match(name); got != "" {
				t.Errorf("%q matched %q", name, got)
			}
		}
		for _, n := range []string{"folder", "foldermusic", "folderpictures", "foldervideos", "file", "filetext", "filepdf", "fileimage", "filearchive", "fileapp", "fileshortcut"} {
			if !p.has(n) {
				t.Errorf("%s: no %s glyph", dir, n)
			}
		}
	}
}

func TestComposeLayerIcon(t *testing.T) {
	p, err := LoadIconPack("builtin_packs/bw")
	if err != nil || !p.Dynamic || p.Variant != "auto" {
		t.Fatal("pack", err)
	}
	f, err := os.Open(p.Path("camera"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mask, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bg, ink := layerColours(true)
	acc := [3]uint8{0x3a, 0x6a, 0xd8}
	img := composeLayerIcon(mask, bg, ink, acc)
	count := map[[3]uint8]int{}
	for i := 0; i < len(img.Pix); i += 4 {
		count[[3]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2]}]++
	}
	if count[bg] < 30000 || count[ink] < 3000 || count[acc] < 100 {
		t.Fatalf("tile %d, lines %d, accent %d", count[bg], count[ink], count[acc])
	}
	// light tiles and another accent: same layers, other colours
	bg2, ink2 := layerColours(false)
	img2 := composeLayerIcon(mask, bg2, ink2, [3]uint8{255, 0, 0})
	if img2.Pix[0] != bg2[0] || img2.Pix[3] != 255 {
		t.Fatal("light tile corner", img2.Pix[:4])
	}
}
