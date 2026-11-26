import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';

const source=readFileSync(new URL('../entry/src/main/ets/models/AgentReadingHeader.ets',import.meta.url),'utf8');
const model=stripTypeScriptTypes(source.replace('@ObservedV2','').replaceAll('@Trace ','').replace('export class','class'));
function fixture(){
  let sequence=0,covered=false;
  const pending=new Map(),animations=[];
  const kick=(cb,delay)=>{pending.set(++sequence,{cb,delay});return sequence;};
  const State=new Function('setTimeout','clearTimeout',model+';return AgentReadingHeader')(kick,id=>pending.delete(id));
  const state=new State();
  const bind=owner=>state.attach(owner,()=>covered,v=>{animations.push(v);state.collapse=v;});
  function tick(delay){
    const first=[...pending.entries()].find(([,timer])=>timer.delay===delay);assert.ok(first,'expected idle timer '+delay);
    pending.delete(first[0]);first[1].cb();
  }
  bind('chat-a');state.measure('chat-a',94);
  return {state,pending,animations,bind,tick,cover:v=>covered=v};
}
test('the Agent header follows the reader toward the latest reply, opens toward history, and settles on the nearer size',()=>{
  const {state:s}=fixture();
  s.scroll('chat-a',26,true);assert.equal(s.collapse,0,'a programmatic glide is not a reader gesture');
  s.scroll('chat-a',24,false);assert.equal(s.collapse,24);
  s.settle('chat-a',false);assert.equal(s.collapse,0);
  s.scroll('chat-a',19,false);s.scroll('chat-a',16,true);s.settle('chat-a',false);assert.equal(s.collapse,54);
  s.scroll('chat-a',-48,false);s.settle('chat-a',false);assert.equal(s.collapse,0);
  s.scroll('chat-a',1,false);s.settle('chat-a',true);assert.equal(s.collapse,54);
  s.reveal('chat-a');assert.equal(s.collapse,0);
  s.jumped('chat-a');assert.equal(s.collapse,54);
});
test('three seconds of inactivity fold the active header only while its title, tabs and chat are free',()=>{
  const {state:s,cover,tick,pending}=fixture();
  s.overlay('chat-a','info',true);tick(3000);assert.equal(s.collapse,0);
  s.overlay('chat-a','info',false);cover(true);tick(3000);assert.equal(s.collapse,0);
  s.interacted('chat-a');cover(false);tick(3000);assert.equal(s.collapse,54);
  s.reveal('chat-a');const previous=[...pending.values()][0].cb;
  s.detach('chat-a');assert.equal(pending.size,0);previous();assert.equal(s.collapse,0);
  s.jumped('chat-a');assert.equal(s.collapse,0);
});
test('changing sessions retires the finger motion and timer, and larger full headers keep their fold fraction',()=>{
  const {state:s,bind,pending}=fixture();const fromA=[...pending.values()][0].cb;
  s.scroll('chat-a',27,false);s.measure('chat-a',120);assert.equal(s.collapse,40);
  bind('chat-b');s.measure('chat-b',130);s.scroll('chat-a',90,false);s.settle('chat-a',true);s.reveal('chat-a');fromA();
  assert.equal(s.collapse,0);s.scroll('chat-b',10,true);assert.equal(s.collapse,0);
  s.overlay('chat-b','siblings',true);s.scroll('chat-b',90,false);assert.equal(s.collapse,0);
  s.detach('chat-a');assert.equal(s.owns('chat-b'),true);
});
test('only the active developer session publishes manual transcript motion to the measured header',()=>{
  const conv=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
  const method=conv.match(/^  private trackScrollMotion\([\s\S]+?^  \}/m)[0];
  const sources={DRAG:0,FLING:1,EDGE_EFFECT:2,OTHER_USER_INPUT:3,SCROLL_BAR:4,SCROLL_BAR_FLING:5,SCROLLER:6,SCROLLER_ANIMATION:7};
  const View=new Function('ScrollSource','return '+stripTypeScriptTypes('class View {'+method+'}'))(sources);
  const {state}=fixture(),v=Object.assign(new View(),{readingHeader:state,headerOwner:'chat-a',active:true,admission:()=>true,readingMotion:0});
  for(const k of ['SCROLLER','SCROLLER_ANIMATION','EDGE_EFFECT','FLING'])v.trackScrollMotion(20,sources[k]);assert.equal(state.collapse,0);
  v.trackScrollMotion(20,sources.DRAG);assert.equal(state.collapse,20);
  v.readingRestoring=true;v.trackScrollMotion(18,sources.DRAG);assert.equal(state.collapse,20);
  v.readingRestoring=false;v.active=false;v.trackScrollMotion(18,sources.DRAG);assert.equal(state.collapse,20);
});
