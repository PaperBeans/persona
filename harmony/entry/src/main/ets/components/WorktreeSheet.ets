import { Tokens } from '../design/Tokens';
import { WorktreeActions } from '../models/WorktreeActions';
import { PhoneStyle, PhoneSectionTitle } from '../design/PhoneStyle';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';

@ComponentV2
export struct WorktreeSheet {
  @Require @Param form: WorktreeActions;
  @Local editingPath: boolean = false;
  @Event onCancel: () => void = () => {};
  @Event onSubmit: () => void = () => {};
  @Event onRetry: () => void = () => {};
  @Event onReconcile: (replay: boolean) => void = () => {};

  private heading(): string {
    return tr(this.form.action === 'create' ? 'tree_create' : this.form.action === 'rename' ? 'tree_rename' : 'tree_cleanup');
  }
  private verb(): string {
    return tr(this.form.action === 'create' ? 'create' : this.form.action === 'rename' ? 'save' : 'tree_remove');
  }
  @Builder field(label: string, identity: string, placeholder: string, change: (text: string) => void) {
    Column({ space: 2 }) {
      Text(label).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      if (identity === 'tree.form.path') {
        TextArea({ text: this.form.path, placeholder: placeholder }).fontSize(15).fontFamily('monospace')
          .lineHeight('22fp').constraintSize({ minHeight: 44, maxHeight: 112 }).maxLines(4)
          .padding({ left: 0, right: 0, top: 9, bottom: 8 }).borderRadius(0)
          .backgroundColor(Color.Transparent).fontColor(Colors.ink).caretColor(Colors.action)
          .onFocus(() => { this.editingPath = true; }).onBlur(() => { this.editingPath = false; })
          .enterKeyType(EnterKeyType.Done).inputFilter('[^\\r\\n]*')
          .onChange((v: string) => { if (this.editingPath) change(v); })
          .enabled(!this.form.busy && (!this.form.unknown || this.form.legacy)).accessibilityDescription(label).id(identity)
      } else {
        TextInput({ text: identity === 'tree.form.branch' ? this.form.branch : this.form.name, placeholder: placeholder })
          .fontSize(16).fontFamily('monospace').constraintSize({minHeight:44}).padding({ left: 0, right: 0 }).borderRadius(0)
          .backgroundColor(Color.Transparent).fontColor(Colors.ink).caretColor(Colors.action)
          .onChange(change).enabled(!this.form.busy && (!this.form.unknown || this.form.legacy)).accessibilityDescription(label).id(identity)
      }
    }.width('100%').alignItems(HorizontalAlign.Start).backgroundColor(Colors.surface).padding({ left: 15, right: 15, top: 10, bottom: 3 })
  }
  @Builder valueRow(label: string, value: string, danger: boolean = false) {
    Row({ space: 14 }) {
      Text(label).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.secondary)
      Text(value).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontWeight(FontWeight.Medium).fontColor(danger ? Colors.danger : Colors.ink)
        .textAlign(TextAlign.End).layoutWeight(1).wordBreak(WordBreak.BREAK_WORD)
    }.width('100%').constraintSize({ minHeight: 45 }).padding({ left: 15, right: 15, top: 7, bottom: 7 })
  }
  @Builder codeValue(label: string, value: string) {
    Column({ space: 5 }) {
      Text(label).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      Text(value).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontFamily('monospace').fontColor(Colors.ink).wordBreak(WordBreak.BREAK_ALL)
        .width('100%').textAlign(TextAlign.Start).copyOption(CopyOptions.LocalDevice)
    }.width('100%').alignItems(HorizontalAlign.Start).padding({ left: 15, right: 15, top: 11, bottom: 11 })
  }
  @Builder rule() {
    Divider().color(Colors.divider).strokeWidth(0.5).margin({ left: 15 })
  }
  @Builder toggle(label: string, checked: boolean, identity: string, change: (v: boolean) => void) {
    Row({ space: 10 }) {
      Text(label).layoutWeight(1).fontColor(Colors.ink).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16))
      Toggle({ type: ToggleType.Switch, isOn: checked }).selectedColor(this.form.action === 'clean' ? Colors.danger : Colors.action).onChange(change)
        .enabled(!this.form.busy && !this.form.unknown).accessibilityText(label).id(identity)
    }.width('100%').constraintSize({ minHeight: 48 }).padding({ left: 15, right: 15, top: 6, bottom: 6 })
  }
  @Builder worktreeBar() {
    Row({ space: 8 }) {
      Button({ type: ButtonType.Circle }) {
        SymbolGlyph($r('sys.symbol.xmark')).fontSize('17vp').fontColor([Colors.ink])
      }.width(38).height(38).padding(0).backgroundColor(Colors.surface)
        .responseRegion({ x: -3, y: -3, width: 44, height: 44 }).accessibilityText(tr('close'))
        .enabled(!this.form.busy).onClick(() => this.onCancel()).id('tree.form.cancel')
      Text(this.heading()).layoutWeight(1).textAlign(TextAlign.Center).fontSize(18).maxFontScale(Tokens.interfaceScaleFor(18)).fontWeight(FontWeight.Bold)
        .fontColor(Colors.ink).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
      Button({ type: this.form.action === 'rename' ? ButtonType.Circle : ButtonType.Capsule }) {
        if (this.form.busy) LoadingProgress().width(19).height(19).color(Colors.secondary)
        else if (this.form.action === 'rename') SymbolGlyph(Symbols.check).fontSize('18vp')
          .fontColor([this.form.canSubmit ? Colors.onAction : Colors.secondary])
        else Text(this.verb()).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontWeight(FontWeight.Medium)
          .fontColor(this.form.canSubmit ? (this.form.action === 'clean' ? Colors.danger : Colors.onAction) : Colors.secondary)
          .maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
      }.height(38).constraintSize({ minWidth: this.form.action === 'rename' ? 38 : 54, maxWidth: '30%' })
        .padding({left:this.form.action === 'rename' ? 0 : 14,right:this.form.action === 'rename' ? 0 : 14})
        .backgroundColor(this.form.canSubmit ? (this.form.action === 'clean' ? Colors.surface : Colors.action) : Colors.control)
        .responseRegion({ x: 0, y: -3, width: '100%', height: 44 }).enabled(this.form.canSubmit)
        .accessibilityText(this.verb()).onClick(() => this.onSubmit()).id('tree.form.confirm')
    }.width('100%').constraintSize({minHeight:58}).padding({ left: PhoneStyle.page, right: PhoneStyle.page, top: 8, bottom: 8 })
  }
  build() {
    Column() {
      this.worktreeBar()
      Scroll() {
        Column() {
          if (this.form.action !== 'rename') {
            PhoneSectionTitle({ text: tr('tree_location') })
            Column() {
              this.valueRow(tr('machines'), this.form.machine)
              this.rule()
              ForEach([this.form.locationTitle], (title:string) => {
                this.valueRow(tr(this.form.action === 'create' ? 'tree_source' : 'project_worktree'), title)
              }, (title:string):string => title)
              if (this.form.baseBranch) {
                this.rule()
                this.codeValue(tr('tree_base_branch'), this.form.baseBranch)
              }
            }.width('100%').borderRadius(PhoneStyle.cardRadius).backgroundColor(Colors.surface).clip(true)
          }
          if (this.form.loading) {
            LoadingProgress().width(30).height(30).margin(20)
          } else if (this.form.ready || this.form.unknown) {
            if (this.form.action === 'create') {
              PhoneSectionTitle({ text: tr('tree_checkout') })
              Column() {
                if (this.form.supportsAutomatic) this.toggle(tr('tree_automatic'), this.form.automatic, 'tree.form.auto', (v: boolean) => this.form.automatic = v)
                if (!this.form.automatic) {
                  if (this.form.supportsAutomatic) this.rule()
                  this.field(tr('tree_branch'), 'tree.form.branch', tr('tree_branch_hint'), (v: string) => this.form.setBranch(v))
                  this.rule()
                  this.field(tr('directory'), 'tree.form.path', tr('tree_path_hint'), (v: string) => this.form.setPath(v))
                }
              }.width('100%').borderRadius(PhoneStyle.cardRadius).backgroundColor(Colors.surface).clip(true)
            } else if (this.form.action === 'rename') {
              Column() {
                if (this.form.unknown && this.form.removedRenameDirectory) {
                  this.codeValue(tr('tree_rename_previous_directory'),this.form.removedRenameDirectory)
                  this.rule()
                  this.codeValue(tr('directory'),this.form.path)
                } else if (this.form.unknown && this.form.renameCurrent) {
                  Column() {
                    this.valueRow(tr('machines'),this.form.machine)
                    this.rule()
                    this.codeValue(tr('directory'),this.form.path)
                    this.rule()
                    this.valueRow(tr('tree_rename_current_name'),this.form.renameCurrent)
                    this.rule()
                    this.valueRow(tr('tree_rename_requested_name'),this.form.name)
                    if (this.form.renameOriginal && this.form.renameOriginal !== this.form.renameCurrent) {
                      this.rule()
                      this.valueRow(tr('tree_rename_original_name'),this.form.renameOriginal)
                    }
                  }.width('100%').id('tree.form.rename.review')
                } else this.field(tr('tree_display_name'), 'tree.form.name', tr('tree_display_name'), (v: string) => this.form.name = v)
              }.margin({top:14}).width('100%').borderRadius(PhoneStyle.cardRadius).backgroundColor(Colors.surface).clip(true)
            } else {
              PhoneSectionTitle({ text: tr('tree_cleanup_preview') })
              Column() {
                this.codeValue(tr('directory'), this.form.path)
                this.rule()
                if (this.form.branch) {
                  this.codeValue(tr('tree_branch'), this.form.branch)
                  this.rule()
                }
                this.valueRow(tr('tree_dirty_files'), String(this.form.dirty), this.form.dirty > 0)
              }.width('100%').borderRadius(PhoneStyle.cardRadius).backgroundColor(Colors.surface).clip(true).id('tree.form.preview')
              if (this.form.dirty > 0) Text(tr('tree_changed_files')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.danger)
                .padding({ left: 6, right: 6, top: 6 })
              if (this.form.dirty || (!this.form.folder && this.form.branch)) {
                Column() {
                  if (this.form.dirty) this.toggle(tr('tree_force'), this.form.force, 'tree.form.force', (v: boolean) => this.form.force = v)
                  if (!this.form.folder && this.form.branch) {
                    if (this.form.dirty) this.rule()
                    this.toggle(tr('tree_delete_branch'), this.form.deleteBranch, 'tree.form.delete_branch', (v: boolean) => this.form.deleteBranch = v)
                  }
                }.width('100%').borderRadius(PhoneStyle.cardRadius).backgroundColor(Colors.surface).clip(true).margin({top:14})
              }
            }
          }
          if (this.form.error) {
            Text(tr(this.form.error)).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(this.form.renameCurrent || this.form.removedRenameDirectory || this.form.error === 'tree_cleanup_present' || this.form.error === 'tree_cleanup_removing' ? Colors.secondary : Colors.danger).padding(12).id('tree.form.error')
            if (!this.form.ready && !this.form.unknown) Button(tr('retry'), { buttonStyle: ButtonStyleMode.TEXTUAL }).onClick(() => this.onRetry()).id('tree.form.retry')
            if (this.form.unknown && (this.form.requestCheckAvailable || this.form.busy)) {
              Button(tr(this.form.action === 'clean' ? 'tree_check_result' : 'tree_check_worktree_result'), { buttonStyle: ButtonStyleMode.TEXTUAL })
                .enabled(this.form.canCheck).onClick(() => this.onReconcile(false)).id('tree.form.reconcile')
              if (this.form.canRetrySame) Button(tr('tree_retry_same_request'), { buttonStyle: ButtonStyleMode.TEXTUAL })
                .onClick(() => this.onReconcile(true)).id('tree.form.retry_same')
              if (this.form.cleanupRepreviewAvailable) {
                Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
                  Text(tr('tree_revoke_unused_preview')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16))
                    .fontColor(Colors.link).textAlign(TextAlign.Center).wordBreak(WordBreak.BREAK_WORD)
                }.width('100%').constraintSize({minHeight:48}).padding({left:12,right:12,top:8,bottom:8})
                  .accessibilityText(tr('tree_revoke_unused_preview')).enabled(this.form.canCheck)
                  .onClick(() => this.onReconcile(true)).id('tree.form.cleanup.repreview')
              }
              if (this.form.action==='rename' && this.form.removedRenameDirectory) {
                Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
                  Text(tr('tree_rename_removed_continue')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16))
                    .fontColor(Colors.link).textAlign(TextAlign.Center).wordBreak(WordBreak.BREAK_WORD)
                }.width('100%').constraintSize({minHeight:48}).padding({left:12,right:12,top:8,bottom:8})
                  .accessibilityText(tr('tree_rename_removed_continue')).enabled(this.form.canCheck)
                  .onClick(()=>this.onReconcile(true)).id('tree.form.rename.continue_removed')
              }
              if (this.form.action === 'rename' && this.form.renameCurrent) {
                Button({type:ButtonType.Normal,buttonStyle:ButtonStyleMode.TEXTUAL}) {
                  Text(tr('tree_rename_keep_current')).fontSize(16).maxFontScale(Tokens.interfaceScaleFor(16)).fontColor(Colors.link).textAlign(TextAlign.Center).wordBreak(WordBreak.BREAK_WORD)
                }.width('100%').constraintSize({minHeight:48}).padding({left:12,right:12,top:8,bottom:8})
                  .accessibilityText(tr('tree_rename_keep_current')).enabled(this.form.canCheck)
                  .onClick(() => this.onReconcile(true)).id('tree.form.rename.keep')
              }
              if (this.form.legacy && this.form.releaseReady) Button(tr('tree_confirm_not_created'), { buttonStyle: ButtonStyleMode.TEXTUAL })
                .enabled(!this.form.busy).onClick(() => this.onReconcile(true)).id('tree.form.confirm_absent')
            }
          }
        }.width('100%').padding({ left: PhoneStyle.page, right: PhoneStyle.page, bottom: 28 }).alignItems(HorizontalAlign.Start)
      }.align(Alignment.TopStart).width('100%').layoutWeight(1)
    }.width('100%').height('100%').backgroundColor(Colors.grouped).id('tree.form')
  }
}
