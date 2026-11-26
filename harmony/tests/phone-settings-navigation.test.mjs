import test from 'node:test';
import assert from 'node:assert/strict';
import { phoneAppearancePage, phoneAppearancePages, phoneSettingsLink, pushPhoneSetting, phoneSettingsRows,phoneSettingsSearchCatalog,phoneHubUpdateMessage } from '../entry/src/main/ets/core/PhoneSettingsNavigation.ts';

test('the core has at most ten real routes and deep searches bypass Advanced while ordinary Back keeps it',()=>{
  const groups=['assistant','device','hub'], modern=['inbox_sources','extension_manager'];
  const core=groups.flatMap(g=>phoneSettingsRows(false,g,modern,true)),advanced=groups.flatMap(g=>phoneSettingsRows(true,g,modern,true));
  assert.deepEqual(core,['models','assistant','channels','connectors','phone_inbox_sources','extensions','machines','notifications','about','phone_hubs']);
  assert.ok(core.every(id=>!advanced.includes(id)));assert.ok([...core,...advanced].every(id=>phoneSettingsSearchCatalog.includes(id)));
  assert.ok(!phoneSettingsRows(false,'assistant',[],true).includes('extensions'));
  assert.ok(phoneSettingsRows(false,'assistant',['plugin_manager'],true).includes('extensions'));
  assert.deepEqual(phoneSettingsRows(false,'hub',modern,false),[]);assert.ok(!phoneSettingsRows(true,'hub',modern,false).includes('managers'));
  const trail=pushPhoneSetting(pushPhoneSetting([],'phone_settings_advanced'),'phone_agent_options');
  assert.deepEqual(trail.slice(0,-1),['phone_settings_advanced']);assert.deepEqual(pushPhoneSetting([],'phone_agent_options').slice(0,-1),[]);
});

test('the phone pops each appearance child to its real origin', () => {
  const directory = [];
  const appearance = pushPhoneSetting(directory, 'appearance');
  for (const child of phoneAppearancePages) {
    const fromHome = pushPhoneSetting(appearance, child);
    assert.deepEqual(fromHome, ['appearance', child]);
    assert.deepEqual(fromHome.slice(0, -1), appearance);
    assert.deepEqual(pushPhoneSetting(directory, child).slice(0, -1), directory);
    assert.equal(pushPhoneSetting(fromHome, child), fromHome);
  }
  assert.equal(phoneSettingsLink('phone_agent_layout'), 'phone_look_layout');
  assert.equal(phoneAppearancePage('phone_look_avatar'), true);
  assert.equal(phoneSettingsLink('models'), '');
  assert.deepEqual(appearance, ['appearance']);
});

test('Advanced only advertises the actual latest release or an update already in progress',()=>{
  assert.equal(phoneHubUpdateMessage('idle',false,'release'),'');
  assert.equal(phoneHubUpdateMessage('checking',false,'release'),'');
  assert.equal(phoneHubUpdateMessage('confirming',false,'docker'),'admin_hub_state_confirming');
  assert.equal(phoneHubUpdateMessage('available',true,'docker'),'phone_hub_new_image');
  assert.equal(phoneHubUpdateMessage('idle',true,'release'),'phone_hub_update_available');
});

test('Live Screen requires an advertised machine on Advanced and stays a directly searchable device setting',()=>{
 assert.ok(!phoneSettingsRows(true,'device',[],true,false).includes('phone_live_screen'));
 assert.ok(phoneSettingsRows(true,'device',[],true,true).includes('phone_live_screen'));
 assert.ok(!phoneSettingsRows(false,'device',[],true,true).includes('phone_live_screen'));
 assert.ok(phoneSettingsSearchCatalog.includes('phone_live_screen'));
 assert.deepEqual(pushPhoneSetting([],'phone_live_screen'),['phone_live_screen']);
});
