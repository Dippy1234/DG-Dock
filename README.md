# DG-Dock

**A fast dock for Windows 11.** Your apps, folders, search, music and system tray in one place at the bottom of the screen — so you never need the taskbar.

https://github.com/user-attachments/assets/7439d04d-214e-4c8b-90d3-0eb70fcfe6f7

**Free to download and use.** If you enjoy it, please consider [sponsoring development](https://github.com/sponsors/Dippy1234) ♥

---

## Features

- **Pill-style dock** that matches Windows' light/dark mode and taskbar colour
- **Smooth magnification**, labels and bounce, at your screen's refresh rate
- **Live window previews** — point at an open app to see (and close) its windows
- **Drag and drop** — drag apps on to add, along to reorder, off to remove
- **Two pages of apps** with page dots; scroll to flip, and the dock resizes to fit
- **Open-apps pill** — unpinned apps slide out into their own pill
- **Folder cards** — browse folders right from the dock, with scrolling
- **Search** (Start button or <kbd>Alt</kbd>+<kbd>Space</kbd>) — apps, settings, files, quick sums and the web
- **Widgets** — clock, battery ring with Wi-Fi and scroll-for-brightness, now-playing with live sound wave and scroll-for-volume
- **System tray icons** and **Show desktop** at the end of the dock
- **Your own icons** — change any icon, or import an Android icon pack (.apk)
- **Auto-hide, smart hide, keep space for the dock, hide the Windows taskbar**
- **Bottom, left or right**, on your main display or the one your mouse is on
- **Adjustable animation speed** and a Windows 11-style settings panel

📖 The full **[How to Use Guide (PDF)](docs/DG-Dock%20How%20to%20Use%20Guide.pdf)** explains every feature with pictures.

## Download and install

1. Download **DG-Dock-Setup-1.0.0.exe** from the [Releases](../../releases) page.
2. Double-click it. If Windows shows *“Windows protected your PC”*, click **More info → Run anyway** (this appears for new programs that aren't signed yet).
3. Accept the licence, choose your options and click **Install**.

Installs just for your account — no administrator password needed. Requires Windows 11 (64-bit) and the Microsoft Edge WebView2 Runtime, which comes with Windows 11.

**Uninstall:** Settings › Apps › Installed apps › DG-Dock › Uninstall. The Windows taskbar is put back automatically.

## Support DG-Dock

DG-Dock is made by one person in their spare time and is free for everyone. If it's useful to you, a donation helps keep it going:

- ♥ **[Sponsor DG-Dock on GitHub](https://github.com/sponsors/Dippy1234)** — or use the **♥ Sponsor** button at the top of this page.

Inside the app, the **♥** button in Settings (and Settings › About) takes you there too.

Donations are voluntary gifts and don't change the licence.

## Licence

DG-Dock is **free to use** but it is **not open source**. © 2026 Dippy_drippy — all rights reserved.

| You may | You may not |
|---|---|
| Download, install and use it for free, at home or at work | Sell it, or charge for it in any way |
| Read the source code and build it for your own use | Upload or share copies anywhere except this official page |
| Link people to this page | Publish your own modified version |
| Suggest changes through issues and pull requests | Use the DG-Dock name or icon for something else |

The full terms are in [LICENSE.txt](LICENSE.txt). By sending a contribution you agree to section 4 of the licence (the author may use it in any version of DG-Dock, including paid versions).

DG-Dock includes open-source components under their own licences — see [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt).

Windows is a trademark of Microsoft Corporation. DG-Dock is not affiliated with Microsoft.

## Building from source (for your own use)

The program's code is in the `src` folder. Requirements: Go 1.24 or later. Dependencies are included in `src/vendor`.

```
cd src
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H windowsgui" -o DG-Dock.exe .
```

The icon, manifest and version details come from `src/rsrc_windows_amd64.syso` (regenerate it from `src/versioninfo.json` with [goversioninfo](https://github.com/josephspurrier/goversioninfo) if you change them). Add `-tags dev` for a test build that offers to update itself from newer builds placed in your Downloads folder.

## Project layout

| Folder | What's in it |
|---|---|
| `src/` | The program: Go code, the Search / Settings / Setup pages, and its dependencies |
| `docs/` | The How to Use Guide (PDF) |
| `assets/` | The app icon |

## Reporting a problem

Open an [issue](../../issues) with what happened, what you expected, and your Windows version. Screenshots help a lot.
