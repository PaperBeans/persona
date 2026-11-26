import test from 'node:test';
import assert from 'node:assert/strict';
import {clientLocale,initializeClientLanguage,setClientLanguage,supportedInterfaceLocale} from '../entry/src/main/ets/core/ClientLocale.ts';
test('the Match System option shows the supported language even while a different interface language is selected',()=>{
 setClientLanguage('ja-JP');
 assert.equal(supportedInterfaceLocale('zh_Hant_TW'),'zh-Hans');
 assert.equal(supportedInterfaceLocale('fr-CA'),'en');
 assert.equal(clientLocale('fr-CA'),'ja');
 setClientLanguage('default');
});
test('wire locale follows the committed UI selection independently of the system and resolves system fallback',()=>{
 for(const [choice,expected] of [['en-US','en'],['zh-CN','zh-Hans'],['ja-JP','ja']]){setClientLanguage(choice);for(const system of ['zh-CN','en-US','ja-JP','ko-KR'])assert.equal(clientLocale(system),expected);}
 setClientLanguage('default');for(const [system,expected]of [['zh-Hant-TW','zh-Hans'],['ja-JP','ja'],['en-GB','en'],['ko-KR','en']])assert.equal(clientLocale(system),expected);
});
test('Ability recreation cannot apply a pending cold-start language to this launch',()=>{
 initializeClientLanguage('en-US');assert.equal(clientLocale('zh-CN'),'en');initializeClientLanguage('default');assert.equal(clientLocale('zh-CN'),'en');setClientLanguage('default');
});
