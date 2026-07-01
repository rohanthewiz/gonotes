#!/usr/bin/env bash
#
# mac-install.sh — Install (or update) GoNotes as a native macOS app.
#
# Pulls the latest master branch into ~/.gonotes-src, ensures Go >= 1.24 is available
# (auto-installing a private copy under ~/.local/go if needed), builds gonotes,
# then creates ~/Applications/GoNotes.app with a small Swift/WebKit wrapper.
#
# The app starts the bundled gonotes server and displays it in its own macOS
# window instead of opening the UI in a browser.
#
# WARNING: this script owns ~/.gonotes-src. Re-running it will `git reset --hard`
# that directory to origin/$GN_BRANCH — do not put local edits there.
# (~/.gonotes-src holds the source checkout; app data lives elsewhere.)
#
# Usage:
#   ./mac-install.sh
#   curl -fsSL https://raw.githubusercontent.com/rohanthewiz/gonotes/master/mac-install.sh | bash
#
# Env overrides:
#   GN_REPO_URL    git remote   (default: https://github.com/rohanthewiz/gonotes.git)
#   GN_BRANCH      branch       (default: master)
#   GN_DIR         repo dir     (default: $HOME/.gonotes-src)
#   GN_GO_VERSION  Go to fetch  (default: 1.24.4)
#   GN_GO_DIR      Go install   (default: $HOME/.local/go)
#   GN_APP_DIR     app dir      (default: $HOME/Applications)
#   GN_APP_NAME    app name     (default: GoNotes)
#   GN_PORT        server port  (default: 8444 — passed to the server via --port)

set -euo pipefail

GN_REPO_URL="${GN_REPO_URL:-https://github.com/rohanthewiz/gonotes.git}"
GN_BRANCH="${GN_BRANCH:-master}"
GN_DIR="${GN_DIR:-$HOME/.gonotes-src}"
GN_GO_VERSION="${GN_GO_VERSION:-1.24.4}"
GN_GO_DIR="${GN_GO_DIR:-$HOME/.local/go}"
GN_APP_DIR="${GN_APP_DIR:-$HOME/Applications}"
GN_APP_NAME="${GN_APP_NAME:-GoNotes}"
GN_PORT="${GN_PORT:-8444}"

# ---- output helpers --------------------------------------------------------

if [ -t 1 ]; then
  C_BLUE=$'\033[34m'; C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'
  C_GREEN=$'\033[32m'; C_CYAN=$'\033[36m'; C_DIM=$'\033[2m'; C_RESET=$'\033[0m'
else
  C_BLUE=""; C_YELLOW=""; C_RED=""; C_GREEN=""; C_CYAN=""; C_DIM=""; C_RESET=""
fi

# ---- banner ----------------------------------------------------------------

banner() {
  printf '%s' "$C_CYAN"
  cat <<'ART'

   ██████╗  ██████╗ ███╗   ██╗ ██████╗ ████████╗███████╗███████╗
  ██╔════╝ ██╔═══██╗████╗  ██║██╔═══██╗╚══██╔══╝██╔════╝██╔════╝
  ██║  ███╗██║   ██║██╔██╗ ██║██║   ██║   ██║   █████╗  ███████╗
  ██║   ██║██║   ██║██║╚██╗██║██║   ██║   ██╔══╝  ╚════██║
  ╚██████╔╝╚██████╔╝██║ ╚████║╚██████╔╝   ██║   ███████╗███████║
   ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝ ╚═════╝    ╚═╝   ╚══════╝╚══════╝
ART
  printf '%s' "$C_RESET"
  printf '   %sYour notes, natively on your Mac%s\n\n' "$C_CYAN" "$C_RESET"
}

info() { printf '%s==>%s %s\n' "$C_BLUE" "$C_RESET" "$*"; }
ok()   { printf '%s ok%s %s\n'  "$C_GREEN" "$C_RESET" "$*"; }
warn() { printf '%swarn%s %s\n' "$C_YELLOW" "$C_RESET" "$*" >&2; }
err()  { printf '%serror%s %s\n' "$C_RED" "$C_RESET" "$*" >&2; }
die()  { err "$*"; exit 1; }

# ---- platform detect -------------------------------------------------------

detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  [ "$os" = "darwin" ] || die "mac-install.sh requires macOS"

  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  arch="amd64";;
    aarch64|arm64) arch="arm64";;
    *) die "unsupported arch: $arch (need amd64 or arm64)";;
  esac

  OS="$os"
  ARCH="$arch"
}

# ---- prerequisites ---------------------------------------------------------

require_git() {
  if command -v git >/dev/null 2>&1; then return 0; fi
  die "git not found. Install with: xcode-select --install"
}

