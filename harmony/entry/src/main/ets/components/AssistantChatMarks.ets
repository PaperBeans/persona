import { LengthMetrics } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { assistantChatTitleParts } from '../models/AssistantRecentChats';
import { nativeEnvironment } from '../platform/NativeEnvironment';

export function assistantSourceSymbol(key: string): Resource {
  switch (key) {
    case 'all': return $r('sys.symbol.list_bullet_square');
    case 'mine': return $r('sys.symbol.person');
    case 'inbox': return $r('sys.symbol.inbox');
    case 'task': return $r('sys.symbol.alarm');
    case 'lark_agent': return $r('sys.symbol.text_bubble_left_2');
    case 'spawned': return Symbols.persona;
    case 'extension': return Symbols.apps;
    default: return Symbols.gear;
  }
}

@ComponentV2
export struct AssistantChatTitle {
  @Require @Param title: string;
  @Param source: string = '';
  @Param gap: number = 4;
  @Param identity: string = '';
  build() {
    Flex({direction:FlexDirection.Row,alignItems:ItemAlign.Center,space:{main:LengthMetrics.vp(this.gap)}}) {
      Text() {
        Span(assistantChatTitleParts(this.title)[0]).fontColor(Colors.secondary)
        Span(assistantChatTitleParts(this.title)[1]).fontColor(Colors.ink)
      }.fontSize(Tokens.body).maxFontScale(Tokens.interfaceScaleFor(Tokens.body))
        .maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1).constraintSize({minWidth:0}).id(this.identity+'.title')
      if (this.source === 'lark_agent') Image($r('app.media.brand_lark'))
        .width(PhoneStyle.sidebarMetric(14,nativeEnvironment.fontScale)).aspectRatio(1).objectFit(ImageFit.Contain)
        .flexShrink(0).accessibilityLevel('no').id(this.identity+'.source')
      else if (this.source) SymbolGlyph(assistantSourceSymbol(this.source))
        .fontSize(Tokens.interfaceFontVp(13,nativeEnvironment.fontScale)).fontColor([Colors.secondary])
        .flexShrink(0).accessibilityLevel('no').id(this.identity+'.source')
    }.width('100%')
  }
}

@ComponentV2
export struct AssistantChatStateMark {
  @Require @Param state: string;
  @Param identity: string = '';
  build() {
    if (this.state === 'working') LoadingProgress()
      .width(PhoneStyle.sidebarMetric(13,nativeEnvironment.fontScale)).aspectRatio(1)
      .color(Colors.workingMark).accessibilityLevel('no').id(this.identity+'.working')
    else if (this.state === 'needs_you' || this.state === 'unread') Circle()
      .width(PhoneStyle.sidebarMetric(8,nativeEnvironment.fontScale)).aspectRatio(1)
      .fill(this.state === 'needs_you' ? Colors.needsMark : Colors.unreadMark).accessibilityLevel('no').id(this.identity+'.'+this.state)
  }
}
