import SwiftUI

/// Where New from Photo's image comes from (#2405): Photos (`PhotosPicker`) or an image file (the Mac's open panel, the
/// Files app), opened as the crop sheet (`AvatarCropView`), which also edits an uploaded avatar (#15449). Hung on the
/// avatar picker (`PersonaPicker`), which says which one to open. The picking itself is `AvatarPicturePicking`, as the
/// sheet's own pictures are picked.
struct AvatarSources: ViewModifier {
    var uploads: AvatarUploadsModel
    @Binding var choosingPhoto: Bool
    @Binding var choosingFile: Bool
    @Binding var cropping: AvatarUploadModel?

    func body(content: Content) -> some View {
        content
            .modifier(AvatarPicturePicking(choosingPhoto: $choosingPhoto, choosingFile: $choosingFile, picked: { data in
                cropping = await uploads.picked(data)
            }, failed: uploads.failedToRead))
            .sheet(item: $cropping) { AvatarCropView(model: $0) }
    }
}
