// Vice Dolls Helper — Quick Paste helper for macOS.
// Lives in the menu bar. A global hotkey (⌃⌥V by default) pops a menu of your
// saved messages at the mouse; picking one pastes it into the focused field.
// Reads the same file the main app writes.
//
// Build:  swiftc -O VDHPaste.swift -o VDHPaste -framework Cocoa -framework Carbon
// Run:    ./VDHPaste
//
// First paste asks for Accessibility permission (System Settings → Privacy &
// Security → Accessibility). Enable VDHPaste there once.

import Cocoa
import Carbon

let DATA_PATH = NSString(string: "~/Library/Application Support/ViceDollsHelper/vicedolls-data.json").expandingTildeInPath

// MARK: - Data model

struct Sub { let id: String; let name: String }
struct Folder { let id: String; let name: String; let subs: [Sub]; var hidden: Bool = false; var auto: Bool = false }
struct Msg { let folder: String; let sub: String; let text: String; let label: String; let order: Double?; let createdAt: Double; let hot: Bool; let id: String; let lastUsedAt: Double }

func loadData() -> (folders: [Folder], messages: [Msg], profile: String) {
    guard let data = FileManager.default.contents(atPath: DATA_PATH),
          let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
        return ([], [], "")
    }
    var folders: [Folder] = []
    if let fa = root["folders"] as? [[String: Any]] {
        for f in fa {
            let subsArr = (f["subs"] as? [[String: Any]]) ?? []
            let subs = subsArr.map { Sub(id: ($0["id"] as? String) ?? "", name: ($0["name"] as? String) ?? "") }
            folders.append(Folder(id: (f["id"] as? String) ?? "",
                                  name: (f["name"] as? String) ?? "Папка",
                                  subs: subs,
                                  hidden: (f["qpHidden"] as? Bool) ?? false,
                                  auto: (f["qpAuto"] as? Bool) ?? false))
        }
    }
    var messages: [Msg] = []
    if let ma = root["messages"] as? [[String: Any]] {
        for m in ma {
            messages.append(Msg(folder: (m["folder"] as? String) ?? "",
                                sub: (m["sub"] as? String) ?? "",
                                text: (m["text"] as? String) ?? "",
                                label: (m["label"] as? String) ?? "",
                                order: m["order"] as? Double,
                                createdAt: (m["createdAt"] as? Double) ?? 0,
                                hot: (m["hot"] as? Bool) ?? false,
                                id: (m["id"] as? String) ?? "",
                                lastUsedAt: (m["lastUsedAt"] as? NSNumber)?.doubleValue ?? 0))
        }
    }
    // profile name is shown only when there is more than one profile
    var profile = ""
    if let ps = root["profiles"] as? [[String: Any]], ps.count > 1 {
        let active = (root["activeProfile"] as? String) ?? ""
        profile = (ps.first { ($0["id"] as? String) == active }?["name"] as? String) ?? ""
    }
    return (folders, messages, profile)
}

func sortKey(_ m: Msg) -> Double { m.order ?? -m.createdAt }

// the message used longest ago (random among equals) — for the auto "Back" folder
func pickLeastRecent(_ list: [Msg]) -> Msg? {
    guard let mn = list.map({ $0.lastUsedAt }).min() else { return nil }
    return list.filter { $0.lastUsedAt == mn }.randomElement()
}

// stamp a message as used, leaving every other field of the data file untouched
func markUsed(_ id: String) {
    guard !id.isEmpty,
          let data = FileManager.default.contents(atPath: DATA_PATH),
          var root = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
          var list = root["messages"] as? [[String: Any]] else { return }
    var changed = false
    for i in 0..<list.count where (list[i]["id"] as? String) == id {
        let uses = (list[i]["uses"] as? NSNumber)?.intValue ?? 0
        list[i]["uses"] = uses + 1
        list[i]["lastUsedAt"] = Int64(Date().timeIntervalSince1970 * 1000)
        changed = true
    }
    guard changed else { return }
    root["messages"] = list
    guard let out = try? JSONSerialization.data(withJSONObject: root, options: [.withoutEscapingSlashes]) else { return }
    try? out.write(to: URL(fileURLWithPath: DATA_PATH), options: .atomic)
}

// MARK: - Audience presets: tick folders in the OnlyMonster picker by typing their names

