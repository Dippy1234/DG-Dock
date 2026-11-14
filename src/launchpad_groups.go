package main

// Launchpad groups: the categories DG-Dock sorts apps into by itself, and folders you
// make. You can move any app to another category or folder; that choice is kept.

import "strings"

// The built-in categories, in the order they're shown.
var launchCategories = []string{"Productivity", "Creative", "Internet", "Social", "Media", "Games", "AI", "Developer", "Utilities", "System", "Other"}

type catRule struct {
	cat   string
	words []string // single words match whole words; phrases or long words match anywhere
}

var catRules = []catRule{
	{"Games", []string{"steam", "steamapps", "epic games", "xbox", "game", "games", "gaming", "minecraft", "riot", "valorant", "league of legends",
		"battle.net", "ubisoft", "ea app", "origin", "gog galaxy", "roblox", "fortnite", "playnite", "solitaire", "mahjong", "chess", "rockstar",
		"geforce now", "nvidia app", "heroic", "retroarch", "emulator", "dolphin", "yuzu", "ryujinx", "pcsx2", "cemu", "minesweeper", "rungameid"}},
	{"AI", []string{"chatgpt", "claude", "copilot", "gemini", "perplexity", "ollama", "lm studio", "openai", "grok", "deepseek"}},
	{"Developer", []string{"visual studio", "vs code", "code", "terminal", "powershell", "command prompt", "git", "github", "gitkraken", "docker", "python",
		"node.js", "android studio", "intellij", "pycharm", "webstorm", "rider", "clion", "jetbrains", "postman", "wsl", "ubuntu", "debian", "sql",
		"putty", "winscp", "sublime", "notepad++", "cursor", "windsurf", "unity", "unreal", "godot", "xcode", "insomnia", "dbeaver", "wireshark",
		"virtualbox", "vmware", "hyper-v", "developer", "sdk", "arduino", "filezilla"}},
	{"Creative", []string{"photoshop", "illustrator", "lightroom", "premiere", "after effects", "indesign", "affinity", "figma", "blender", "gimp",
		"krita", "inkscape", "paint", "canva", "davinci", "resolve", "capcut", "clipchamp", "obs", "audacity", "fl studio", "ableton", "cubase",
		"cinema 4d", "sketchup", "autocad", "fusion 360", "substance", "zbrush", "maya", "3ds max", "procreate", "handbrake", "shotcut",
		"kdenlive", "vegas", "camtasia", "snagit", "darktable", "rawtherapee", "design", "studio"}},
	{"Social", []string{"discord", "whatsapp", "telegram", "signal", "slack", "teams", "zoom", "skype", "messenger", "instagram", "facebook",
		"twitter", "snapchat", "phone link", "wechat", "line", "reddit", "linkedin", "threads", "tiktok", "viber", "element", "mastodon"}},
	{"Internet", []string{"edge", "chrome", "firefox", "opera", "brave", "vivaldi", "browser", "arc", "tor", "thunderbird", "qbittorrent",
		"utorrent", "internet", "web"}},
	{"Media", []string{"spotify", "netflix", "vlc", "media player", "music", "movies", "tv", "photos", "camera", "prime video", "disney",
		"youtube", "plex", "itunes", "apple music", "tidal", "voice recorder", "sound recorder", "kodi", "jellyfin", "podcast", "audible",
		"kindle", "player", "mpc-hc", "potplayer", "foobar", "musicbee", "twitch", "crunchyroll", "hulu"}},
	{"Productivity", []string{"word", "excel", "powerpoint", "outlook", "onenote", "office", "access", "publisher", "visio", "project",
		"notion", "obsidian", "evernote", "acrobat", "pdf", "to do", "todo", "calendar", "mail", "libreoffice", "notepad", "wordpad",
		"sticky notes", "trello", "asana", "clickup", "todoist", "anydesk", "teamviewer", "remote", "scanner", "print", "fax", "onedrive", "dropbox",
		"google drive", "box", "zotero", "grammarly", "joplin", "logseq"}},
	{"Utilities", []string{"calculator", "clock", "alarms", "weather", "maps", "snipping", "7-zip", "winrar", "zip", "everything", "powertoys",
		"ccleaner", "nvidia", "amd", "radeon", "intel", "razer", "synapse", "armoury", "logitech", "logi", "corsair", "steelseries", "icue",
		"blade control", "keepass", "bitwarden", "1password", "lastpass", "vpn", "nordvpn", "proton", "cpu-z", "gpu-z", "hwinfo", "hwmonitor",
		"rufus", "revo", "file explorer", "explorer", "task manager", "backup", "malwarebytes", "antivirus", "norton", "mcafee", "avast",
		"defrag", "disk", "cleanup", "screen", "recorder", "translator", "authenticator", "msi afterburner", "afterburner", "fan", "dg-dock",
		"dg-wallpaper", "sharex", "greenshot", "lightshot", "f.lux", "monitor", "uninstaller", "driver", "update"}},
	{"System", []string{"settings", "control panel", "windows tools", "windows security", "security", "administrative", "uninstall",
		"device manager", "recovery", "remote desktop", "character map", "magnifier", "narrator", "on-screen keyboard", "accessibility",
		"run", "help", "get help", "tips", "feedback hub", "microsoft store", "store", "windows", "system", "registry", "event viewer",
		"services", "computer management", "disk management", "readme", "release notes", "license", "manual", "documentation",
		"website", "support", "quick assist", "windows backup", "dev home", "get started"}},
}

// autoCategory guesses an app's category from its name and where it's installed.
func autoCategory(id, name string) string {
	n := strings.ToLower(name)
	idl := strings.ToLower(id)
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(n, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '.' || r == '-')
	}) {
		words[w] = true
	}
	match := func(k string) bool {
		if strings.ContainsAny(k, " .-+") || len(k) > 6 {
			return strings.Contains(n, k)
		}
		return words[k]
	}
	// where it's installed says the most for games
	for _, k := range []string{`\steamapps\`, `\epic games\`, `\riot games\`, `\gog galaxy\games`, `\ubisoft game launcher\games`, "steam://", `\xboxgames\`} {
		if strings.Contains(idl, k) {
			return "Games"
		}
	}
	for _, r := range catRules {
		for _, k := range r.words {
			if match(k) {
				return r.cat
			}
		}
	}
	return "Other"
}

func isCategory(g string) bool { return hasString(launchCategories, g) }

func hasString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// isHelperApp: Start menu entries that aren't really apps (uninstallers, readmes…).
func isHelperApp(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, k := range []string{"uninstall", "readme", "read me", "release notes", "changelog", "license", "licence",
		"documentation", "user guide", "user manual", "online help", "website", "visit ", "eula", "what's new", "debuggable package manager"} {
		if strings.Contains(n, k) {
			return true
		}
	}
	return n == "help" || strings.HasSuffix(n, " help") || strings.HasSuffix(n, " manual") || strings.HasSuffix(n, " docs")
}

// sameAppName drops the bits that tell copies of one app apart: "(x64)", "(64-bit)"…
func sameAppName(name string) string {
	n := strings.ToLower(name)
	for _, k := range []string{"(x64)", "(x86)", "(arm64)", "(64-bit)", "(32-bit)", "64-bit", "32-bit", " x64", " x86", " (64 bit)", " (32 bit)"} {
		n = strings.ReplaceAll(n, k, "")
	}
	return strings.Join(strings.Fields(n), " ")
}
