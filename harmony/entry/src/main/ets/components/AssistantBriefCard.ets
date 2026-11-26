import { i18n } from '@kit.LocalizationKit';
import { briefWaitDuration } from '../core/PhoneBrief';
import { Colors } from '../design/Colors';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { AssistantFeedModel } from '../models/AssistantFeed';
import { BriefOpenButton, BriefRowActions } from './BriefRowActions';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow, Wire } from '../models/Wire';
import { nativeEnvironment } from '../platform/NativeEnvironment';

const STAGES: string[] = ['plan', 'develop', 'review', 'merge', 'deploy', 'verify'];

@ComponentV2
export struct AssistantBriefCard {
  @Require @Param model: ClientModel;
  @Require @Param feed: AssistantFeedModel;
  @Require @Param item: Record<string, Object>;
  @Param active: boolean = false;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onOpenTriage: () => void = () => {};
  @Local pastOpen: boolean = false;
  @Local stageWidth: number = 0;

  @Computed get body(): Record<string, Object> { return Wire.object(this.item, 'body'); }
  @Computed get hasPastRows(): boolean { return this.rows('failed').length + this.rows('done').length > 0; }
  @Computed get stageNamesFit(): boolean {
    if (this.stageWidth <= 0) return true;
    const ui = this.getUIContext();
    const font = Tokens.interfaceFontVp(13, nativeEnvironment.fontScale);
    return STAGES.every((stage: string) => ui.px2vp(ui.getMeasureUtils().measureText({
      textContent: tr('session_brief_stage_' + stage), fontSize: font, fontWeight: FontWeight.Medium
    })) <= this.stageWidth / STAGES.length);
  }

  @Computed get setupRows(): Record<string, Object>[] {
    const seen = new Set<string>();
    return this.rows('setup').filter((line: Record<string, Object>) => {
      const id = Wire.text(line, 'id');
      if (!id || !Wire.text(line, 'text').trim() || seen.has(id)) return false;
      seen.add(id);
      return true;
    });
  }

  private owned(): boolean {
    const conversation = Wire.text(this.item, 'conversation'), manager = Wire.text(this.item, 'manager');
    return this.active && this.model.connected && this.model.selected.kind === 'conversation' &&
      (!conversation || conversation === this.model.selected.id) && (!manager || manager === this.model.profile.manager);
  }

  private destination(section: string, row: Record<string, Object>): ResourceRow | undefined {
    if (!this.owned()) return undefined;
    const id = Wire.text(row, 'id');
    if (!id) return undefined;
    if (section === 'waiting' || section === 'later') {
      const machine = Wire.text(row, 'machine_id'), session = Wire.text(row, 'session_id');
      if (!machine || !session) return section === 'waiting' ? this.model.briefWaitingSession(id) : undefined;
      return this.model.sessions.find((agent: ResourceRow) => agent.machine === machine && (agent.session || agent.id) === session &&
        agent.manager === this.model.profile.manager && (!agent.group || agent.group === this.model.profile.id));
    }
    if (!['accept', 'progress', 'done'].includes(section)) return undefined;
    const todo = new ResourceRow(id, Wire.text(row, 'title'), 'todo');
    todo.group = this.model.profile.id;
    todo.manager = this.model.profile.manager;
    return todo;
  }

  private rows(section: string): Record<string, Object>[] { return Wire.rows(this.body, section); }

  private count(section: string): number {
    return Math.max(this.rows(section).length, Wire.number(this.body, section + '_total'));
  }

  private stageText(key: string, stage: string): string {
    return tr(key).replace('{step}', String(STAGES.indexOf(stage) + 1)).replace('{total}', String(STAGES.length))
      .replace('{name}', tr('session_brief_stage_' + stage));
  }

  private shownStage(row: Record<string, Object>): string {
    return Wire.text(row, 'stage_kind') === 'general' ? '' : Wire.text(row, 'stage').trim();
  }

  private pastTitle(): string {
    return tr('session_brief_past').replace('{hours}', String(Wire.number(this.body, 'hours', 12)))
      .replace('{done}', String(this.count('done'))).replace('{failed}', String(this.count('failed')));
  }

  private language(): string {
    return this.model.language && this.model.language !== 'default' ? this.model.language : i18n.getSystemLanguage();
  }