struct AudPreset { let name: String; let skip: [String]; let send: [String]; let macro: [String]; let isMacro: Bool }

func loadAudience() -> (presets: [AudPreset], key: String, pause: Int, click: Bool, wait: Int) {
    guard let data = FileManager.default.contents(atPath: DATA_PATH),
          let root = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
          let au = root["audience"] as? [String: Any] else { return ([], "down_enter", 350, true, 1500) }
    func names(_ v: Any?) -> [String] {
        var out: [String] = []
        for x in (v as? [Any]) ?? [] {
            let t = ((x as? String) ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if !t.isEmpty && !out.contains(where: { $0.lowercased() == t.lowercased() }) { out.append(t) }
        }
        return out
    }
    var presets: [AudPreset] = []
    for p in (au["presets"] as? [[String: Any]]) ?? [] {
        let macro = ((p["macro"] as? [Any]) ?? []).compactMap { ($0 as? String)?.lowercased() }
        presets.append(AudPreset(name: (p["name"] as? String) ?? "Пресет", skip: names(p["skip"]), send: names(p["send"]),
                                 macro: macro, isMacro: (p["mode"] as? String) == "macro"))
    }
    let key = (au["key"] as? String) ?? "down_enter"
    var pause = (au["pause"] as? NSNumber)?.intValue ?? 350
    if pause < 100 { pause = 350 }
    if pause > 3000 { pause = 3000 }
    var wait = (au["wait"] as? NSNumber)?.intValue ?? 1500
    if wait <= 0 { wait = 1500 }
    if wait > 10000 { wait = 10000 }
    return (presets, key, pause, (au["click"] as? Bool) ?? true, wait)
}

final class AudInfo: NSObject {
    let names: [String]; let macro: [String]; let key: String; let pause: Int
    var click = true; var at = CGPoint.zero; var wait = 1500
    init(_ n: [String], _ m: [String], _ k: String, _ p: Int) { names = n; macro = m; key = k; pause = p }
}

let macroCodes: [String: CGKeyCode] = ["down": 125, "up": 126, "enter": 36, "space": 49, "tab": 48]

func sendKey(_ code: CGKeyCode, cmd: Bool = false) {
    let src = CGEventSource(stateID: .combinedSessionState)
    let down = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: true)
    let up = CGEvent(keyboardEventSource: src, virtualKey: code, keyDown: false)
    if cmd { down?.flags = .maskCommand; up?.flags = .maskCommand }
    down?.post(tap: .cghidEventTap)
    up?.post(tap: .cghidEventTap)
}

// For each name: type it into the focused search box, tick it, clear the box.
// Esc or switching to another app stops it right away. It never presses "Send".
func runAudience(_ info: AudInfo) {
    DispatchQueue.global().async {
        Thread.sleep(forTimeInterval: 0.3)
        if info.click { // the picker closes when our menu takes focus: click where the menu was opened to reopen it
            let src = CGEventSource(stateID: .combinedSessionState)
            for t in [CGEventType.leftMouseDown, CGEventType.leftMouseUp] {
                CGEvent(mouseEventSource: src, mouseType: t, mouseCursorPosition: info.at, mouseButton: .left)?.post(tap: .cghidEventTap)
            }
            Thread.sleep(forTimeInterval: Double(info.wait) / 1000.0)
        }
        let target = NSWorkspace.shared.frontmostApplication?.processIdentifier
        let pb = NSPasteboard.general
        let prev = pb.string(forType: .string)
        let ms = Double(info.pause) / 1000.0
        var stopped = false
        func aborted() -> Bool {
            if CGEventSource.keyState(.combinedSessionState, key: 53) { return true } // Esc
            return NSWorkspace.shared.frontmostApplication?.processIdentifier != target
        }
        for t in info.macro { // recorded / built key sequence
            if aborted() { stopped = true; break }
            if t.hasPrefix("wait:"), let w = Double(t.dropFirst(5)) { Thread.sleep(forTimeInterval: min(w, 5000) / 1000.0); continue }
            if let code = macroCodes[t] { sendKey(code); Thread.sleep(forTimeInterval: ms / 3) }
        }
        for n in info.names {
            if aborted() { stopped = true; break }
            DispatchQueue.main.sync { pb.clearContents(); pb.setString(n, forType: .string) }
            sendKey(9, cmd: true)                       // Cmd+V
            Thread.sleep(forTimeInterval: ms)
            switch info.key {
            case "down_enter": sendKey(125); Thread.sleep(forTimeInterval: 0.08); sendKey(36)
            case "space": sendKey(49)
            case "tab": sendKey(48)
            default: sendKey(36)                        // Return
            }
            Thread.sleep(forTimeInterval: ms / 2)
            sendKey(0, cmd: true)                       // Cmd+A
            sendKey(51)                                 // Delete
            Thread.sleep(forTimeInterval: ms / 2)
        }
        DispatchQueue.main.async {
            if let prev = prev { pb.clearContents(); pb.setString(prev, forType: .string) }
            NSSound(named: stopped ? "Basso" : "Glass")?.play()
        }
    }
}

