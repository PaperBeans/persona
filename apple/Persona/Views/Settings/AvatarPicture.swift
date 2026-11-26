import CoreGraphics
import Foundation
import ImageIO
import UIKit
import UniformTypeIdentifiers

// Pictures for an avatar made from images (#15449, docs/apple/persona.md "Uploading an image"): the main picture and,
// with a hub that lists `look_uploads_states`, a picture per state. A still picture is cropped and scaled here, as
// before; an animated one (GIF, APNG, animated WebP) is sent as it was picked, with the square to keep in its pixels,
// since only the hub turns it into the package's frames.

/// The square of a picture to keep, in its own pixels (`crop` in POST / PATCH /api/v1/looks/uploads).
nonisolated struct LookCrop: Codable, Sendable, Equatable {
    var x: Int
    var y: Int
    var side: Int
}

/// One picture as sent: base64 bytes, and the square to keep when they are the picture as picked (an animated one).
/// A still one is the crop already, square and upright, so it goes without `crop`.
nonisolated struct LookPicture: Sendable, Equatable {
    var image: String
    var crop: LookCrop?
}

/// A picture the owner picked: its bytes as picked, frame 0 upright for the crop, and whether it moves.
nonisolated struct AvatarPicture: Sendable {
    /// The bytes as picked: what is sent when it moves.
    let data: Data
    /// Frame 0, upright, at most `AvatarImage.loadSide`: what the crop square is drawn over and measured on.
    let image: CGImage
    /// A GIF, APNG or WebP with more than one frame.
    let animated: Bool
    /// The picture's own size in pixels (an animated one's canvas): what `crop` is sent in.
    let pixelWidth: Int
    let pixelHeight: Int

    /// The image as one already loaded (tests, and the still path the crop sheet had before).
    init(image: CGImage) {
        data = Data()
        self.image = image
        animated = false
        pixelWidth = image.width
        pixelHeight = image.height
    }

    private init(data: Data, image: CGImage, animated: Bool, width: Int, height: Int) {
        self.data = data
        self.image = image
        self.animated = animated
        pixelWidth = width
        pixelHeight = height
    }

    /// Reads a picked file. A still one is decoded as `AvatarImage.load` does (upright, at most `loadSide`); an
    /// animated one keeps its bytes, and frame 0 is shown as the hub reads it: no orientation turned, its canvas.
    static func load(_ data: Data) throws -> AvatarPicture {
        guard let src = CGImageSourceCreateWithData(data as CFData, nil), Self.animated(src) else {
            let image = try AvatarImage.load(data)
            return AvatarPicture(data: data, image: image, animated: false, width: image.width, height: image.height)
        }
        let (width, height) = canvas(src)
        guard min(width, height) >= AvatarImage.minSide else {
            throw AvatarImage.Problem(errorDescription: String(localized: "This image is too small. Choose one at least \(AvatarImage.minSide) pixels on each side."))
        }
        let opts: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: false,
            kCGImageSourceThumbnailMaxPixelSize: AvatarImage.loadSide,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(src, 0, opts as CFDictionary) else {
            throw AvatarImage.Problem(errorDescription: String(localized: "This file isn't an image Persona can read. Choose a photo, PNG, JPEG, WebP or GIF."))
        }
        return AvatarPicture(data: data, image: image, animated: true, width: width, height: height)
    }

    /// More than one frame in a format that animates: GIF, PNG (APNG) or WebP. A HEIC with several images (a burst,
    /// a depth map) is a still picture.
    static func animated(_ src: CGImageSource) -> Bool {
        guard CGImageSourceGetCount(src) > 1, let type = CGImageSourceGetType(src).flatMap({ UTType($0 as String) }) else { return false }
        return type.conforms(to: .gif) || type.conforms(to: .png) || type.conforms(to: .webP)
    }

    /// The canvas an animated picture's frames are drawn on, as its file says; frame 0's size otherwise.
    private static func canvas(_ src: CGImageSource) -> (Int, Int) {
        let all = CGImageSourceCopyProperties(src, nil) as? [CFString: Any]
        let kinds: [(CFString, CFString, CFString)] = [
            (kCGImagePropertyGIFDictionary, kCGImagePropertyGIFCanvasPixelWidth, kCGImagePropertyGIFCanvasPixelHeight),
            (kCGImagePropertyPNGDictionary, kCGImagePropertyAPNGCanvasPixelWidth, kCGImagePropertyAPNGCanvasPixelHeight),
            (kCGImagePropertyWebPDictionary, kCGImagePropertyWebPCanvasPixelWidth, kCGImagePropertyWebPCanvasPixelHeight),
        ]
        for (dict, w, h) in kinds {
            if let d = all?[dict] as? [CFString: Any], let width = d[w] as? Int, let height = d[h] as? Int, width > 0, height > 0 {
                return (width, height)
            }
        }
        let first = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any]
        return (first?[kCGImagePropertyPixelWidth] as? Int ?? 0, first?[kCGImagePropertyPixelHeight] as? Int ?? 0)
    }

    /// `rect` (in `image`'s pixels) in the picture's own pixels: a square inside it.
    func sourceCrop(_ rect: CGRect) -> LookCrop {
        let k = Double(pixelWidth) / Double(max(1, image.width))
        let side = max(1, min(min(pixelWidth, pixelHeight), Int((rect.width * k).rounded())))
        let x = min(max(0, Int((rect.minX * k).rounded())), pixelWidth - side)
        let y = min(max(0, Int((rect.minY * k).rounded())), pixelHeight - side)
        return LookCrop(x: x, y: y, side: side)
    }

    /// What is sent for the square `rect`: a still picture's crop drawn at most `side` pixels and encoded; an animated
    /// one's bytes as picked, with the square in its pixels. Nil when the crop could not be drawn.
    func payload(rect: CGRect, side: Int) -> LookPicture? {
        if animated { return LookPicture(image: data.base64EncodedString(), crop: sourceCrop(rect)) }
        guard let drawn = AvatarImage.render(image, rect: rect, side: side), let encoded = AvatarImage.encode(drawn) else { return nil }
        return LookPicture(image: encoded.base64EncodedString(), crop: nil)
    }

    /// The square `rect` as it will move, `side` pixels wide, for a preview: an animated picture's frames over its loop
    /// at most as many as the hub keeps (`AvatarMotion`), a still one's one frame. Run off the main actor.
    func frames(rect: CGRect, side: Int) -> PersonaFrames? {
        guard animated, let src = CGImageSourceCreateWithData(data as CFData, nil) else {
            guard let drawn = AvatarImage.render(image, rect: rect, side: side) else { return nil }
            return PersonaFrames(images: [UIImage(cgImage: drawn)], duration: 0)
        }
        let count = CGImageSourceGetCount(src)
        let delays = (0..<count).map { AvatarMotion.delay(src, at: $0) }
        let crop = sourceCrop(rect)
        // Each frame decoded no larger than needed for `side` pixels of the square (a large canvas is never decoded whole).
        let scale = min(1, Double(side) / Double(max(1, crop.side)))
        let longest = max(side, Int((Double(max(pixelWidth, pixelHeight)) * scale).rounded(.up)))
        let opts: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: false,
            kCGImageSourceThumbnailMaxPixelSize: longest,
        ]
        var images: [UIImage] = []
        let shown = AvatarMotion.samples(delays)
        for index in shown.frames {
            guard let frame = CGImageSourceCreateThumbnailAtIndex(src, index, opts as CFDictionary) else { continue }
            let k = Double(frame.width) / Double(max(1, pixelWidth))
            let square = CGRect(x: Double(crop.x) * k, y: Double(crop.y) * k, width: Double(crop.side) * k, height: Double(crop.side) * k)
            if let drawn = AvatarImage.render(frame, rect: square, side: side) { images.append(UIImage(cgImage: drawn)) }
        }
        guard !images.isEmpty else { return nil }
        return PersonaFrames(images: images, duration: images.count > 1 ? shown.duration : 0)
    }
}