require_swiftc() {
  if command -v swiftc >/dev/null 2>&1; then return 0; fi
  die "swiftc not found. Install Xcode Command Line Tools with: xcode-select --install"
}

# gonotes links DuckDB via cgo, so the build needs a working C compiler. The
# Xcode Command Line Tools (already required for swiftc) provide clang.
require_cc() {
  if command -v cc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1; then return 0; fi
  die "no C compiler found (gonotes needs cgo). Install with: xcode-select --install"
}

# fetcher: prefer curl, fall back to wget. Args: url, out-file
download() {
  local url="$1" out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --retry-delay 2 -o "$out" "$url"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$out" "$url"
  else
    die "neither curl nor wget found; cannot download $url"
  fi
}

# ---- repo sync -------------------------------------------------------------

sync_repo() {
  if [ -d "$GN_DIR/.git" ]; then
    info "updating $GN_DIR from origin/$GN_BRANCH"
    git -C "$GN_DIR" remote set-url origin "$GN_REPO_URL"
    git -C "$GN_DIR" fetch --depth=1 origin "$GN_BRANCH"
    git -C "$GN_DIR" checkout -q "$GN_BRANCH" 2>/dev/null || git -C "$GN_DIR" checkout -q -B "$GN_BRANCH" "origin/$GN_BRANCH"
    git -C "$GN_DIR" reset --hard "origin/$GN_BRANCH"
  else
    if [ -e "$GN_DIR" ]; then
      die "$GN_DIR exists but is not a git checkout; refusing to overwrite. Remove it or set GN_DIR."
    fi
    info "cloning $GN_REPO_URL into $GN_DIR"
    git clone --depth=1 --branch "$GN_BRANCH" "$GN_REPO_URL" "$GN_DIR"
  fi
  ok "repo at $(git -C "$GN_DIR" rev-parse --short HEAD)"
}

# ---- Go resolution ---------------------------------------------------------

# version_ge HAVE WANT  -> 0 if HAVE >= WANT, 1 otherwise. Pure shell.
version_ge() {
  local have="$1" want="$2" h w i
  IFS=. read -r -a h <<< "$have"
  IFS=. read -r -a w <<< "$want"
  for i in 0 1 2; do
    local hi="${h[$i]:-0}" wi="${w[$i]:-0}"
    hi="${hi%%[^0-9]*}"; wi="${wi%%[^0-9]*}"
    hi="${hi:-0}"; wi="${wi:-0}"
    if   [ "$hi" -gt "$wi" ]; then return 0
    elif [ "$hi" -lt "$wi" ]; then return 1
    fi
  done
  return 0
}

# go_version_of GO_BIN -> prints "1.24.4" or empty on failure
go_version_of() {
  local bin="$1" v
  v="$("$bin" env GOVERSION 2>/dev/null || true)"
  v="${v#go}"
  printf '%s' "$v"
}

resolve_go() {
  local sys_go sys_ver local_go local_ver
  GO_BIN=""
  GO_SOURCE=""

  if command -v go >/dev/null 2>&1; then
    sys_go="$(command -v go)"
    sys_ver="$(go_version_of "$sys_go")"
    if [ -n "$sys_ver" ] && version_ge "$sys_ver" "$GN_GO_VERSION"; then
      GO_BIN="$sys_go"
      GO_VERSION="$sys_ver"
      GO_SOURCE="system"
      ok "using system Go $sys_ver at $sys_go"
      return
    fi
    warn "system Go $sys_ver at $sys_go is older than required $GN_GO_VERSION"
  fi

  local_go="$GN_GO_DIR/bin/go"
  if [ -x "$local_go" ]; then
    local_ver="$(go_version_of "$local_go")"
    if [ -n "$local_ver" ] && version_ge "$local_ver" "$GN_GO_VERSION"; then
      GO_BIN="$local_go"
      GO_VERSION="$local_ver"
      GO_SOURCE="local-cached"
      ok "using cached Go $local_ver at $local_go"
      return
    fi
  fi

  install_go_local
  GO_BIN="$GN_GO_DIR/bin/go"
  GO_VERSION="$(go_version_of "$GO_BIN")"
  [ -n "$GO_VERSION" ] || die "installed Go but '$GO_BIN env GOVERSION' returned empty"
  GO_SOURCE="local-installed"
  ok "installed Go $GO_VERSION at $GO_BIN"
}

