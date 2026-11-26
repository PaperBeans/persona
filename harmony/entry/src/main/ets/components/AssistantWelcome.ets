import { Symbols } from '../design/Symbols';
import { Colors } from '../design/Colors';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { Wire } from '../models/Wire';
import { nativeEnvironment, NativeEnvironment } from '../platform/NativeEnvironment';

class WelcomeSuggestion {
  id: string;
  title: string;
  prompt: string;
  local: boolean;
  needs: string[];
  constructor(id: string, title: string, prompt: string, local: boolean = false, needs: string[] = []) {
    this.id = id; this.title = title; this.prompt = prompt; this.local = local; this.needs = needs;
  }
}

function builtInQuestions(): WelcomeSuggestion[] {
  return ['agenda', 'mail', 'agents', 'lark'].map((id: string) => {
    const title = tr('phone_chat_ask_' + id);
    return new WelcomeSuggestion(id, title, title, false, id === 'agenda' ? ['phone_calendar'] : []);
  });
}

function readinessQuestions(result: Record<string, Object>): WelcomeSuggestion[] {
  const seen = new Set<string>(), questions: WelcomeSuggestion[] = [];
  for (const item of Wire.rows(result, 'suggestions')) {
    const id = Wire.text(item, 'id'), title = Wire.text(item, 'title');
    if (!id || !title || seen.has(id)) continue;
    seen.add(id);
    questions.push(new WelcomeSuggestion(id, title, Wire.text(item, 'prompt') || title, Wire.bool(item, 'local'), Wire.strings(item, 'needs')));
  }
  return questions.length ? questions : builtInQuestions();
}

@ComponentV2
export struct AssistantWelcome {
  @Require @Param model: ClientModel;
  @Param canAsk: boolean = true;
  @Event onPlace: (name: string) => void = () => {};
  @Event onAsk: (prompt: string) => void = () => {};
  @Local suggestions: WelcomeSuggestion[] = [];
  @Local help: WelcomeSuggestion | undefined;
  @Local helpShown: boolean = false;
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Local contentWidth: number = 360;
  private revision: number = 0;
  private ownerKey: string = '';

  aboutToAppear(): void { this.reload(); }
  aboutToDisappear(): void { this.revision++; this.ownerKey = ''; this.helpShown = false; }
  @Monitor('model.profile', 'model.selected', 'model.connected', 'model.features')
  contextChanged(): void { this.reload(); }

