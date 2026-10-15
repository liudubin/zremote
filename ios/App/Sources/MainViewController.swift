import UIKit

/// 容器：有配置直达远程页，否则进设置页
final class MainViewController: UIViewController {
    private var current: UIViewController?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Theme.background
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        if current == nil {
            ConfigStore.host.isEmpty ? showSettings() : showRemote()
        }
    }

    private func swap(_ vc: UIViewController) {
        current?.willMove(toParent: nil)
        current?.view.removeFromSuperview()
        current?.removeFromParent()
        addChild(vc)
        view.addSubview(vc.view)
        vc.view.frame = view.bounds
        vc.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        vc.didMove(toParent: self)
        current = vc
    }

    func showSettings() {
        let vc = SettingsViewController()
        vc.onConnect = { [weak self] host, token in
            ConfigStore.save(host: host, token: token)
            self?.showRemote()
        }
        swap(vc)
    }

    func showRemote() {
        let vc = RemoteViewController(host: ConfigStore.host, token: ConfigStore.token)
        vc.onExit = { [weak self] in
            ConfigStore.save(host: "", token: "")
            self?.showSettings()
        }
        swap(vc)
    }
}
