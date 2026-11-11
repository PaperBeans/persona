import CryptoKit
import Foundation
import Security

/// The system browser performs OPAQUE; only a one-time, PKCE-bound code returns to the app.
nonisolated struct PasswordLoginRequest: Sendable {
    let server: URL
    let page: URL
    let state: String
    let verifier: String

    init(address: String, name: String, kind: String) throws {
        let text = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var base = URLComponents(string: text.contains("://") ? text : "https://" + text),
              base.scheme == "https", base.host?.isEmpty == false, base.user == nil, base.password == nil,
              base.query == nil, base.fragment == nil, base.path.isEmpty || base.path == "/"
        else { throw APIError(status: 0, message: interfaceText("Enter a valid HTTPS hub address")) }
        base.path = "/"
        guard let server = base.url else { throw APIError(status: 0, message: interfaceText("Invalid hub address")) }
        self.server = server
        state = try Self.random()
        verifier = try Self.random()
        let challenge = Self.encode(Data(SHA256.hash(data: Data(verifier.utf8))))
        var fragment = URLComponents()
        fragment.queryItems = [.init(name: "state", value: state), .init(name: "challenge", value: challenge),
                               .init(name: "kind", value: kind), .init(name: "name", value: String(name.prefix(70)))]
        base.path = "/login/device"
        base.percentEncodedFragment = fragment.percentEncodedQuery
        guard let page = base.url else { throw APIError(status: 0, message: interfaceText("Could not create the sign-in request")) }
        self.page = page
    }

    struct Credential: Decodable, Sendable {
        let token: String
        let id: String
        let hubID: String
        enum CodingKeys: String, CodingKey { case token, id; case hubID = "hub_id" }
    }

    func exchange(_ callback: URL) async throws -> Credential {
        guard let parts = URLComponents(url: callback, resolvingAgainstBaseURL: false),
              parts.scheme == "persona", parts.host == "password-login",
              parts.queryItems?.filter({ $0.name == "state" }).count == 1,
              parts.queryItems?.filter({ $0.name == "code" }).count == 1,
              parts.queryItems?.first(where: { $0.name == "state" })?.value == state,
              let code = parts.queryItems?.first(where: { $0.name == "code" })?.value,
              code.range(of: "^[A-Za-z0-9_-]{43}$", options: .regularExpression) != nil
        else { throw APIError(status: 0, message: interfaceText("Invalid sign-in callback. Try again")) }
        var request = URLRequest(url: server.appending(path: "api/v1/auth/password/exchange"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONEncoder().encode(["code": code, "verifier": verifier])
        let session = URLSession(configuration: .ephemeral, delegate: PasswordLoginRedirectGuard(), delegateQueue: nil)
        defer { session.invalidateAndCancel() }
        let (data, response) = try await session.data(for: request)
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw APIError(status: 0, message: interfaceText("Sign-in authorization has expired. Sign in again"))
        }
        let credential = try JSONDecoder().decode(Credential.self, from: data)
        guard credential.token.hasPrefix("jd_"), credential.id.hasPrefix("dev_") else {
            throw APIError(status: 0, message: interfaceText("Invalid device credential"))
        }
        return credential
    }

    private static func random() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        guard SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) == errSecSuccess else {
            throw APIError(status: 0, message: interfaceText("Could not create a secure sign-in request"))
        }
        return encode(Data(bytes))
    }

    private static func encode(_ data: Data) -> String {
        data.base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
}

private nonisolated final class PasswordLoginRedirectGuard: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}