final class PickInfo: NSObject {
    let text: String; let id: String
    init(_ t: String, _ i: String) { text = t; id = i }
}

func menuLabel(_ m: Msg) -> String {
    let base = m.label.trimmingCharacters(in: .whitespaces).isEmpty ? m.text : m.label
    let one = base.replacingOccurrences(of: "\n", with: " ⏎ ").trimmingCharacters(in: .whitespaces)
    return one.count > 60 ? String(one.prefix(60)) + "…" : one
}

// MARK: - Paste

func pasteText(_ text: String) {
    let pb = NSPasteboard.general
    let prev = pb.string(forType: .string)
    pb.clearContents()
    pb.setString(text, forType: .string)
    // Cmd+V into the frontmost app
    let src = CGEventSource(stateID: .combinedSessionState)
    let vKey: CGKeyCode = 9 // 'v'
    let down = CGEvent(keyboardEventSource: src, virtualKey: vKey, keyDown: true)
    down?.flags = .maskCommand
    let up = CGEvent(keyboardEventSource: src, virtualKey: vKey, keyDown: false)
    up?.flags = .maskCommand
    down?.post(tap: .cghidEventTap)
    up?.post(tap: .cghidEventTap)
    // restore the previous clipboard shortly after
    if let prev = prev {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
            pb.clearContents(); pb.setString(prev, forType: .string)
        }
    }
}

// MARK: - App

