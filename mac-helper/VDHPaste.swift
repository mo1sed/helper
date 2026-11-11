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

    @objc func pick(_ sender: NSMenuItem) {
        guard let info = sender.representedObject as? PickInfo else { return }
        // let the menu close first, then paste into the real frontmost app
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.08) { pasteText(info.text); markUsed(info.id) }
    }

    // Pop the messages menu at the mouse (triggered by the hotkey).
    func popAtMouse() {
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
