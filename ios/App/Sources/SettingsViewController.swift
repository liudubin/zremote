import UIKit

/// 设置页：主机地址 + 令牌 表单（支持粘贴完整链接 / 扫码自动填）
final class SettingsViewController: UIViewController, UITextFieldDelegate {
    var onConnect: ((String, String) -> Void)?

    private let scrollView = UIScrollView()
    private let stack = UIStackView()
    private let titleField = UILabel()
    private let subtitle = UILabel()
    private let hostInput = UITextField()
    private let tokenInput = UITextField()
    private let scanButton = UIButton(type: .system)
    private let connectButton = UIButton(type: .system)
    private let toast = UILabel()
    private var kbObservers: [NSObjectProtocol] = []

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Theme.background
        buildUI()
        prefill()
        observeKeyboard()
    }

    private func buildUI() {
        scrollView.keyboardDismissMode = .interactive
        scrollView.alwaysBounceVertical = true
        scrollView.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(scrollView)

        stack.axis = .vertical
        stack.spacing = 14
        stack.translatesAutoresizingMaskIntoConstraints = false
        scrollView.addSubview(stack)

        titleField.text = "ZRemote"
        titleField.font = .systemFont(ofSize: 34, weight: .bold)
        titleField.textColor = Theme.acc

        subtitle.text = "4K/120fps 远程桌面\n地址与令牌见电脑上 zremote.exe 控制台（或其二维码）"
        subtitle.font = .systemFont(ofSize: 13)
        subtitle.textColor = Theme.dim
        subtitle.numberOfLines = 0

        for (field, placeholder) in [(hostInput, "主机地址  如 192.168.88.209:48443"),
                                     (tokenInput, "访问令牌  8 位")] {
            field.placeholder = placeholder
            field.textColor = Theme.fg
            field.backgroundColor = Theme.field
            field.layer.cornerRadius = 10
            field.layer.borderWidth = 1
            field.layer.borderColor = Theme.border.cgColor
            field.autocorrectionType = .no
            field.autocapitalizationType = .none
            field.keyboardType = .URL
            field.delegate = self
            field.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 12, height: 1))
            field.leftViewMode = .always
            field.heightAnchor.constraint(equalToConstant: 46).isActive = true
        }
        tokenInput.isSecureTextEntry = false
        tokenInput.keyboardType = .asciiCapable

        scanButton.setTitle("📷 扫描控制台二维码", for: .normal)
        scanButton.setTitleColor(Theme.fg, for: .normal)
        scanButton.backgroundColor = Theme.panel
        scanButton.layer.cornerRadius = 10
        scanButton.layer.borderWidth = 1
        scanButton.layer.borderColor = Theme.border.cgColor
        scanButton.heightAnchor.constraint(equalToConstant: 44).isActive = true
        scanButton.addTarget(self, action: #selector(onScan), for: .touchUpInside)

        connectButton.setTitle("连  接", for: .normal)
        connectButton.setTitleColor(.white, for: .normal)
        connectButton.titleLabel?.font = .systemFont(ofSize: 17, weight: .semibold)
        connectButton.backgroundColor = Theme.acc
        connectButton.layer.cornerRadius = 12
        connectButton.heightAnchor.constraint(equalToConstant: 52).isActive = true
        connectButton.addTarget(self, action: #selector(onConnectTapped), for: .touchUpInside)

        toast.textColor = UIColor(red: 0xe7 / 255, green: 0x4c / 255, blue: 0x3c / 255, alpha: 1)
        toast.font = .systemFont(ofSize: 13)
        toast.numberOfLines = 0
        toast.alpha = 0

        let hint = UILabel()
        hint.text = "· 令牌即访问凭证，请勿外传\n· 手机推荐填 HTTPS 端口（默认 48443）\n· 越狱/巨魔设备安装未签名 IPA 即可使用"
        hint.textColor = Theme.dim
        hint.font = .systemFont(ofSize: 12)
        hint.numberOfLines = 0

        for v in [titleField, subtitle, hostInput, tokenInput, scanButton, connectButton, toast, hint] {
            stack.addArrangedSubview(v)
        }

        NSLayoutConstraint.activate([
            scrollView.topAnchor.constraint(equalTo: view.topAnchor),
            scrollView.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            scrollView.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            scrollView.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            stack.topAnchor.constraint(equalTo: scrollView.contentLayoutGuide.topAnchor, constant: 60),
            stack.leadingAnchor.constraint(equalTo: scrollView.contentLayoutGuide.leadingAnchor, constant: 24),
            stack.trailingAnchor.constraint(equalTo: scrollView.contentLayoutGuide.trailingAnchor, constant: -24),
            stack.bottomAnchor.constraint(lessThanOrEqualTo: scrollView.contentLayoutGuide.bottomAnchor, constant: -30),
            stack.widthAnchor.constraint(equalTo: scrollView.frameLayoutGuide.widthAnchor, constant: -48),
        ])
    }

    private func prefill() {
        hostInput.text = ConfigStore.host
        tokenInput.text = ConfigStore.token
        // 剪贴板里正好是连接链接则直接预填
        if let clip = UIPasteboard.general.string?.trimmingCharacters(in: .whitespacesAndNewlines),
           clip.contains("://"), let parsed = ConfigStore.parseLink(clip) {
            hostInput.text = parsed.origin
            if !parsed.token.isEmpty { tokenInput.text = parsed.token }
        }
    }

    private func observeKeyboard() {
        let show = NotificationCenter.default.addObserver(forName: UIResponder.keyboardWillShowNotification, object: nil, queue: .main) { [weak self] n in
            guard let self, let end = (n.userInfo?[UIResponder.keyboardFrameEndUserInfoKey] as? NSValue)?.cgRectValue else { return }
            self.scrollView.contentInset.bottom = end.height + 20
            self.scrollView.verticalScrollIndicatorInsets.bottom = end.height
        }
        let hide = NotificationCenter.default.addObserver(forName: UIResponder.keyboardWillHideNotification, object: nil, queue: .main) { [weak self] _ in
            self?.scrollView.contentInset.bottom = 0
        }
        kbObservers = [show, hide]
    }

    @objc private func onScan() {
        view.endEditing(true)
        let qr = QRScannerViewController()
        qr.onFound = { [weak self] value in
            guard let self else { return }
            if let parsed = ConfigStore.parseLink(value) {
                self.hostInput.text = parsed.origin
                if !parsed.token.isEmpty { self.tokenInput.text = parsed.token }
                self.showToast("已识别，点「连接」开始")
            } else {
                self.showToast("二维码内容不是 ZRemote 链接")
            }
        }
        present(qr, animated: true)
    }

    @objc private func onConnectTapped() {
        view.endEditing(true)
        let rawHost = hostInput.text ?? ""
        let token = (tokenInput.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        guard let origin = ConfigStore.normalizeHost(rawHost) else {
            showToast("主机地址无效"); return
        }
        guard !token.isEmpty else {
            showToast("请填写访问令牌"); return
        }
        onConnect?(origin, token)
    }

    private func showToast(_ msg: String) {
        toast.text = msg
        UIView.animate(withDuration: 0.2) { self.toast.alpha = 1 }
        NSObject.cancelPreviousPerformRequests(withTarget: self, selector: #selector(hideToast), object: nil)
        perform(#selector(hideToast), with: nil, afterDelay: 2.5)
    }
    @objc private func hideToast() {
        UIView.animate(withDuration: 0.4) { self.toast.alpha = 0 }
    }

    func textFieldShouldReturn(_ textField: UITextField) -> Bool {
        if textField == hostInput { tokenInput.becomeFirstResponder() } else { onConnectTapped() }
        return true
    }
}