  private meta(row: Record<string, Object>): string {
    const wait = briefWaitDuration(Wire.number(row, 'waited_s'), this.language(),
      tr('session_brief_days'), tr('session_brief_hours'), tr('session_brief_minutes'));
    const parts = [tr('session_brief_waited').replace('{value}', wait)];
    if (Wire.bool(row, 'nudged')) parts.push(tr('session_brief_nudged'));
    const from = Wire.text(row, 'from').trim();
    if (from) parts.push(from);
    return parts.join(' · ');
  }

  private pastDetail(section: string, row: Record<string, Object>): string {
    const at = Date.parse(Wire.text(row, 'at'));
    const time = Number.isFinite(at) ? new Date(at).toLocaleTimeString(this.language(), {
      hour: '2-digit', minute: '2-digit', hour12: false }) : '';
    if (section === 'done') return time;
    const why = Wire.text(row, 'why').trim();
    if (!time) return why;
    const state = Wire.text(row, 'kind') === 'routine' ? (Wire.bool(row, 'skipped') ? 'skipped' : 'run_failed') : 'start_failed';
    const summary = tr('session_brief_' + state).replace('{value}', time);
    return why ? summary + ' — ' + why : summary;
  }

  private stepAccessibility(name: string, stage: string, index: number): string {
    const current = STAGES.indexOf(stage);
    const state = index < current ? 'done' : index === current ? 'current' : 'ahead';
    return tr('session_brief_stage_' + state).replace('{value}', tr('session_brief_stage_' + name))
      .replace('{step}', String(index + 1)).replace('{total}', String(STAGES.length));
  }

  @Builder note(value: string) {
    Text(value).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
      .wordBreak(WordBreak.BREAK_WORD)
  }

  @Builder more(count: number) {
    if (count > 0) this.note(tr('session_brief_more').replace('{count}', String(count)))
  }

  @Builder heading(label: string, icon: Resource, tint: ResourceColor) {
    Row({ space: 8 }) {
      Stack() {
        Rect().fill(tint).opacity(0.14).radius(6).width('100%').height('100%')
        SymbolGlyph(icon).fontSize(Tokens.interfaceFontVp(14, nativeEnvironment.fontScale)).fontColor([tint])
      }.width(PhoneStyle.formSize(24, 32, nativeEnvironment.fontScale)).aspectRatio(1)
      Text(label).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontWeight(600).fontColor(Colors.secondary)
    }.padding({top: 4}).accessibilityGroup(true)
  }

  @Builder stageDot(index: number, current: number) {
    Stack() {
      Row() {
        Divider().strokeWidth(1).color(index === 0 ? Color.Transparent : index <= current ? Colors.positive : Colors.divider).layoutWeight(1)
        Divider().strokeWidth(1).color(index === STAGES.length - 1 ? Color.Transparent : index < current ? Colors.positive : Colors.divider).layoutWeight(1)
      }.width('100%')
      Circle().width(PhoneStyle.formSize(16, 21, nativeEnvironment.fontScale)).aspectRatio(1)
        .fill(index < current ? Colors.positive : index === current ? Colors.link : Colors.surface)
        .stroke(index > current ? Colors.divider : Color.Transparent).strokeWidth(1)
      if (index < current) SymbolGlyph(Symbols.check).fontSize('12vp').fontColor([Colors.onAction])
      if (index === current) Circle().width(PhoneStyle.formSize(18, 24, nativeEnvironment.fontScale)).aspectRatio(1)
        .fill(Color.Transparent).stroke(Colors.link).strokeWidth(1).opacity(0.35)
    }.width('100%').height(PhoneStyle.formSize(18, 24, nativeEnvironment.fontScale))
  }

