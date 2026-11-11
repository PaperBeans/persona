import Foundation
import ImageIO

/// Public extension artwork never shares the hub's authenticated transport.
/// Decoded thumbnails are bounded and requests for the same URL/pixel size are coalesced.
@MainActor final class ExtensionImageCache {
    static let shared = ExtensionImageCache()
    nonisolated static let byteLimit = 4 << 20
    nonisolated static let pixelLimit = 512

    nonisolated struct Thumbnail: @unchecked Sendable {
        let image: CGImage
        var cost: Int { image.bytesPerRow * image.height }
    }
    struct Deps {
        var fetch: @Sendable (URL) async throws -> (Data, URLResponse)
        static var live: Deps { .init(fetch: { try await session.data(from: $0) }) }
    }
    private let deps: Deps
    private lazy var images: NSCache<NSString, CGImage> = {
        let cache = NSCache<NSString, CGImage>()
        cache.totalCostLimit = 8 << 20
        cache.countLimit = 128
        return cache
    }()
    private var inflight: [String: Task<Thumbnail?, Never>] = [:]

    init(deps: Deps = .live) { self.deps = deps }

    nonisolated static func url(_ value: String?) -> URL? {
        guard let value, let url = URL(string: value), url.scheme?.lowercased() == "https",
              url.host?.isEmpty == false, url.user == nil, url.password == nil else { return nil }
        return url
    }

    func image(_ url: URL, maxPixel: Int) async -> CGImage? {
        guard Self.url(url.absoluteString) != nil else { return nil }
        let pixels = min(Self.pixelLimit, max(1, maxPixel))
        let key = url.absoluteString + "#" + String(pixels)
        if let image = images.object(forKey: key as NSString) { return image }
        if let task = inflight[key] { return await task.value?.image }
        let fetch = deps.fetch
        let task = Task<Thumbnail?, Never> {
            guard let (data, response) = try? await fetch(url),
                  let response = response as? HTTPURLResponse,
                  (200..<300).contains(response.statusCode),
                  Self.url(response.url?.absoluteString) != nil,
                  data.count <= Self.byteLimit else { return nil }
            return await Task.detached(priority: .utility) { Self.decode(data, maxPixel: pixels) }.value
        }
        inflight[key] = task
        let thumbnail = await task.value
        inflight[key] = nil
        if let thumbnail { images.setObject(thumbnail.image, forKey: key as NSString, cost: thumbnail.cost) }
        return thumbnail?.image
    }

    /// Called only by a detached task; ImageIO eagerly decodes to the drawn pixel size (P8).
    nonisolated static func decode(_ data: Data, maxPixel: Int) -> Thumbnail? {
        guard data.count <= byteLimit,
              let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
            kCGImageSourceThumbnailMaxPixelSize: min(pixelLimit, max(1, maxPixel)),
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return Thumbnail(image: image)
    }

    private nonisolated static let redirectPolicy = RedirectPolicy()
    nonisolated static let session: URLSession = {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.httpShouldSetCookies = false
        configuration.httpCookieStorage = nil
        configuration.urlCredentialStorage = nil
        configuration.httpAdditionalHeaders = [:]
        configuration.timeoutIntervalForRequest = 15
        configuration.timeoutIntervalForResource = 30
        configuration.urlCache = URLCache(memoryCapacity: 8 << 20, diskCapacity: 0)
        return URLSession(configuration: configuration, delegate: redirectPolicy, delegateQueue: nil)
    }()

    private nonisolated final class RedirectPolicy: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
        func urlSession(_ session: URLSession, task: URLSessionTask,
                        willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest,
                        completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
            guard ExtensionImageCache.url(request.url?.absoluteString) != nil else { completionHandler(nil); return }
            var clean = URLRequest(url: request.url!)
            clean.httpShouldHandleCookies = false
            completionHandler(clean)
        }
    }
}
