import AVFoundation
import UIKit

/// 扫码页：AVFoundation 元数据识别，只认二维码（模板来自 ZCodeRemote，文案适配 ZRemote）
final class QRScannerViewController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onFound: ((String) -> Void)?

    private let session = AVCaptureSession()
    private var previewLayer: AVCaptureVideoPreviewLayer?
    private let hintLabel = UILabel()
    private let closeButton = UIButton(type: .system)
    private let sessionQueue = DispatchQueue(label: "zremote.qr.session")
    private var isClosing = false

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black

        closeButton.setTitle("关闭", for: .normal)
        closeButton.setTitleColor(.white, for: .normal)
        closeButton.translatesAutoresizingMaskIntoConstraints = false
        closeButton.addTarget(self, action: #selector(onClose), for: .touchUpInside)
        view.addSubview(closeButton)

        hintLabel.text = "对准电脑上 zremote.exe 控制台显示的二维码"
        hintLabel.textColor = .white
        hintLabel.font = .systemFont(ofSize: 13)
        hintLabel.textAlignment = .center
        hintLabel.numberOfLines = 0
        hintLabel.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(hintLabel)

        NSLayoutConstraint.activate([
            closeButton.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 12),
            closeButton.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -16),
            hintLabel.bottomAnchor.constraint(equalTo: view.safeAreaLayoutGuide.bottomAnchor, constant: -40),
            hintLabel.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: 30),
            hintLabel.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -30),
        ])

        sessionQueue.async { [weak self] in
            self?.setupSession()
            self?.session.startRunning()
        }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        previewLayer?.frame = view.bounds
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        sessionQueue.async { [weak self] in self?.session.stopRunning() }
    }

    @objc private func onClose() {
        dismiss(animated: true)
    }

    private func setupSession() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device) else {
            DispatchQueue.main.async { [weak self] in
                self?.hintLabel.text = "无法访问相机"
            }
            return
        }
        session.beginConfiguration()
        session.addInput(input)
        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else {
            session.commitConfiguration()
            return
        }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]
        session.commitConfiguration()

        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            let layer = AVCaptureVideoPreviewLayer(session: self.session)
            layer.videoGravity = .resizeAspectFill
            layer.frame = self.view.bounds
            self.view.layer.insertSublayer(layer, at: 0)
            self.previewLayer = layer
            self.view.bringSubviewToFront(self.closeButton)
            self.view.bringSubviewToFront(self.hintLabel)
        }
    }

    func metadataOutput(_ output: AVCaptureMetadataOutput,
                        didOutput metadataObjects: [AVMetadataObject],
                        from connection: AVCaptureConnection) {
        guard let obj = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
              obj.type == .qr, let value = obj.stringValue else { return }
        guard !isClosing else { return }
        isClosing = true
        session.stopRunning()
        // 先关扫码页再回调，避免转场被打断（ZCodeRemote 真机踩坑）
        let found = onFound
        dismiss(animated: true) { found?(value) }
    }
}
