import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { phoneSettingsSearchCatalog } from '../entry/src/main/ets/core/PhoneSettingsNavigation.ts';

const source = readFileSync(new URL('../entry/src/main/ets/components/SettingsPage.ets', import.meta.url), 'utf8');
const directory = readFileSync(new URL('../entry/src/main/ets/components/SettingsDirectory.ets', import.meta.url), 'utf8');

test('the phone searches nested settings and restores exactly the query used to open a result', () => {
  const catalog = directory.slice(directory.indexOf('const AGENT_OPTIONS:'), directory.indexOf('function languageName('));
  const labels = directory.slice(directory.indexOf('function settingLabel('), directory.indexOf('@ComponentV2'));
  const appear = directory.slice(directory.indexOf('  aboutToAppear():'), directory.indexOf('  aboutToDisappear():'));
  const lookup = directory.slice(directory.indexOf('  @Computed get results()'), directory.indexOf('  @Builder managerMenu('))
    .replace('@Computed ', '');
  const strings = new Map(JSON.parse(readFileSync(new URL('../entry/src/main/resources/zh_CN/element/string.json', import.meta.url))).string
    .map(entry => [entry.name, entry.value]));
  const Search = new Function('tr','phoneSettingsSearchCatalog','canIUse',stripTypeScriptTypes(catalog + labels + ' class Search {' + appear + lookup + '}')+';return Search')(key => strings.get(key) || key,phoneSettingsSearchCatalog,()=>true);
  const page = new Search(), actions = [];
  Object.assign(page,{ initialQuery:' 深色  主题 ', label:id=>id, subtitle:()=>'', meeting:{open(){}}, readHubStatus(){}, field:{ stopEditing:()=>actions.push('dismiss') }, onOpen:id=>actions.push(id) });
  page.aboutToAppear();
  assert.deepEqual(page.results,['appearance']);
  page.openEntry(page.results[0]);
  assert.deepEqual(actions,['dismiss','appearance']);
  assert.equal(page.query,' 深色  主题 ');
  page.query='LaUnChErS'; assert.ok(page.results.includes('launchers'));
  page.query=' '; assert.deepEqual(page.results,[]);
  for(const [text,target] of [['模型','models'],['助手资料','assistant'],['知识库选择','phone_knowledge_selection'],
    ['会议','phone_meeting_options'],['语言与听写','phone_language_options'],['外观与显示','appearance']]) {
    page.query=text; assert.ok(page.results.includes(target),text);
  }
});

function method(name) {
  const start = source.indexOf('  private ' + name + '(');
  assert.ok(start >= 0, name);
  const body = source.indexOf('{', start); let depth = 1, quote = '', escape = false, end = body + 1;
  for (; end < source.length && depth; end++) {
    const ch = source[end];
    if (quote) { if (escape) escape = false; else if (ch === '\\') escape = true; else if (ch === quote) quote = ''; }
    else if (ch === '"' || ch === "'" || ch === '`') quote = ch;
    else if (ch === '{') depth++;
    else if (ch === '}') depth--;
  }
  assert.equal(depth, 0, name); return source.slice(start, end);
}
const NativeSettings = new Function('Wire', 'tr', 'return ' + stripTypeScriptTypes('class NativeSettings {' +
  ['confirmExtensionSave', 'confirmExtensionRemove', 'confirmConnectorLogout', 'confirmChannelUnbind'].map(method).join('\n') + '}'))(
  { text: (row, key, fallback = '') => row[key] || fallback }, value => value);
function fixture() {
  const page = new NativeSettings(); let generation = 1, dialog, writes = 0, backs = 0;
  page.machineRevision = 2; page.mounted = true; page.active = true;
  page.model = { scope: () => 'hub/personal', profile: { manager: 'personal' }, connectionGeneration: () => generation };
  page.management = { selectedId: 'target', extensionDetail: { name: 'Target' }, operation: {}, channelData: () => ({ name: 'Target' }),
    saveExtensionConfig: async () => { writes++; return true; }, removeExtension: async () => { writes++; return true; },
    connectorLogout: async () => { writes++; return true; }, channelUnbind: async () => { writes++; return true; } };
  page.selectedConnector = () => ({ id: 'target', title: 'Target' });
  page.getUIContext = () => ({ showAlertDialog: value => { dialog = value; } }); page.onBack = () => { backs++; };
  return { page, confirm: () => dialog.secondaryButton.action(), reconnect: () => generation++, writes: () => writes, backs: () => backs };
}
for (const name of ['confirmExtensionSave', 'confirmExtensionRemove', 'confirmConnectorLogout', 'confirmChannelUnbind']) {
  test(name + ' rejects a dialog from a previous connection or page lifetime', () => {
    for (const change of ['reconnect', 'hide-and-return', 'replace-owner']) {
      const f = fixture(); f.page[name]();
      if (change === 'reconnect') f.reconnect();
      if (change === 'hide-and-return') f.page.machineRevision++;
      if (change === 'replace-owner') f.page.model = { ...f.page.model };
      f.confirm(); assert.equal(f.writes(), 0, change);
    }
  });
  test(name + ' admits its unchanged visible confirmation', async () => {
    const f = fixture(); f.page[name](); f.confirm(); await Promise.resolve(); assert.equal(f.writes(), 1);
    assert.equal(f.backs(), name === 'confirmExtensionRemove' ? 1 : 0);
  });
}


test('a late removal ACK cannot pop a reentered native detail page', async () => {
  const f = fixture(); let finish;
  f.page.management.removeExtension = () => new Promise(resolve => { finish = resolve; });
  f.page.confirmExtensionRemove(); f.confirm(); f.page.machineRevision++;
  finish(true); await Promise.resolve(); assert.equal(f.backs(), 0);
});


test('core Hub owns its explanatory footer before the separate Advanced card in each published language',()=>{
  const build=directory.slice(directory.indexOf('  build() {'),directory.indexOf('export struct PhoneSettingsOptions'));
  const hub=build.indexOf("this.section(tr('phone_settings_hub')");
  const common=build.indexOf("'phone_settings_common_groups_footer'");
  const advanced=build.indexOf("this.section('',['phone_settings_advanced'])");
  const advancedFooter=build.indexOf("'phone_advanced_footer'");
  assert.ok(hub>=0 && common>hub && advanced>common && advancedFooter>advanced);
  const published={'base':'These two groups are the same whichever assistant is current.','en_US':'These two groups are the same whichever assistant is current.',
    'zh_CN':'这两组和当前是哪个助手无关。','ja_JP':'この 2 つのグループは、どのアシスタントを選んでいても同じです。'};
  for(const [locale,expected] of Object.entries(published)){
    const strings=JSON.parse(readFileSync(new URL('../entry/src/main/resources/'+locale+'/element/string.json',import.meta.url),'utf8')).string;
    assert.equal(strings.find(s=>s.name==='phone_settings_hub')?.value,'Hub');
    assert.equal(strings.find(s=>s.name==='phone_settings_common_groups_footer')?.value,expected);
  }
});
