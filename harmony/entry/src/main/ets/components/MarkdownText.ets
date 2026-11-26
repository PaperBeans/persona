import { LengthMetrics, Theme } from '@kit.ArkUI';
import { pasteboard } from '@kit.BasicServicesKit';
import { tr } from '../design/Strings';
import { IconActions } from '../design/IconActions';
import { MarkdownBlock, MarkdownInline, MarkdownProseRun, MarkdownTableRow, agentReplyBodySize, hasHanProse, markdownProseRuns, parseMarkdown, recapGlyphSize } from '../core/Markdown';
import { Tokens } from '../design/Tokens';
import { Colors } from '../design/Colors';
import { nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct MarkdownText {
  @Require @Param text: string;
  @Param fitContent: boolean = false;
  @Param nativeTextSelection: boolean = true;
  @Param fixedType: boolean = false;
  @Param agentPrompt: boolean = false;
  @Param chatReply: boolean = false;
  @Param agentReply: boolean = false;
  @Param agentFontScale: number = 1;
  @Param recapReading: boolean = false;
  @Param profileReading: boolean = false;
  @Param dockPlan: boolean = false;
  @Param paragraphLineSpacingEm: number = 0.26;
  @Event onLink: (url: string) => void = (_url: string) => {};
  @Event onSelectionChange: (selected: boolean) => void = (_selected: boolean) => {};
  @Local blocks: MarkdownBlock[] = [];
  @Local foreground: ResourceColor = $r('sys.color.font_primary');
  @Local secondary: ResourceColor = $r('sys.color.font_secondary');
  @Local emphasize: ResourceColor = $r('sys.color.font_emphasize');
  @Local codeFill: ResourceColor = $r('sys.color.comp_background_tertiary');
  onWillApplyTheme(theme: Theme): void {
    this.foreground = theme.colors.fontPrimary; this.secondary = theme.colors.fontSecondary;
    this.emphasize = theme.colors.fontEmphasize; this.codeFill = theme.colors.compBackgroundTertiary;
  }

  aboutToAppear(): void { this.blocks = parseMarkdown(this.text); }
  @Monitor('text') textChanged(): void { this.blocks = parseMarkdown(this.text); }

  @Computed get scaleAgentProse(): boolean { return Math.abs(this.agentFontScale - 1) > 0.001; }
  @Computed get recapProse(): boolean { return this.recapReading && this.chatReply && !this.dockPlan; }
  @Computed get agentBodyVp(): number { return agentReplyBodySize(this.agentFontScale); }
  @Computed get agentHanVp(): number { return agentReplyBodySize(this.agentFontScale, true); }
  // The recording profile uses MarkdownUI's display face at larger reading sizes. Its standard face already matches.
  @Computed get profileDisplayProse():boolean { return this.profileReading && this.agentBodyVp >= 20; }
  @Computed get promptBodyVp(): number { return Math.min(53, Math.max(14, Tokens.body * this.agentFontScale)); }

  private inlineSpacing(size: number, lineSpacingEm: number, readingBody:boolean = false): number {
    if (this.recapProse || readingBody && this.profileDisplayProse) return 0;
    if (this.recapReading) return this.readingPoints(size) * (this.dockPlan ? 0.37 : lineSpacingEm);
    return 0;
  }

  private inlineLineHeight(size: number, readingBody: boolean, spacingEm: number, nodes: MarkdownInline[], promptBody: boolean = false): string | undefined {
    // Theme.Level.body uses a 1.45-em advance. On ArkUI a fixed advance keeps its first and last lines at their intrinsic height.
    if (promptBody) return String(this.promptBodyVp * 1.45) + 'vp';
    // The native recap advances 23.5 pt at standard type and 66.5 pt at the largest reading size.
    if (readingBody && this.recapProse) return String(this.readingPoints(size) * (1.125 + spacingEm)) + 'vp';
    if (readingBody && this.agentBodyVp >= 17 && this.agentBodyVp <= 20) {
      // The system text face precedes the CJK Display crossover. Its two- and four-line replies
      // measure 44.7/93.3pt at standard type and 50.7/106pt at the next native category.
      const points = this.agentBodyVp;
      const advance = points <= 19 ? 73/3 + (points-17) * 11/6 : 28 + points-19;
      return String(advance) + 'vp';
    }
    if (readingBody && this.scaleAgentProse) {
      // SwiftUI adds ReplyMetrics.lineSpacing to the system body line advance.
      // The display CJK face advances 69.3pt per line of 48pt mixed prose in the native chat.
      const points = this.agentBodyVp;
      if (points >= 20 && hasHanProse(nodes)) {
        // The measured iPhone display sizes advance by 29, 38 and 45pt at 20, 26 and 31pt.
        // its largest display face advances by 69⅓pt. Retain the requested leading between those native sizes.
        const advance = points <= 26 ? 29 + (points-20)*1.5 :
          points <= 31 ? 38 + (points-26)*1.4 : 45 + (points-31)*(73/3)/17;
        return String(advance + points*(spacingEm-0.26)) + 'vp';
      }
      return String(points * (1.19 + spacingEm)) + 'vp';
    }
    return this.chatReply && !this.recapReading ? String(Math.ceil(size * 1.45)) + 'fp' : undefined;
  }

  private readingPoints(size: number): number {
    const scale = nativeEnvironment.fontScale;
    // MarkdownUI uses a metric relative to body: 17 points at standard type and 48 at AX5.
    const readingScale = scale <= 1 ? scale : 1 + (scale - 1) * (48 / 17 - 1) / (3.2 - 1);
    return size * readingScale;
  }
  private readingFont(size: number): number | string {
    return this.recapReading ? String(this.readingPoints(size)) + 'vp' : size;
  }
  private proseFont(han: boolean): string | undefined {
    if (this.recapProse) return String(recapGlyphSize(this.readingPoints(Tokens.body), han)) + 'vp';
    if (this.profileDisplayProse && !han) return String(this.agentBodyVp) + 'vp';
    return han ? String(this.agentHanVp) + 'vp' : undefined;
  }
  private recapTopInset(): number {
    // The smaller display face loses 5 vp of intrinsic first-line ascent at the largest reading size.
    return Math.max(0, this.readingPoints(Tokens.body) - Tokens.body) * 5 / (48 - Tokens.body);
  }
  private headingSize(level: number): number {
    if (this.chatReply) {
      if (level === 1) return Math.round(Tokens.body * 1.3);
      return level <= 3 ? Tokens.body : 15;
    }
    if (level <= 1) return Tokens.markdownHeading1;
    if (level === 2) return Tokens.markdownHeading2;
    if (level === 3) return Tokens.markdownHeading3;
    if (level === 4) return Tokens.markdownHeading4;
    if (level === 5) return Tokens.markdownHeading5;
    return Tokens.body;
  }

  private async copyCode(text: string): Promise<void> {
    try {
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, text));
      this.getUIContext().getPromptAction().showToast({ message: tr('done') });
    } catch (_) {
      this.getUIContext().getPromptAction().showToast({ message: tr('failed') });
    }
  }

  private tableTextAlign(table: MarkdownBlock, column: number): TextAlign {
    const alignment = table.alignments[column] || 'start';
    if (alignment === 'center') return TextAlign.Center;
    if (alignment === 'end') return TextAlign.End;
    return TextAlign.Start;
  }

  @Builder inlineText(nodes: MarkdownInline[], lineSpacingEm: number, size: number = Tokens.body, weight: FontWeight = FontWeight.Normal,
    alignment: TextAlign = TextAlign.Start, breakAll: boolean = false, quiet: boolean = false, readingBody: boolean = false,
    promptBody: boolean = false) {
    Text() {
      ForEach(nodes, (node: MarkdownInline) => this.inlineNode(node, weight, false, '', quiet, readingBody),
        (node: MarkdownInline, index: number) => String(index))
    }
      .fontSize(promptBody ? String(this.promptBodyVp) + 'vp' :
        readingBody && (this.scaleAgentProse || this.fixedType) ? String(this.agentBodyVp) + 'vp' : this.readingFont(size))
      .fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
      .lineHeight(this.inlineLineHeight(size, readingBody, lineSpacingEm, nodes, promptBody))
      .lineSpacing(LengthMetrics.vp(this.inlineSpacing(size, lineSpacingEm, readingBody)), { onlyBetweenLines: true })
      .textAlign(alignment).wordBreak(breakAll ? WordBreak.BREAK_ALL : WordBreak.BREAK_WORD)
      .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start))
      .width(this.fitContent ? undefined : '100%')
  }

  @Builder inlineNode(node: MarkdownInline, weight: FontWeight, italic: boolean, url: string, quiet: boolean, readingBody: boolean) {
    if (node.kind === 'bold' || node.kind === 'italic' || node.kind === 'link') {
      ForEach(node.children, (child: MarkdownInline) => this.inlineNode(child,
        node.kind === 'bold' ? (this.chatReply ? FontWeight.Medium : FontWeight.Bold) : weight,
        italic || node.kind === 'italic', node.kind === 'link' ? node.url : url, quiet, readingBody),
        (child: MarkdownInline, index: number) => String(index))
    } else if (url) {
      Span(node.text).fontFamily(node.kind === 'code' ? 'monospace' : undefined)
        .fontSize(node.kind === 'code' ? this.readingFont(this.chatReply ? 16 : Tokens.meta) : undefined)
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal).fontColor(this.emphasize)
        .textBackgroundStyle({ color: node.kind === 'code' && !this.chatReply ? this.codeFill : Color.Transparent, radius: Tokens.fine })
        .decoration({ type: TextDecorationType.Underline, color: this.emphasize }).onClick(() => this.onLink(url))
    } else if (node.kind === 'code') {
      Span(node.text).fontFamily('monospace').fontSize(this.readingFont(this.chatReply ? 16 : Tokens.meta))
        .fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
        .fontColor(this.recapProse ? Colors.inlineCodeInk : this.chatReply ? this.foreground : this.secondary)
        .textBackgroundStyle({ color: this.chatReply ? Color.Transparent : this.codeFill, radius: Tokens.fine })
    } else if (readingBody && (this.scaleAgentProse || this.recapProse)) {
      ForEach(markdownProseRuns(node.text), (run: MarkdownProseRun) => {
        Span(run.text).fontSize(this.proseFont(run.han))
          .fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight).fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
      }, (run: MarkdownProseRun, index: number) => String(index))
    } else {
      Span(node.text).fontColor(quiet ? this.secondary : this.foreground).fontWeight(weight)
        .fontStyle(italic ? FontStyle.Italic : FontStyle.Normal)
    }
  }

  @Builder blockView(block: MarkdownBlock, first: boolean = false) {
    if (block.kind === 'paragraph') {
      this.inlineText(block.inlines, this.profileDisplayProse ? 0.26 : this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
        TextAlign.Start, false, false, this.recapProse || this.profileDisplayProse)
    } else if (block.kind === 'heading') {
      Column() {
        this.inlineText(block.inlines, 0.26, this.headingSize(block.level),
          this.chatReply && block.level > 1 ? FontWeight.Medium : FontWeight.Bold,
          TextAlign.Start, false, this.chatReply && block.level >= 4)
      }.alignItems(HorizontalAlign.Start).width(this.fitContent ? undefined : '100%')
        .padding({ top: this.chatReply && !first ? (block.level === 1 ? 8 : 4) : 0 })
    } else if (block.kind === 'code') {
      Column({ space: Tokens.gap }) {
        if (this.chatReply) {
          Row() {
            if (block.language) Text(block.language).fontFamily('monospace').fontSize(13).fontColor(this.secondary)
              .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
            Blank().layoutWeight(1)
            Button({ type: ButtonType.Circle }) {
              SymbolGlyph(IconActions.symbol('copy')).fontSize(16).fontColor([this.secondary])
            }.width(34).height(34).padding(0).backgroundColor(Color.Transparent)
              .responseRegion({ x: -5, y: -5, width: 44, height: 44 })
              .accessibilityText(tr('copy') + (block.language ? ' ' + block.language : ''))
              .bindTips(tr('copy')).onClick(() => this.copyCode(block.text))
          }.width('100%')
        } else if (block.language) Text(block.language).fontSize(Tokens.meta).fontColor(this.secondary)
        Scroll() {
          Text(block.text).fontFamily('monospace').fontSize(Tokens.meta).lineHeight(this.chatReply ? '21fp' : undefined).wordBreak(WordBreak.BREAK_ALL)
            .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).fontColor(this.foreground)
        }.scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: this.chatReply ? 0 : Tokens.hairline, color: this.secondary, radius: this.chatReply ? Tokens.inset : Tokens.fine })
        .borderRadius(this.chatReply ? Tokens.inset : Tokens.fine)
    } else if (block.kind === 'raw') {
      Text(block.text).fontSize(Tokens.body).fontColor(this.foreground).wordBreak(WordBreak.BREAK_WORD)
        .copyOption(this.nativeTextSelection ? CopyOptions.LocalDevice : CopyOptions.None).onTextSelectionChange((start: number, end: number) => this.onSelectionChange(end > start)).width('100%')
    } else if (block.kind === 'rule') {
      Divider().strokeWidth(Tokens.hairline).color(this.secondary).width('100%')
        .margin({ top: Tokens.gap, bottom: Tokens.gap })
    } else if (block.kind === 'quote') {
      Column({ space: Tokens.gap }) {
        ForEach(block.children, (child: MarkdownBlock) => this.blockView(child),
          (child: MarkdownBlock, index: number) => 'quote.' + String(index))
      }
        .padding(Tokens.inset).width('100%').alignItems(HorizontalAlign.Start)
        .backgroundColor(this.codeFill)
        .border({ width: Tokens.hairline, color: this.emphasize, radius: Tokens.fine })
    } else if (block.kind === 'list') {
      Column({ space: this.dockPlan ? this.readingPoints(Tokens.body)*0.2 : Tokens.gap }) {
        ForEach(block.items, (item: MarkdownBlock) => this.listItem(item),
          (item: MarkdownBlock, index: number) => 'list.' + String(index))
      }.width('100%').alignItems(HorizontalAlign.Start)
        .padding({top:this.dockPlan && !first ? Math.max(0,this.readingPoints(Tokens.body)*0.2-6) : 0})
    } else if (block.kind === 'table') {
      Scroll() {
        Column({ space: Tokens.fine }) {
          ForEach(block.rows, (row: MarkdownTableRow) => this.tableRow(row, block),
            (row: MarkdownTableRow, index: number) => 'row.' + String(index))
        }
      }
        .scrollable(ScrollDirection.Horizontal).align(Alignment.TopStart).scrollBar(BarState.Auto).width('100%')
    }
  }

  @Builder listItem(item: MarkdownBlock) {
    Row({ space: this.dockPlan ? 10 : Tokens.inset }) {
      Text(item.marker).fontSize(this.dockPlan ? this.readingFont(Tokens.body) : Tokens.body).fontColor(this.secondary)
      Column({ space: Tokens.gap }) {
        ForEach(item.children, (child: MarkdownBlock) => this.blockView(child),
          (child: MarkdownBlock, index: number) => 'item.' + String(index))
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.width('100%').alignItems(VerticalAlign.Top).padding({left:this.dockPlan ? this.readingPoints(Tokens.body)*0.65 : 0})
  }

  @Builder tableRow(row: MarkdownTableRow, table: MarkdownBlock) {
    Row() {
      ForEach(row.cells, (cell: MarkdownInline[], index: number) => this.tableCell(cell, table, index, row.header),
        (cell: MarkdownInline[], index: number) => 'cell.' + String(index))
    }.alignItems(VerticalAlign.Top)
  }

  @Builder tableCell(cell: MarkdownInline[], table: MarkdownBlock, index: number, header: boolean) {
    Column() {
      this.inlineText(cell, 0.26, Tokens.body, header ? FontWeight.Bold : FontWeight.Normal, this.tableTextAlign(table, index), true)
    }
      .width(Tokens.markdownTableCell).padding(Tokens.inset).alignItems(HorizontalAlign.Start)
      .border({ width: Tokens.hairline, color: this.secondary, radius: Tokens.fine })
      .backgroundColor(header ? this.codeFill : Color.Transparent)
  }

  build() {
    Column({ space: this.dockPlan ? 6 : Tokens.gap }) {
      ForEach(this.blocks, (block: MarkdownBlock, index: number) => {
        if (this.agentReply && block.kind === 'paragraph') {
          Column() {
            this.inlineText(block.inlines, this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
              TextAlign.Start, false, false, true)
          }.width('100%').alignItems(HorizontalAlign.Start)
            .padding({ top: index > 0 && this.blocks[index - 1]?.kind === 'paragraph' ? 10 - Tokens.gap : 0 })
        } else if (this.agentPrompt && block.kind === 'paragraph') {
          this.inlineText(block.inlines, this.paragraphLineSpacingEm, Tokens.body, FontWeight.Normal,
            TextAlign.Start, false, false, false, true)
        } else {
          this.blockView(block, index === 0)
        }
      },
        (block: MarkdownBlock, index: number) => 'block.' + String(index) + ':' + JSON.stringify(block))
    }.width(this.fitContent ? undefined : '100%').alignItems(HorizontalAlign.Start)
      .padding({ top: this.recapProse ? this.recapTopInset() : 0 })
  }
}
