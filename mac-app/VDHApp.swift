// Vice Dolls Helper — native macOS window (WKWebView) around the local app server.
// Gives the app its own icon and its own entry in Dock / Command-Tab, instead of
// borrowing the browser's. Launches the bundled server, shows it in a window.
//
// Build is done by the included script; it links Cocoa + WebKit.

import Cocoa
import WebKit

let PORT = 47615
let BASE = "http://127.0.0.1:\(PORT)/"

func serverBinaryPath() -> String {
    let dir = (Bundle.main.executablePath! as NSString).deletingLastPathComponent
    #if arch(arm64)
    return dir + "/vd-arm64"
    #else
    return dir + "/vd-amd64"
    #endif
}

class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate {
    var window: NSWindow!
    var web: WKWebView!
    var server: Process?
    var quitting = false

    func applicationDidFinishLaunching(_ note: Notification) {
        NSApp.setActivationPolicy(.regular)
        startServer()

        let cfg = WKWebViewConfiguration()
        web = WKWebView(frame: .zero, configuration: cfg)
        web.navigationDelegate = self
        web.uiDelegate = self

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1180, height: 820),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        window.title = "Vice Dolls Helper"
        window.minSize = NSSize(width: 760, height: 520)
        window.center()
        window.contentView = web
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        waitAndLoad(0)
    }

    func startServer() {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: serverBinaryPath())
        var env = ProcessInfo.processInfo.environment
        env["VDH_SERVE_ONLY"] = "1"
        p.environment = env
        // When the server exits on its own (e.g. after applying an update), relaunch it
        // with the new binary and reload the page.
        p.terminationHandler = { [weak self] _ in
            guard let self = self, !self.quitting else { return }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
                self.startServer()
                self.waitAndLoad(0)
            }
        }
        try? p.run()
        server = p
    }

    // poll the server until it answers, then load the page
    func waitAndLoad(_ attempt: Int) {
        var req = URLRequest(url: URL(string: BASE + "api/version")!)
        req.timeoutInterval = 1.5
        URLSession.shared.dataTask(with: req) { _, resp, _ in
            if let http = resp as? HTTPURLResponse, http.statusCode == 200 {
                DispatchQueue.main.async { self.web.load(URLRequest(url: URL(string: BASE)!)) }
            } else if attempt < 60 {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) { self.waitAndLoad(attempt + 1) }
            } else {
                DispatchQueue.main.async {
                    self.web.loadHTMLString("<body style='font-family:-apple-system;background:#13121a;color:#eee;padding:40px'><h2>Не удалось запустить сервер приложения.</h2><p>Закрой и открой приложение ещё раз.</p></body>", baseURL: nil)
                }
            }
        }.resume()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ s: NSApplication) -> Bool { true }
    func applicationWillTerminate(_ n: Notification) { quitting = true; server?.terminate() }

    // MARK: file <input type=file> → native open panel
    func webView(_ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.begin { resp in
            completionHandler(resp == .OK ? panel.urls : nil)
        }
    }

    // MARK: downloads (the "Сохранить копию" backup button)
    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction,
                 preferences: WKWebpagePreferences,
                 decisionHandler: @escaping (WKNavigationActionPolicy, WKWebpagePreferences) -> Void) {
        let u = navigationAction.request.url
        // downloads: the Excel export and the backup button use blob: links with a download attribute
        if navigationAction.shouldPerformDownload || u?.scheme == "blob" || u?.scheme == "data" {
            decisionHandler(.download, preferences)
            return
        }
        // real links to OTHER sites → open in the default browser
        if let u = u, navigationAction.navigationType == .linkActivated,
           let sc = u.scheme, sc == "http" || sc == "https", let h = u.host, h != "127.0.0.1" {
            NSWorkspace.shared.open(u)
            decisionHandler(.cancel, preferences)
            return
        }
        decisionHandler(.allow, preferences)
    }
    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse,
                 decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        if navigationResponse.canShowMIMEType {
            decisionHandler(.allow)
        } else {
            decisionHandler(.download)
        }
    }
    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }
    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }
    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse,
                  suggestedFilename: String, completionHandler: @escaping (URL?) -> Void) {
        let dir = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask).first!
        var url = dir.appendingPathComponent(suggestedFilename.isEmpty ? "vicedolls-backup.json" : suggestedFilename)
        var i = 1
        while FileManager.default.fileExists(atPath: url.path) {
            let ext = (suggestedFilename as NSString).pathExtension
            let base = (suggestedFilename as NSString).deletingPathExtension
            url = dir.appendingPathComponent("\(base) (\(i)).\(ext)")
            i += 1
        }
        completionHandler(url)
    }
    func downloadDidFinish(_ download: WKDownload) {
        NSSound(named: "Glass")?.play()
    }

}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.run()
