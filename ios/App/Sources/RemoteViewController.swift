import UIKit
import WebKit

/// 远程桌面页：全屏 WKWebView。
/// 自签证书仅对「已保存主机」放行（内网自用主机），其它站点走系统默认校验。
final class RemoteViewController: UIViewController, WKNavigationDelegate {
    var onExit: (() -> Void)?

    private let hostOrigin: String
    private let token: String
    private let hostOnly: String
    private let web = WKWebView()
    private let gearButton = UIButton(type: .system)
    private let errorBar = UIButton(type: .system)
    private var loadFailed = false

    init(host: String, token: String) {
        self.hostOrigin = host
        self.token = token
        self.hostOnly = ConfigStore.hostOnly(host)
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Theme.background

        web.navigationDelegate = self
        web.allowsBackForwardNavigationGestures = false
        web.isOpaque = false
        web.backgroundColor = Theme.background
        web.scrollView.backgroundColor = Theme.background
        web.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(web)

        gearButton.setTitle("⚙", for: .normal)
        gearButton.setTitleColor(Theme.fg, for: .normal)
        gearButton.titleLabel?.font = .systemFont(ofSize: 18)
        gearButton.backgroundColor = UIColor(red: 0, green: 0, blue: 0, alpha: 0.45)
        gearButton.layer.cornerRadius = 19
        gearButton.layer.borderWidth = 1
        gearButton.layer.borderColor = UIColor(white: 1, alpha: 0.15).cgColor
        gearButton.translatesAutoresizingMaskIntoConstraints = false
        gearButton.addTarget(self, action: #selector(onGear), for: .touchUpInside)
        view.addSubview(gearButton)

        errorBar.setTitle("  主机不可达 · 点按重试  ", for: .normal)
        errorBar.setTitleColor(.white, for: .normal)
        errorBar.backgroundColor = UIColor(red: 0xe7 / 255, green: 0x4c / 255, blue: 0x3c / 255, alpha: 0.92)
        errorBar.layer.cornerRadius = 8
        errorBar.titleLabel?.font = .systemFont(ofSize: 13)
        errorBar.isHidden = true
        errorBar.translatesAutoresizingMaskIntoConstraints = false
        errorBar.addTarget(self, action: #selector(retryLoad), for: .touchUpInside)
        view.addSubview(errorBar)

        NSLayoutConstraint.activate([
            web.topAnchor.constraint(equalTo: view.topAnchor),
            web.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            web.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            web.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            gearButton.widthAnchor.constraint(equalToConstant: 38),
            gearButton.heightAnchor.constraint(equalToConstant: 38),
            gearButton.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor, constant: -14),
            gearButton.bottomAnchor.constraint(equalTo: view.safeAreaLayoutGuide.bottomAnchor, constant: -14),
            errorBar.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 8),
            errorBar.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            errorBar.heightAnchor.constraint(equalToConstant: 32),
        ])

        load()
    }

    private func load() {
        loadFailed = false
        errorBar.isHidden = true
        guard let url = ConfigStore.remoteURL(host: hostOrigin, token: token) else { return }
        web.load(URLRequest(url: url))
    }

    @objc private func retryLoad() { load() }

    @objc private func onGear() {
        let alert = UIAlertController(title: "ZRemote", message: "断开并返回设置？", preferredStyle: .actionSheet)
        alert.addAction(UIAlertAction(title: "断开并重设地址", style: .destructive) { [weak self] _ in
            self?.onExit?()
        })
        alert.addAction(UIAlertAction(title: "刷新页面", style: .default) { [weak self] _ in
            self?.load()
        })
        alert.addAction(UIAlertAction(title: "取消", style: .cancel))
        alert.popoverPresentationController?.sourceView = gearButton
        alert.popoverPresentationController?.sourceRect = gearButton.bounds
        present(alert, animated: true)
    }

    // MARK: - WKNavigationDelegate

    /// 自签证书放行：仅当目标主机 == 已保存主机
    func webView(_ webView: WKWebView,
                 didReceive challenge: URLAuthenticationChallenge,
                 completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              challenge.protectionSpace.host == hostOnly,
              let trust = challenge.protectionSpace.serverTrust else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(trust: trust))
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        let ns = error as NSError
        guard ns.code != NSURLErrorCancelled else { return }
        loadFailed = true
        errorBar.isHidden = false
    }

    func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) {
        loadFailed = false
        errorBar.isHidden = true
    }

    /// 新窗口链接在当前页打开（避免_blank无响应）
    func webView(_ webView: WKWebView,
                 decidePolicyFor navigationAction: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        if navigationAction.targetFrame == nil {
            webView.load(navigationAction.request)
        }
        decisionHandler(.allow)
    }
}
