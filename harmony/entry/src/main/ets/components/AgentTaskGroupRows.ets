import { AgentWorkspace } from '../models/AgentWorkspace';
import { AgentTaskLine, AgentTaskSection } from '../models/AgentTaskSections';
import { ResourceRow, Wire } from '../models/Wire';
import { tr } from '../design/Strings';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { PhoneStyle } from '../design/PhoneStyle';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct AgentTaskGroupRows {
  @Require @Param section: AgentTaskSection;
  @Require @Param workspace: AgentWorkspace;
  @Param phone: boolean = false;
  @Event onOpen: (row: ResourceRow) => void = () => {};
  @Event onOpenTodo: (row: ResourceRow) => void = () => {};
  @BuilderParam rowContent: (row: ResourceRow, depth: number) => void;
  @Local tip: string = '';
  @Local environment: NativeEnvironment = nativeEnvironment;

  private target(): number { return PhoneStyle.formSize(44,58,this.environment.fontScale); }

  private folded(section: AgentTaskSection): boolean {
    return !this.workspace.search.trim() && this.workspace.groupingFolded(section.key,section.folded);
  }

  private title(section: AgentTaskSection): string { return section.titleKey ? tr(section.titleKey) : section.title; }
  private lineTitle(line: AgentTaskLine): string {
    return line.row.title || (line.kind === 'task' ? tr('agent_task_untitled') : Wire.text(line.row.data(),'agent') || tr('agent'));
  }

  private tone(state: string): ResourceColor {
    if (['needs_you','stuck'].includes(state)) return Colors.needs;
    if (['working','waiting','planned','not_started'].includes(state)) return Colors.working;
    if (['review','unread','done'].includes(state)) return Colors.unread;
    return Colors.secondary;
  }

  private detail(section: AgentTaskSection): string {
    const count = section.direction ? (section.needs > 0 ? tr('agent_task_needs_count').replace('{n}',String(section.needs)) : '') : tr('agent_task_count').replace('{n}',String(section.lines.length));
    return [this.title(section),tr(this.folded(section) ? 'agent_task_collapsed' : 'agent_task_expanded'),count,section.hint ? tr(section.hint) : '']
      .filter((part: string) => !!part).join(', ');
  }

  @Builder header(section: AgentTaskSection) {
    Row({space:6}) {
      Text(this.title(section)).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontWeight(600)
        .fontColor(Colors.ink).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis}).flexShrink(1)
      if (section.hint) {
        Button({type:ButtonType.Circle}) {
          SymbolGlyph($r('sys.symbol.info_circle')).fontSize('15vp').fontColor([Colors.secondary])
        }.width(this.target()).height(this.target()).padding(0).backgroundColor(Color.Transparent)
          .accessibilityText(tr('agent_task_about').replace('{title}',this.title(section))).id('agents.task.info.' + section.key)
          .onClick(() => { this.tip = this.tip === section.key ? '' : section.key; })
          .bindPopup(this.tip === section.key,{message:tr(section.hint),placement:Placement.Bottom,
            onStateChange:(event:PopupStateChangeParam) => { if (!event.isVisible && this.tip === section.key) this.tip = ''; }})
      }
      if (section.direction) {
        if (section.running) LoadingProgress().width(13).height(13).color(Colors.working)
        if (section.needs > 0) Text(tr('agent_task_needs_count').replace('{n}',String(section.needs)))
          .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary).maxLines(1)
      } else {
        Text(String(section.lines.length)).fontSize(12).maxFontScale(Tokens.interfaceScaleFor(12))
          .fontWeight(600).fontColor(this.tone(section.state)).textAlign(TextAlign.Center)
          .padding({left:6,right:6,top:1,bottom:1}).borderRadius(9).backgroundColor(Colors.control)
      }
      if (section.todo) {
        Button({type:ButtonType.Circle}) {
          SymbolGlyph(Symbols.expand).fontSize('15vp').fontColor([Colors.secondary])
        }.width(this.target()).height(this.target()).padding(0).backgroundColor(Color.Transparent).accessibilityText(tr('agent_task_detail'))
          .id('agents.task.open.' + section.key).onClick(() => { if (section.todo) this.onOpenTodo(section.todo); })
      }
      Blank().layoutWeight(1)
      SymbolGlyph(this.folded(section) ? Symbols.expand : Symbols.down).fontSize('12vp').fontColor([Colors.tertiary])
    }.width('100%').constraintSize({minHeight:this.target()+20}).padding({left:20,right:20,top:10,bottom:10})
      .accessibilityText(this.detail(section)).accessibilityRole(AccessibilityRoleType.BUTTON)
      .onClick(() => this.workspace.toggleGroupingFold(section.key, this.folded(section))).id('agents.group.toggle.'+section.key)
  }

  @Builder taskOrTree(line: AgentTaskLine) {
    ListItem() {
      Button({type:ButtonType.Normal}) {
        Row({space:8}) {
          if (line.kind === 'task') {
            SymbolGlyph($r('sys.symbol.list_checkmark')).fontSize('17vp').fontColor([Colors.secondary])
          } else {
            Circle().width(8).height(8).fill(this.tone(line.state))
          }
          Text(this.lineTitle(line)).fontSize(17).maxFontScale(Tokens.interfaceFontMaxScale).fontColor(Colors.ink)
            .layoutWeight(1).maxLines(1).textOverflow({overflow:TextOverflow.Ellipsis})
          Text(tr((line.kind === 'task' ? 'agent_task_' : 'agent_task_state_') + line.state))
            .fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(this.tone(line.state)).maxLines(1)
          if (line.kind === 'task') SymbolGlyph(Symbols.expand).fontSize('12vp').fontColor([Colors.tertiary])
        }.width('100%').constraintSize({minHeight:this.target()+30}).padding({left:20+Math.min(line.depth,5)*16,right:20,top:15,bottom:15})
      }.width('100%').padding(0).backgroundColor(Colors.surface)
        .accessibilityText(this.lineTitle(line) + ', ' + tr((line.kind === 'task' ? 'agent_task_' : 'agent_task_state_') + line.state))
        .id((line.kind === 'task' ? 'agents.direction.task.' : 'agents.direction.agent.') + line.row.id)
        .onClick(() => { if (line.kind === 'task') this.onOpenTodo(line.row); else this.onOpen(line.row); })
    }
  }

  build() {
    ListItemGroup({header:() => this.header(this.section)}) {
      if (!this.folded(this.section)) {
        if (!this.section.lines.length && this.section.hint) {
          ListItem() {
            Text(tr(this.section.hint)).fontSize(13).maxFontScale(Tokens.interfaceScaleFor(13)).fontColor(Colors.secondary)
              .width('100%').padding({left:16,right:16,top:8,bottom:8}).backgroundColor(Colors.surface)
          }.id('agents.group.empty.' + this.section.key)
        }
        Repeat<AgentTaskLine>(this.section.lines).each((entry: RepeatItem<AgentTaskLine>) => {
          if (entry.item.kind === 'session') this.rowContent(entry.item.row,entry.item.depth)
          else this.taskOrTree(entry.item)
        }).key((line: AgentTaskLine) => line.kind + line.row.key())
      }
    }.margin({left:this.phone ? 4 : 0,right:this.phone ? 4 : 0}).id('agents.group.' + this.section.key)
  }
}