/// How an animated picture plays as a state's frames, as the hub keeps it (`docs/apple/persona.md` "Uploading an
/// image"): at most 10 frames a second (shorter frames merged), at most 36 frames over the loop.
nonisolated enum AvatarMotion {
    static let maxFPS = 10.0
    static let maxFrames = 36
    /// A frame with no delay, or one under 10 ms, shows for 100 ms, as browsers play it.
    static let defaultDelay = 0.1

    /// The frames shown, sampled evenly over the loop, and the loop's length.
    static func samples(_ delays: [Double]) -> (frames: [Int], duration: Double) {
        guard delays.count > 1 else { return (delays.isEmpty ? [] : [0], 0) }
        let total = delays.reduce(0, +)
        let n = max(1, min(maxFrames, delays.count, Int((total * maxFPS).rounded(.down))))
        var frames: [Int] = []
        var start = 0.0, index = 0
        for k in 0..<n {
            let t = total * Double(k) / Double(n)
            while index < delays.count - 1, start + delays[index] <= t {
                start += delays[index]
                index += 1
            }
            frames.append(index)
        }
        return (frames, total)
    }

    /// A frame's delay in seconds, from whichever of GIF, APNG or WebP the file is.
    static func delay(_ src: CGImageSource, at index: Int) -> Double {
        let props = CGImageSourceCopyPropertiesAtIndex(src, index, nil) as? [CFString: Any]
        let kinds: [(CFString, CFString, CFString)] = [
            (kCGImagePropertyGIFDictionary, kCGImagePropertyGIFUnclampedDelayTime, kCGImagePropertyGIFDelayTime),
            (kCGImagePropertyPNGDictionary, kCGImagePropertyAPNGUnclampedDelayTime, kCGImagePropertyAPNGDelayTime),
            (kCGImagePropertyWebPDictionary, kCGImagePropertyWebPUnclampedDelayTime, kCGImagePropertyWebPDelayTime),
        ]
        for (dict, unclamped, clamped) in kinds {
            guard let d = props?[dict] as? [CFString: Any] else { continue }
            let v = (d[unclamped] as? Double) ?? (d[clamped] as? Double) ?? 0
            return v > 0.01 ? v : defaultDelay
        }
        return defaultDelay
    }
}