  @Builder stages(stage: string) {
    Column({ space: 4 }) {
      Row() {
        ForEach(STAGES, (name: string, index: number) => {
          Column({ space: 4 }) {
            this.stageDot(index, STAGES.indexOf(stage))
            if (this.stageNamesFit) Text(tr('session_brief_stage_' + name)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
              .fontColor(name === stage ? Colors.link : index < STAGES.indexOf(stage) ? Colors.ink : Colors.secondary)
              .fontWeight(name === stage ? 600 : FontWeight.Normal).textAlign(TextAlign.Center).maxLines(1)
          }.layoutWeight(1).accessibilityGroup(!this.stageNamesFit)
            .accessibilityText(this.stageNamesFit ? '' : this.stepAccessibility(name, stage, index))
            .id('assistant.brief.stage.' + stage + '.' + name)
        }, (name: string) => name)
      }.width('100%')
      if (!this.stageNamesFit) Text(this.stageText('session_brief_stage_summary', stage))
        .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontWeight(600).fontColor(Colors.link)
    }.width('100%').onAreaChange((_: Area, area: Area) => { this.stageWidth = Number(area.width); })
      .alignItems(HorizontalAlign.Start).accessibilityGroup(this.stageNamesFit)
      .accessibilityText(this.stageNamesFit ? this.stageText('session_brief_stage_accessibility', stage) : '')
      .id('assistant.brief.stage.' + stage)
  }

  private openRow(section: string, row: Record<string, Object>): void {
    const target = this.destination(section, row);
    if (target) this.onOpen(target);
  }

  @Builder title(row: Record<string, Object>) {
    Text(Wire.text(row, 'title') || Wire.text(row, 'why')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15))
      .fontWeight(FontWeight.Medium).lineHeight(PhoneStyle.settingsExampleLineHeight(18, 25.33, nativeEnvironment.fontScale))
      .fontColor(Colors.ink).wordBreak(WordBreak.BREAK_WORD)
  }