install_go_local() {
  local url tmp tarball
  url="https://go.dev/dl/go${GN_GO_VERSION}.${OS}-${ARCH}.tar.gz"
  info "downloading Go $GN_GO_VERSION for ${OS}/${ARCH}"
  printf '    %s%s%s\n' "$C_DIM" "$url" "$C_RESET"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"; trap - RETURN' RETURN
  tarball="$tmp/go.tar.gz"

  download "$url" "$tarball"
  tar -xzf "$tarball" -C "$tmp"
  [ -x "$tmp/go/bin/go" ] || die "extracted archive missing go/bin/go"

  mkdir -p "$(dirname "$GN_GO_DIR")"
  rm -rf "$GN_GO_DIR"
  mv "$tmp/go" "$GN_GO_DIR"
}

# ---- build -----------------------------------------------------------------

build_gonotes() {
  local build_id
  build_id="$(git -C "$GN_DIR" rev-parse --short HEAD)"
  info "building gonotes (cgo/DuckDB — this can take a minute)"
  ( cd "$GN_DIR" && \
    CGO_ENABLED=1 "$GO_BIN" build -trimpath \
      -ldflags "-s -w" \
      -o gonotes . )
  [ -x "$GN_DIR/gonotes" ] || die "build reported success but $GN_DIR/gonotes is missing"
  ok "built $GN_DIR/gonotes"
  GN_BUILD_ID="$build_id"
}

# ---- app icon --------------------------------------------------------------