/// The automatic theme color (#15449): its seed estimated from the crop as the hub takes it from frame 0 of the main
/// picture, by the method `spec/fixtures/look-colors/README.md` pins in whole numbers (from `make_persona.py`'s
/// `extract_colors`), so the swatch the sheet shows is the color the hub will write.
nonisolated enum AvatarColor {
    /// The side the crop is read at for the sheet's swatch: the size a still crop is sent at, which the hub reads as
    /// it comes (so for a still picture the swatch is the hub's seed exactly).
    static let side = AvatarImage.outputSide
    /// The seed when too little of the picture is colorful: #787878.
    static let gray: UInt32 = (120 << 16) | (120 << 8) | 120

    /// The seed of `image` at its own size: from the pixels that are opaque enough (A > 200), bright enough
    /// (max ≥ 90) and colorful enough (10·d > 3·max), the 10° hue bin most of them are in (ties: the lower bin), then
    /// each channel's lower-middle value over that bin and its two neighbors. Gray under 50 such pixels.
    static func seed(_ image: CGImage) -> UInt32 {
        guard let pixels = rgba(image) else { return gray }
        var kept: [(r: Int, g: Int, b: Int, bin: Int)] = []
        kept.reserveCapacity(pixels.count / 4)
        for i in stride(from: 0, to: pixels.count, by: 4) {
            let a = Int(pixels[i + 3])
            guard a > 200 else { continue }
            // Straight channels: the context is premultiplied, so only a pixel that is not quite opaque is divided back.
            let r = straight(pixels[i], a), g = straight(pixels[i + 1], a), b = straight(pixels[i + 2], a)
            let mx = max(r, g, b), d = mx - min(r, g, b)
            guard mx >= 90, 10 * d > 3 * mx else { continue }
            let num = mx == r ? (g >= b ? g - b : g - b + 6 * d) : mx == g ? b - r + 2 * d : r - g + 4 * d
            kept.append((r, g, b, (6 * num) / d % 36))
        }
        guard kept.count >= 50 else { return gray }
        var counts = [Int](repeating: 0, count: 36)
        for p in kept { counts[p.bin] += 1 }
        var peak = 0
        for bin in counts.indices where counts[bin] > counts[peak] { peak = bin }
        let near: Set<Int> = [(peak + 35) % 36, peak, (peak + 1) % 36]
        let picked = kept.filter { near.contains($0.bin) }
        let middle = (picked.count - 1) / 2
        let r = picked.map(\.r).sorted()[middle], g = picked.map(\.g).sorted()[middle], b = picked.map(\.b).sorted()[middle]
        return UInt32(r << 16 | g << 8 | b)
    }

    /// "#RRGGBB" as the hub writes it.
    static func hex(_ c: UInt32) -> String { String(format: "#%06X", c & 0xFF_FFFF) }

    private static func straight(_ c: UInt8, _ a: Int) -> Int {
        a == 255 ? Int(c) : min(255, (Int(c) * 255 + a / 2) / a)
    }

    /// The picture's pixels in sRGB, premultiplied RGBA bytes.
    private static func rgba(_ image: CGImage) -> [UInt8]? {
        let w = image.width, h = image.height
        guard w > 0, h > 0, let space = CGColorSpace(name: CGColorSpace.sRGB) else { return nil }
        var bytes = [UInt8](repeating: 0, count: w * h * 4)
        let drawn = bytes.withUnsafeMutableBytes { buf -> Bool in
            guard let ctx = CGContext(data: buf.baseAddress, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                      space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return false }
            ctx.draw(image, in: CGRect(x: 0, y: 0, width: w, height: h))
            return true
        }
        return drawn ? bytes : nil
    }
}