class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    var statusItem: NSStatusItem!
    var hotKeyRef: EventHotKeyRef?
    var origin = CGPoint.zero

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.accessory)
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let b = statusItem.button { b.title = "VD"; b.toolTip = "Vice Dolls Helper — ⌃⌥V для вставки" }
        let top = NSMenu(); top.delegate = self   // rebuilt each time it opens, so it is always fresh
        statusItem.menu = top
        registerHotKey()
        _ = AXIsProcessTrustedWithOptions(["AXTrustedCheckOptionPrompt": true] as CFDictionary)
    }

    // Build the messages menu. forStatusBar adds Quit at the bottom.
    func buildMenu(forStatusBar: Bool) -> NSMenu {
        let (folders, messages, profile) = loadData()
        let sorted = messages.sorted { a, b in a.hot != b.hot ? a.hot : sortKey(a) < sortKey(b) }
        let menu = NSMenu()
        menu.autoenablesItems = false

        func addMessages(_ into: NSMenu, _ list: [Msg]) {
            if list.isEmpty { let it = NSMenuItem(title: "(пусто)", action: nil, keyEquivalent: ""); it.isEnabled = false; into.addItem(it); return }
            for m in list {
                let it = NSMenuItem(title: menuLabel(m), action: #selector(pick(_:)), keyEquivalent: "")
                it.target = self; it.representedObject = PickInfo(m.text, m.id)
                into.addItem(it)
            }
        }
        let usedFolders = folders.isEmpty
            ? Array(Set(sorted.map { $0.folder })).map { Folder(id: $0, name: $0, subs: []) }
            : folders
        for f in usedFolders {
            if f.hidden { continue } // hidden from the full menu
            let inF = sorted.filter { $0.folder == f.id }
            if f.auto { // one click pastes the message used longest ago
                if let m = pickLeastRecent(inF) {
                    let it = NSMenuItem(title: f.name + "  ↻", action: #selector(pick(_:)), keyEquivalent: "")
                    it.target = self; it.representedObject = PickInfo(m.text, m.id)
                    menu.addItem(it)
                } else {
                    let it = NSMenuItem(title: f.name + " (пусто)", action: nil, keyEquivalent: "")
                    it.isEnabled = false; menu.addItem(it)
                }
                continue
            }
            let fm = NSMenu()
            if f.subs.isEmpty {
                addMessages(fm, inF)
            } else {
                for (k, s) in f.subs.enumerated() {
                    let inS = inF.filter { m in
                        let known = f.subs.contains { $0.id == m.sub }
                        return (known && m.sub == s.id) || (!known && k == 0)
                    }
                    let sm = NSMenu()
                    addMessages(sm, inS)
                    let si = NSMenuItem(title: s.name, action: nil, keyEquivalent: "")
                    si.submenu = sm
                    fm.addItem(si)
                }
            }
            let fi = NSMenuItem(title: f.name, action: nil, keyEquivalent: "")
            fi.submenu = fm
            menu.addItem(fi)
        }
        do {
            let au = loadAudience()
            for kind in ["skip", "send"] {
                let sub = NSMenu()
                for p in au.presets {
                    if p.isMacro && kind == "send" { continue }
                    let list = p.isMacro ? p.macro : (kind == "skip" ? p.skip : p.send)
                    if list.isEmpty { continue }
                    let it = NSMenuItem(title: "\(p.name) (\(p.isMacro ? "⌨ " : "")\(list.count))", action: #selector(pickAud(_:)), keyEquivalent: "")
                    it.target = self
                    let ai = p.isMacro ? AudInfo([], list, au.key, au.pause) : AudInfo(list, [], au.key, au.pause)
                    ai.click = au.click && !forStatusBar; ai.at = origin; ai.wait = au.wait
                    it.representedObject = ai
                    sub.addItem(it)
                }
                if sub.items.isEmpty { continue }
                let top = NSMenuItem(title: kind == "skip" ? "🎯 Не отправлять" : "🎯 Отправить", action: nil, keyEquivalent: "")
                top.submenu = sub
                menu.addItem(top)
            }
        }
        if !profile.isEmpty {
            menu.addItem(.separator())
            let pi = NSMenuItem(title: "Профиль: " + profile, action: nil, keyEquivalent: "")
            pi.isEnabled = false; menu.addItem(pi)
        }
        if forStatusBar {
            menu.addItem(.separator())
            let q = NSMenuItem(title: "Выход", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"); q.target = NSApp
            menu.addItem(q)
        }
        return menu
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        let fresh = buildMenu(forStatusBar: true)
        menu.removeAllItems()
        for it in fresh.items { fresh.removeItem(it); menu.addItem(it) }
    }

    @objc func pickAud(_ sender: NSMenuItem) {
        guard let info = sender.representedObject as? AudInfo else { return }
        runAudience(info)
    }

    @objc func pick(_ sender: NSMenuItem) {
        guard let info = sender.representedObject as? PickInfo else { return }
        // let the menu close first, then paste into the real frontmost app
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.08) { pasteText(info.text); markUsed(info.id) }
    }

    // Pop the messages menu at the mouse (triggered by the hotkey).
    func popAtMouse() {
        // remember where the menu opens (CoreGraphics coordinates have the origin at the top-left)
        let m = NSEvent.mouseLocation
        origin = CGPoint(x: m.x, y: (NSScreen.screens.first?.frame.height ?? 0) - m.y)
        let menu = buildMenu(forStatusBar: false)
        let loc = NSEvent.mouseLocation
        menu.popUp(positioning: nil, at: loc, in: nil)
    }

    // MARK: Carbon global hotkey  ⌃⌥V
    func registerHotKey() {
        var hotKeyID = EventHotKeyID(signature: OSType(0x56444850), id: 1) // 'VDHP'
        let mods = UInt32(controlKey | optionKey)
        let keyV = UInt32(kVK_ANSI_V)
        var eventType = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        let handler: EventHandlerUPP = { _, _, _ -> OSStatus in
            DispatchQueue.main.async { (NSApp.delegate as? AppDelegate)?.popAtMouse() }
            return noErr
        }
        InstallEventHandler(GetApplicationEventTarget(), handler, 1, &eventType, nil, nil)
        RegisterEventHotKey(keyV, mods, hotKeyID, GetApplicationEventTarget(), 0, &hotKeyRef)
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