# build_app_icon RESOURCES_DIR -> writes RESOURCES_DIR/GoNotes.icns, returns 0
# on success. Renders a squircle with a blue->violet gradient and a white note
# page (folded corner + text lines) via a tiny AppKit program. Offscreen AppKit
# drawing needs a window-server connection, so this can fail on a headless/SSH
# install — callers treat failure as non-fatal.
build_app_icon() {
  local resources="$1" tmp icon_swift icon_bin iconset
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"; trap - RETURN' RETURN
  icon_swift="$tmp/MakeIcon.swift"
  icon_bin="$tmp/makeicon"
  iconset="$tmp/GoNotes.iconset"
  mkdir -p "$iconset"

  cat > "$icon_swift" <<'SWIFT'
import AppKit
import Foundation

func draw(_ size: CGFloat) {
    guard let ctx = NSGraphicsContext.current?.cgContext else { return }
    let full = CGRect(x: 0, y: 0, width: size, height: size)
    ctx.clear(full)

    // macOS-style squircle background.
    let inset = size * 0.045
    let r = full.insetBy(dx: inset, dy: inset)
    let bg = NSBezierPath(roundedRect: r, xRadius: r.width * 0.2237, yRadius: r.height * 0.2237)
    bg.addClip()

    let top = NSColor(srgbRed: 0.36, green: 0.47, blue: 0.96, alpha: 1.0)
    let bot = NSColor(srgbRed: 0.55, green: 0.29, blue: 0.86, alpha: 1.0)
    if let grad = NSGradient(starting: top, ending: bot) {
        grad.draw(in: r, angle: -50)
    }

    // White note page with a folded top-right corner, centered.
    let pageW = size * 0.46
    let pageH = size * 0.56
    let px = (size - pageW) / 2
    let py = (size - pageH) / 2
    let fold = size * 0.13

    let page = NSBezierPath()
    page.move(to: CGPoint(x: px, y: py))                          // bottom-left
    page.line(to: CGPoint(x: px + pageW, y: py))                  // bottom-right
    page.line(to: CGPoint(x: px + pageW, y: py + pageH - fold))   // up right side to fold
    page.line(to: CGPoint(x: px + pageW - fold, y: py + pageH))   // diagonal fold cut
    page.line(to: CGPoint(x: px, y: py + pageH))                  // top-left
    page.close()
    NSColor.white.setFill()
    page.fill()

    // The folded-over corner triangle, lightly shaded.
    let foldPath = NSBezierPath()
    foldPath.move(to: CGPoint(x: px + pageW - fold, y: py + pageH))
    foldPath.line(to: CGPoint(x: px + pageW - fold, y: py + pageH - fold))
    foldPath.line(to: CGPoint(x: px + pageW, y: py + pageH - fold))
    foldPath.close()
    NSColor(white: 0.78, alpha: 1.0).setFill()
    foldPath.fill()

    // Text lines on the page. The first is short (a title), the rest full width.
    let lineColor = NSColor(srgbRed: 0.46, green: 0.40, blue: 0.78, alpha: 1.0)
    lineColor.setStroke()
    let marginX = pageW * 0.17
    let lineX0 = px + marginX
    let lineX1 = px + pageW - marginX
    let firstY = py + pageH - fold - size * 0.075
    let gap = size * 0.078
    for i in 0..<4 {
        let y = firstY - CGFloat(i) * gap
        let p = NSBezierPath()
        p.move(to: CGPoint(x: lineX0, y: y))
        let x1 = i == 0 ? (lineX0 + (lineX1 - lineX0) * 0.55) : lineX1
        p.line(to: CGPoint(x: x1, y: y))
        p.lineWidth = size * 0.024
        p.lineCapStyle = .round
        p.stroke()
    }
}

func png(_ px: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: px, pixelsHigh: px,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
    draw(CGFloat(px))
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

let outDir = CommandLine.arguments[1]
let targets: [(String, Int)] = [
    ("icon_16x16.png", 16),    ("icon_16x16@2x.png", 32),
    ("icon_32x32.png", 32),    ("icon_32x32@2x.png", 64),
    ("icon_128x128.png", 128), ("icon_128x128@2x.png", 256),
    ("icon_256x256.png", 256), ("icon_256x256@2x.png", 512),
    ("icon_512x512.png", 512), ("icon_512x512@2x.png", 1024),
]
for (name, px) in targets {
    try png(px).write(to: URL(fileURLWithPath: outDir).appendingPathComponent(name))
}
SWIFT

  swiftc "$icon_swift" -o "$icon_bin" -framework AppKit >/dev/null 2>&1 || return 1
  "$icon_bin" "$iconset" >/dev/null 2>&1 || return 1
  iconutil -c icns "$iconset" -o "$resources/GoNotes.icns" >/dev/null 2>&1 || return 1
  [ -f "$resources/GoNotes.icns" ]
}

# ---- macOS app -------------------------------------------------------------

install_macos_app() {
  local app_path contents macos resources plist swift_src app_exe bundled_bin bundle_id
  app_path="$GN_APP_DIR/$GN_APP_NAME.app"
  contents="$app_path/Contents"
  macos="$contents/MacOS"
  resources="$contents/Resources"
  plist="$contents/Info.plist"
  swift_src="$resources/GoNotesApp.swift"
  app_exe="$macos/$GN_APP_NAME"
  bundled_bin="$resources/gonotes"
  bundle_id="dev.gonotes.GoNotes"

  info "installing native macOS app at $app_path"
  rm -rf "$app_path"
  mkdir -p "$macos" "$resources"
  cp "$GN_DIR/gonotes" "$bundled_bin"
  chmod +x "$bundled_bin"

  local icon_plist=""
  info "generating app icon"
  if build_app_icon "$resources"; then
    icon_plist=$'  <key>CFBundleIconFile</key>\n  <string>GoNotes</string>'
    ok "app icon generated"
  else
    warn "could not generate app icon (continuing without one)"
  fi

  cat > "$plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleDisplayName</key>
  <string>$GN_APP_NAME</string>
  <key>CFBundleExecutable</key>
  <string>$GN_APP_NAME</string>
  <key>CFBundleIdentifier</key>
  <string>$bundle_id</string>
$icon_plist
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>$GN_APP_NAME</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>1.0</string>
  <key>CFBundleVersion</key>
  <string>$GN_BUILD_ID</string>
  <key>LSMinimumSystemVersion</key>
  <string>11.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
EOF

  cat > "$swift_src" <<EOF
import AppKit
import Foundation
import WebKit

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate {
    private var window: NSWindow!
    private var webView: WKWebView!
    private var serverProcess: Process?
    private let port = "$GN_PORT"
    private var baseURL: URL { URL(string: "http://127.0.0.1:\(port)")! }
    private var healthURL: URL { baseURL.appendingPathComponent("api/v1/health") }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        applyAppIcon()
        buildMenu()
        buildWindow()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        Task { @MainActor in
            if await waitForServer(timeout: 0.5) {
                loadApp()
                return
            }
            startServer()
            if await waitForServer(timeout: 20.0) {
                loadApp()
            } else {
                showError("GoNotes did not become ready. Check ~/Library/Logs/GoNotes/gonotes.log for details.")
            }
        }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    // Without a main menu, AppKit never binds the standard Cmd+Q (and other
    // edit shortcuts), so the app appears unresponsive to Quit. Build a minimal
    // application menu that declares them.
    private func buildMenu() {
        let mainMenu = NSMenu()

        let appMenuItem = NSMenuItem()
        let appMenu = NSMenu()
        let name = "$GN_APP_NAME"
        appMenu.addItem(withTitle: "Hide \(name)", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        let hideOthers = appMenu.addItem(withTitle: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
        hideOthers.keyEquivalentModifierMask = [.command, .option]
        appMenu.addItem(withTitle: "Show All", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        appMenu.addItem(NSMenuItem.separator())
        appMenu.addItem(withTitle: "Quit \(name)", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appMenuItem.submenu = appMenu
        mainMenu.addItem(appMenuItem)

        // An Edit menu so Cmd+C/V/X/A/Z work inside the web view.
        let editMenuItem = NSMenuItem()
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        let redo = editMenu.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "z")
        redo.keyEquivalentModifierMask = [.command, .shift]
        editMenu.addItem(NSMenuItem.separator())
        editMenu.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        editMenu.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editMenuItem.submenu = editMenu
        mainMenu.addItem(editMenuItem)

        NSApp.mainMenu = mainMenu
    }

    // The Cmd+Tab switcher and Dock render the running app's applicationIconImage,
    // not the bundle's CFBundleIconFile directly. macOS caches that runtime icon by
    // bundle id, so a re-installed bundle can keep showing a stale/generic icon there
    // even when Finder and Spotlight already pick up the new GoNotes.icns. Setting the
    // icon explicitly at launch bypasses that cache.
    private func applyAppIcon() {
        guard let iconURL = Bundle.main.url(forResource: "GoNotes", withExtension: "icns"),
              let icon = NSImage(contentsOf: iconURL) else { return }
        NSApp.applicationIconImage = icon
    }

    func applicationWillTerminate(_ notification: Notification) {
        serverProcess?.terminate()
    }

    private func buildWindow() {
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .default()
        webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = self

        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.center()
        window.title = "$GN_APP_NAME"
        window.contentView = webView
    }

    private func startServer() {
        guard let binURL = Bundle.main.resourceURL?.appendingPathComponent("gonotes") else {
            showError("The bundled gonotes binary is missing.")
            return
        }

        // The gonotes server resolves its DuckDB file and config under its working
        // directory (set via --dir). Use ~/.gonotes — the same default the CLI
        // uses — so the app and command line share one database rather than the
        // read-only app bundle.
        let dataDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".gonotes")
        try? FileManager.default.createDirectory(at: dataDir, withIntermediateDirectories: true)

        let logDir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library")
            .appendingPathComponent("Logs")
            .appendingPathComponent("GoNotes")
        try? FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        let logURL = logDir.appendingPathComponent("gonotes.log")
        FileManager.default.createFile(atPath: logURL.path, contents: nil)

        let process = Process()
        process.executableURL = binURL
        process.arguments = ["--dir", dataDir.path, "--port", port]

        if let logHandle = try? FileHandle(forWritingTo: logURL) {
          _ = try? logHandle.seekToEnd()
            process.standardOutput = logHandle
            process.standardError = logHandle
        }

        do {
            try process.run()
            serverProcess = process
        } catch {
            showError("GoNotes could not start: \(error.localizedDescription)")
        }
    }

    private func waitForServer(timeout: TimeInterval) async -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        repeat {
            if await isHealthy() { return true }
            try? await Task.sleep(nanoseconds: 250_000_000)
        } while Date() < deadline
        return false
    }

    private func isHealthy() async -> Bool {
        var request = URLRequest(url: healthURL)
        request.timeoutInterval = 0.4
        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            return (response as? HTTPURLResponse)?.statusCode == 200
        } catch {
            return false
        }
    }

    private func loadApp() {
        webView.load(URLRequest(url: baseURL))
    }

    private func showError(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "GoNotes could not start"
        alert.informativeText = message
        alert.alertStyle = .critical
        alert.runModal()
        NSApp.terminate(nil)
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
EOF

  swiftc "$swift_src" -o "$app_exe" -framework AppKit -framework WebKit
  chmod +x "$app_exe"
  ok "app installed at $app_path"
}

# ---- main ------------------------------------------------------------------

main() {
  banner
  detect_platform
  require_git
  require_swiftc
  require_cc
  sync_repo
  resolve_go
  build_gonotes
  install_macos_app

  printf '\n'
  printf '%s✓ GoNotes %s app installed%s\n' "$C_GREEN" "$GN_BUILD_ID" "$C_RESET"
  printf '  repo: %s\n' "$GN_DIR"
  printf '  app:  %s\n' "$GN_APP_DIR/$GN_APP_NAME.app"
  printf '  data: ~/.gonotes/data (shared with the gonotes CLI)\n'
  printf '  Go:   %s (%s)\n' "$GO_VERSION" "$GO_SOURCE"
  printf '\nOpen %s%s.app%s from Finder, Spotlight, or the Dock.\n' "$C_GREEN" "$GN_APP_NAME" "$C_RESET"
  printf 'Logs: ~/Library/Logs/GoNotes/gonotes.log\n'
  printf '\nRe-run this installer any time to update to the latest %s.\n' "$GN_BRANCH"
}

main "$@"
