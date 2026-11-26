import { IconActions } from '../design/IconActions';
import { ChatAttachment } from '../models/ChatAttachments';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';

@ComponentV2
export struct AttachmentStrip {
  @Require @Param attachments: ChatAttachment[];
  @Param errorKey: string = '';
  @Param actionsEnabled: boolean = true;
  @Event onRemove: (id: string) => void = () => {};
  @Event onEdit: (id: string) => void = () => {};
  @Event onPreview: (item: ChatAttachment) => void = () => {};
  @Builder imageMenu(item: ChatAttachment) {
    Menu() { MenuItem({ content: tr('edit') }).enabled(this.actionsEnabled).onClick(() => this.onEdit(item.id)) }
  }
  @Builder item(item: ChatAttachment) {
    Row({ space: Tokens.gap }) {
      if (item.kind === 'image') Image(item.previewUri).width(42).height(42).borderRadius(6).objectFit(ImageFit.Cover)
        .accessibilityText(item.name).onClick(() => this.onPreview(item)).bindContextMenu(this.imageMenu(item), ResponseType.LongPress)
      else SymbolGlyph(Symbols.document).fontSize(Tokens.symbolFont).fontColor([Colors.link])
      Column({ space: Tokens.fine }) {
        Text(item.name).fontSize(Tokens.meta).fontColor(Colors.ink).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis }).width('100%').onClick(() => this.onPreview(item))
        if (item.status === 'unknown' || item.status === 'uploading')
          Text(tr(item.status === 'uploading' ? 'chat_attachment_uploading' : 'chat_attachment_upload_unknown')).fontSize(Tokens.meta).fontColor(Colors.secondary)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      Button({ type: ButtonType.Circle }) { SymbolGlyph(IconActions.symbol('remove')).fontSize(13).fontColor([Colors.secondary]) }.width(26).height(26).padding(0).backgroundColor(Colors.surface).responseRegion({x:-7,y:-7,width:40,height:40}).accessibilityText(tr('remove') + ' ' + item.name).bindTips(tr('remove') + ' ' + item.name).enabled(this.actionsEnabled).onClick(() => this.onRemove(item.id))
    }.width(183).constraintSize({ minHeight: Tokens.touch }).padding({left:6,right:8,top:3,bottom:3})
      .borderRadius(10).backgroundColor(Colors.control).alignItems(VerticalAlign.Center).id('chat.attachment.' + item.id)
  }
  build() {
    if (this.attachments.length > 0 || this.errorKey) Column({ space: Tokens.gap }) {
      if (this.errorKey && !this.attachments.some((item: ChatAttachment) => item.status === 'unknown' || item.status === 'uploading'))
        Text(tr(this.errorKey)).fontSize(Tokens.meta).fontColor($r('app.color.persona_danger')).width('100%')
      if (this.attachments.length) Scroll() {
        Row({space:6}) {
          ForEach(this.attachments, (item: ChatAttachment) => this.item(item),
            (item: ChatAttachment) => JSON.stringify([item.id,item.status,item.name,item.previewUri]))
        }
      }.width('100%').align(Alignment.Start).scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off).id('chat.attachments')

    }.width('100%')
  }
}