  private targetKey(): string { return JSON.stringify([this.model.scope(), this.model.connectionGeneration(), this.model.target()]); }
  private async reload(): Promise<void> {
    const revision = ++this.revision, owner = this.model, key = this.targetKey();
    this.ownerKey = key; this.helpShown = false; this.help = undefined; this.suggestions = [];
    if (!owner.connected) return;
    if (!owner.features.includes('assistant_readiness')) { this.suggestions = builtInQuestions(); return; }
    try {
      const result = await owner.repo().call('/api/v1/assistant/readiness');
      if (revision === this.revision && this.model === owner && this.targetKey() === key) this.suggestions = readinessQuestions(result);
    } catch (_) {
      if (revision === this.revision && this.model === owner && this.targetKey() === key) this.suggestions = builtInQuestions();
    }
  }
  private greeting(): string {
    const hour = new Date().getHours();
    return tr(hour >= 5 && hour < 11 ? 'welcome_morning' : hour >= 11 && hour < 18 ? 'welcome_afternoon' : 'welcome_evening');
  }
  private mainConversation(): boolean {
    return !this.model.mainId || this.model.selected.id === this.model.mainId &&
      (!this.model.mainMachineId || this.model.selected.machine === this.model.mainMachineId);
  }
  private questionSymbol(id: string): Resource {
    if (id === 'today' || id === 'agenda') return Symbols.calendar;
    if (id === 'connect_mail' || id === 'mail') return Symbols.inbox;
    if (id === 'largest_folders') return Symbols.folder;
    if (id === 'agents') return Symbols.device;
    if (id === 'how_to_connect_model') return Symbols.gear;
    if (id === 'what_can_you_do') return $r('sys.symbol.questionmark_circle');
    return Symbols.persona;
  }
  private choose(item: WelcomeSuggestion): void {
    if (!this.model.connected || this.ownerKey !== this.targetKey() || !this.suggestions.includes(item)) return;
    if (item.local) { this.help = item; this.helpShown = true; return; }
    if (!this.canAsk) return;
    if (item.needs.length) this.getUIContext().getPromptAction().showToast({ message: tr('phone_chat_phone_read') });
    this.onAsk(item.prompt);
  }
  @Builder question(item: WelcomeSuggestion) {
    Button({type:ButtonType.Normal}) {
      Column({space:10}) {
        SymbolGlyph(this.questionSymbol(item.id)).fontSize(22).fontColor([Colors.link])
        Text(item.title).fontSize(16).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          .width('100%').textAlign(TextAlign.Start)
      }.width('100%').constraintSize({ minHeight: 92 }).alignItems(HorizontalAlign.Start).justifyContent(FlexAlign.Start).padding(14)
    }.width('100%').padding(0).backgroundColor(Colors.control).borderRadius(15)
      .accessibilityText(item.title).enabled(item.local || this.canAsk)
      .onClick(() => this.choose(item)).id('welcome.suggestion.' + item.id)
  }
  @Builder helpSheet() {
    Column({ space: Tokens.page }) {
      Text(tr(this.help?.id === 'how_to_connect_model' ? 'phone_chat_help_model' : 'phone_chat_help_skills'))
        .fontSize(Tokens.body).fontColor(Colors.ink).width('100%')
      if (this.help?.id === 'how_to_connect_model') {
        Button(tr('open_settings')).onClick(() => {
          if (this.ownerKey !== this.targetKey()) return;
          this.helpShown = false; this.onPlace('settings');
        }).id('welcome.help.settings')
      }
    }.width('100%').padding({ left: 20, right: 20, top: 14, bottom: 32 }).alignItems(HorizontalAlign.Start).id('welcome.help')
  }
  build() {
    Column({space:18}) {
      Column({space:8}) {
        Text(this.greeting()).fontSize(32).fontWeight(FontWeight.Bold).fontColor(Colors.ink).width('100%')
        Text(tr(this.mainConversation() ? 'phone_chat_intro_main' : 'phone_chat_intro_side')).fontSize(16)
          .lineHeight(23).fontColor(Colors.secondary).width('100%')
      }.width('100%').alignItems(HorizontalAlign.Start)
      if (this.suggestions.length) {
        GridRow({columns: this.contentWidth >= 300 && this.environment.fontScale < 1.5 ? 2 : 1, gutter: { x: 10, y: 10 }}) {
          ForEach(this.suggestions, (item: WelcomeSuggestion) => {
            GridCol({span:1}) { this.question(item) }
          }, (item: WelcomeSuggestion) => item.id)
        }.width('100%')
      }
      Flex({ wrap: FlexWrap.Wrap, alignItems: ItemAlign.Center }) {
        ForEach(['projects','inbox','todo'], (key: string) => {
          Button({type:ButtonType.Normal}) {
            Row({space:5}) {
              SymbolGlyph(key === 'projects' ? Symbols.folder : key === 'inbox' ? Symbols.inbox : Symbols.check)
                .fontSize(14).fontColor([Colors.link])
              Text(tr(key === 'todo' ? 'todos' : key)).fontSize(14).fontColor(Colors.link)
            }
          }.padding({left:7,right:7}).constraintSize({minHeight:44}).backgroundColor(Color.Transparent)
            .onClick(() => this.onPlace(key)).id('welcome.' + key)
        }, (key: string) => key)
      }.width('100%')
    }.alignItems(HorizontalAlign.Start).width('100%').padding({left:Tokens.page,right:Tokens.page,top:24,bottom:12})
      .onAreaChange((_old: Area, area: Area) => { const width = Number(area.width); if (width > 0) this.contentWidth = width; })
      .bindSheet($$this.helpShown, this.helpSheet(), { title: { title: this.help?.title || '' }, height: SheetSize.FIT_CONTENT, showClose: true })
      .id('chat.welcome')
  }
}
