import UIKit

/// 与 ZRemote 网页端一致的深色主题
enum Theme {
    static let background = UIColor(red: 0x0b / 255, green: 0x0e / 255, blue: 0x14 / 255, alpha: 1)
    static let panel = UIColor(red: 0x15 / 255, green: 0x1a / 255, blue: 0x23 / 255, alpha: 1)
    static let field = UIColor(red: 0x1c / 255, green: 0x23 / 255, blue: 0x30 / 255, alpha: 1)
    static let border = UIColor(red: 0x2a / 255, green: 0x34 / 255, blue: 0x48 / 255, alpha: 1)
    static let fg = UIColor(red: 0xe6 / 255, green: 0xe9 / 255, blue: 0xf0 / 255, alpha: 1)
    static let dim = UIColor(red: 0x8b / 255, green: 0x93 / 255, blue: 0xa7 / 255, alpha: 1)
    static let acc = UIColor(red: 0x4f / 255, green: 0x8c / 255, blue: 0xff / 255, alpha: 1)
}

/// 连接配置（主机 + 令牌），仅存本机 UserDefaults
enum ConfigStore {
    private static let hostKey = "zr.host"
    private static let tokenKey = "zr.token"

    static var host: String { UserDefaults.standard.string(forKey: hostKey) ?? "" }
    static var token: String { UserDefaults.standard.string(forKey: tokenKey) ?? "" }

    static func save(host: String, token: String) {
        UserDefaults.standard.set(host, forKey: hostKey)
        UserDefaults.standard.set(token, forKey: tokenKey)
    }

    /// 归一化主机输入 → scheme://host[:port] 形式的 origin
    static func normalizeHost(_ raw: String) -> String? {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return nil }
        if !s.hasPrefix("http://") && !s.hasPrefix("https://") {
            s = "https://" + s
        }
        guard let url = URL(string: s), let h = url.host, !h.isEmpty else { return nil }
        var origin = url.scheme! + "://" + h
        if let port = url.port {
            origin += ":\(port)"
        }
        return origin
    }

    /// 从完整链接 https://host:port/?k=TOKEN 中拆出 origin 与令牌
    static func parseLink(_ raw: String) -> (origin: String, token: String)? {
        guard let origin = normalizeHost(raw) else { return nil }
        guard let url = URL(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              let comp = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return nil }
        let token = (comp.queryItems ?? []).first(where: { $0.name == "k" })?.value ?? ""
        return (origin, token)
    }

    static func remoteURL(host: String, token: String) -> URL? {
        URL(string: host + "/?k=" + token)
    }

    /// 无 scheme 的裸主机名（用于 WKWebView 自签证书放行匹配）
    static func hostOnly(_ origin: String) -> String {
        URL(string: origin)?.host ?? origin
    }
}