  @Builder pastRow(section: string, row: Record<string, Object>) {
    Row({space: 8}) {
      SymbolGlyph(section === 'done' ? $r('sys.symbol.checkmark_circle_fill') : $r('sys.symbol.exclamationmark_circle_fill'))
        .fontSize(Tokens.interfaceFontVp(14, nativeEnvironment.fontScale)).fontColor([section === 'done' ? Colors.positive : Colors.danger])
      Column({space: 2}) {
        this.title(row)
        if (this.pastDetail(section, row)) this.note(this.pastDetail(section, row))
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
      if (this.destination(section, row)) SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.tertiary])
    }.width('100%').padding({top: 2, bottom: 2}).accessibilityGroup(true)
      .onClick(this.destination(section, row) ? () => { this.openRow(section, row); } : undefined)
      .id('assistant.brief.' + section + '.' + Wire.text(row, 'id'))
  }

  @Builder itemRow(section: string, row: Record<string, Object>) {
    Column({ space: 4 }) {
      Row({ space: 12 }) {
        Column({space: 2}) {
          this.title(row)
          if (section === 'waiting') this.note(this.meta(row))
        }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        if ((section === 'waiting' || section === 'progress') && this.destination(section, row)) {
          BriefOpenButton({title: Wire.text(row, 'title'), identity: 'assistant.brief.' + section + '.' + Wire.text(row, 'id'),
            onOpen: () => this.openRow(section, row)})
        }
      }.width('100%')
      if (section === 'accept' && Wire.text(row, 'verdict')) {
        Text(Wire.text(row, 'verdict')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.ink)
      }
      if (section === 'accept' && Wire.text(row, 'detail')) this.note(Wire.text(row, 'detail'))
      if (section === 'accept' && Wire.text(row, 'recommend')) {
        this.note(tr('session_brief_recommended').replace('{value}', Wire.text(row, 'recommend')))
      }
      if (section === 'later' || section === 'accept') {
        this.note(this.meta(row))
        BriefRowActions({feed: this.feed, item: this.item, row: row, section: section, active: this.owned(),
          showsOpen: !!this.destination(section, row), onOpen: () => this.openRow(section, row)})
      }
      if (section === 'progress' && STAGES.includes(this.shownStage(row))) this.stages(this.shownStage(row))
      else if (section === 'progress' && this.shownStage(row)) this.note(this.shownStage(row))
    }.width('100%').alignItems(HorizontalAlign.Start).padding({top: 4, bottom: 4})
  }

  @Builder section(section: string, icon: Resource, tint: ResourceColor) {
    Column({ space: 4 }) {
      this.heading(tr('session_brief_' + section + '_count').replace('{count}', String(this.count(section))), icon, tint)
      if (!this.rows(section).length && (section === 'waiting' || section === 'accept')) this.note(tr('session_brief_empty'))
      ForEach(this.rows(section), (row: Record<string, Object>) => {
        if (section !== 'accept' || this.feed.briefOutcome(section, row) !== 'completed') this.itemRow(section, row)
      },
        (row: Record<string, Object>, index: number) => JSON.stringify([section, index, row]))
      this.more(this.count(section) - this.rows(section).length)
    }.width('100%').alignItems(HorizontalAlign.Start).id('assistant.brief.' + section)
  }

  @Builder past() {
    Column({ space: 4 }) {
      if (this.hasPastRows) {
        Row({ space: 8 }) {
          this.heading(this.pastTitle(), $r('sys.symbol.clock'), Colors.link)
          Blank()
          SymbolGlyph(this.pastOpen ? Symbols.up : Symbols.down).fontSize(Tokens.interfaceFontVp(14, nativeEnvironment.fontScale)).fontColor([Colors.tertiary])
        }.width('100%').accessibilityGroup(true).accessibilityRole(AccessibilityRoleType.BUTTON)
          .accessibilityDescription(tr(this.pastOpen ? 'control_expanded' : 'control_collapsed'))
          .onClick(() => { this.pastOpen = !this.pastOpen; }).id('assistant.brief.past')
      } else {
        this.heading(this.pastTitle(), $r('sys.symbol.clock'), Colors.link)
        this.note(tr('session_brief_empty'))
      }
      if (!this.hasPastRows || this.pastOpen) {
        ForEach(this.rows('failed'), (row: Record<string, Object>) => { this.pastRow('failed', row) },
          (row: Record<string, Object>, index: number) => JSON.stringify([index, row]))
        ForEach(this.rows('done'), (row: Record<string, Object>) => { this.pastRow('done', row) },
          (row: Record<string, Object>, index: number) => JSON.stringify([index, row]))
        this.more(this.count('failed') + this.count('done') - this.rows('failed').length - this.rows('done').length)
      }
    }.width('100%').alignItems(HorizontalAlign.Start)
  }

  @Builder setup() {
    Column({ space: 4 }) {
      this.heading(tr('session_brief_setup'), Symbols.gear, Colors.secondary)
      this.note(tr('session_brief_setup_note'))
      ForEach(this.setupRows, (line: Record<string, Object>) => {
        Row({ space: 12 }) {
          Text(Wire.text(line, 'text')).fontSize(15).maxFontScale(Tokens.interfaceScaleFor(15)).fontColor(Colors.ink).layoutWeight(1)
          if (['triage_decide', 'triage_auto_continue', 'triage_wake'].includes(Wire.text(line, 'id')) && this.owned()) {
            Button(tr('open_settings'), {type: ButtonType.Capsule}).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
              .fontWeight(600).padding({left: 12, right: 12}).backgroundColor(Colors.control).fontColor(Colors.link)
              .onClick(() => { if (this.owned()) this.onOpenTriage(); }).id('assistant.brief.setup.' + Wire.text(line, 'id'))
          }
        }.width('100%').padding({top: 4, bottom: 4})
      }, (line: Record<string, Object>) => JSON.stringify(line))
    }.width('100%').alignItems(HorizontalAlign.Start).id('assistant.brief.setup')
  }

  build() {
    Column({ space: 8 }) {
      Row({ space: 8 }) {
        SymbolGlyph($r('sys.symbol.sun_max_fill')).fontSize(Tokens.interfaceFontVp(13, nativeEnvironment.fontScale)).fontColor([Colors.secondary])
        Text(Wire.text(this.body, 'title') || tr('assistant_detail_brief')).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13))
          .maxLines(1).fontWeight(600).fontColor(Colors.secondary)
      }
      if (Wire.bool(this.body, 'waiting_unknown')) {
        this.heading(tr('session_brief_waiting_unknown'), $r('sys.symbol.hand_raised_fingers_spread'), Colors.needs)
        this.note(tr('session_brief_waiting_note'))
      } else { this.section('waiting', $r('sys.symbol.hand_raised_fingers_spread'), Colors.needs) }
      Divider().color(Colors.divider)
      this.past()
      Divider().color(Colors.divider)
      this.section('accept', $r('sys.symbol.checkmark_circle'), Colors.positive)
      if (this.count('later')) { Divider().color(Colors.divider); this.section('later', $r('sys.symbol.questionmark_circle'), Colors.secondary) }
      if (this.count('progress')) { Divider().color(Colors.divider); this.section('progress', $r('sys.symbol.gearshape_fill'), Colors.working) }
      if (this.setupRows.length) { Divider().color(Colors.divider); this.setup() }
    }.width('100%').alignItems(HorizontalAlign.Start).padding(PhoneStyle.page).backgroundColor(Colors.surface)
      .border({width: Tokens.divider, color: Colors.divider, radius: Tokens.card}).borderRadius(Tokens.card).id('assistant.brief.card')
  }
}
