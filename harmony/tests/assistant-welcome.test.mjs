import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';

const source=readFileSync(new URL('../entry/src/main/ets/components/AssistantWelcome.ets',import.meta.url),'utf8');
const wire={
  rows:(value,key)=>Array.isArray(value[key])?value[key]:[],
  text:(value,key)=>typeof value[key]==='string'?value[key]:'',
  strings:(value,key)=>Array.isArray(value[key])?value[key].filter(item=>typeof item==='string'):[],
  bool:(value,key)=>value[key]===true
};
const presentation=source.slice(source.indexOf('class WelcomeSuggestion'), source.indexOf('@ComponentV2'));
const controller=source.slice(source.indexOf('  private targetKey()'), source.indexOf('  @Builder question('));
const {questions,View}=new Function('Wire','tr',stripTypeScriptTypes(presentation+'\nclass View {\n'+controller+'\n}\n')+
  'return {questions:readinessQuestions,View};')(wire,key=>key);

test('setup-only Hub questions open help locally; full-mode phone questions notify and use the normal composer', async()=>{
 const here={profile:'home',target:'main',generation:1},calls=[],sent=[],notices=[];
 const setup={suggestions:[
   {id:'what_can_you_do',title:'How does Persona work?',local:true},
   {id:'how_to_connect_model',title:'Connect a model',local:true},
   {id:'how_to_connect_model',title:'Duplicate',local:true}
 ]};
 const view=new View();view.revision=0;view.canAsk=true;view.onAsk=prompt=>sent.push(prompt);
 view.model={connected:true,features:['assistant_readiness'],scope:()=>here.profile,target:()=>here.target,
   connectionGeneration:()=>here.generation,repo:()=>({call:async path=>{calls.push(path);return setup;}})};
 view.getUIContext=()=>({getPromptAction:()=>({showToast:notice=>notices.push(notice.message)})});
 await view.reload();assert.deepEqual(view.suggestions.map(question=>question.id),['what_can_you_do','how_to_connect_model']);
 view.choose(view.suggestions[1]);assert.equal(view.help.id,'how_to_connect_model');assert.equal(view.helpShown,true);
 assert.deepEqual(sent,[]);assert.deepEqual(calls,['/api/v1/assistant/readiness']);
 const old=view.suggestions[1];
 view.suggestions=questions({suggestions:[{id:'today',title:'My day',prompt:'Read my calendar',needs:['phone_calendar']}]});
 view.choose(old);view.canAsk=false;view.choose(view.suggestions[0]);assert.deepEqual(sent,[]);assert.deepEqual(notices,[]);
 view.canAsk=true;view.choose(view.suggestions[0]);assert.deepEqual(sent,['Read my calendar']);assert.deepEqual(notices,['phone_chat_phone_read']);
 here.generation++;view.choose(view.suggestions[0]);assert.deepEqual(sent,['Read my calendar']);
});

test('older Hubs have four native starter questions and stale readiness cannot retarget another conversation', async()=>{
 const view=new View();view.revision=0;
 let destination='chat-a',reads=0;
 const gate=Promise.withResolvers();
 view.model={connected:true,scope:()=> 'home',target:()=>destination,connectionGeneration:()=>1,features:['assistant_readiness'],
   repo:()=>({call:()=>{reads++;return gate.promise;}})};
 const opening=view.reload();
 destination='chat-b';view.model.features=[];await view.reload();
 assert.deepEqual(view.suggestions.map(question=>question.id),['agenda','mail','agents','lark']);
 gate.resolve({suggestions:[{id:'foreign',title:'Old chat'}]});await opening;
 assert.equal(reads,1);assert.ok(view.suggestions.every(question=>question.id!=='foreign'));
 assert.equal(questions({suggestions:[{id:'test',title:'A question'}]})[0].prompt,'A question');
 assert.match(source,/GridRow\(\{columns: this\.contentWidth >= 300 && this\.environment\.fontScale < 1.5 \? 2 : 1/);
});
