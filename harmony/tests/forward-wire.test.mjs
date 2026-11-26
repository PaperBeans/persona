import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {wireForwardBlock,wireContextBody,forwardPrefix,forwardLimit,forwardMaximum,forwardURL,forwardSendNote,forwardGroups} from '../entry/src/main/ets/core/ChatForwardWire.ts';
const fixture=name=>JSON.parse(readFileSync(new URL('../../spec/fixtures/forward/'+name,import.meta.url),'utf8'));
test('forward blocks use the cross-client wire fixtures exactly and ignore device-local media handles',()=>{
 for(const c of fixture('cases.json').cases){
  if(c.block){const b=c.block;assert.equal(wireForwardBlock({kind:'forward',title:b.title,url:b.url,msg:b.msg,...b.attributes,text:b.text,local_images:['private'],local_transfer:'test'},'persona-context'),c.xml);}
  else{const r=forwardPrefix(c.message),p=r.entries[0];assert.equal(r.rest,c.expected.rest);assert.equal(p.title,c.expected.title);assert.equal(p.text,c.expected.text);assert.equal(p.custom,'kept');}
 }
 assert.equal(wireContextBody('x< / PERSONA-CONTEXT >y'),'xy');
});
test('per-message limits, encoded deep links and empty-note fallbacks follow one and several forwards',()=>{
 assert.equal(forwardMaximum,30);assert.equal(forwardLimit(1),20000);assert.equal(forwardLimit(4),15000);assert.equal(forwardLimit(30),2000);
 assert.equal(forwardURL('m','s','id#12'),'persona://session/m/s?at=id%2312');
 assert.equal(forwardSendNote('Personal note',2,'one','many'),'Personal note');
 assert.equal(forwardSendNote('',0,'one','many'),'');assert.equal(forwardSendNote('',2,'one','many'),'many');
});

test('the shared several-message envelope and adjacent-card groups preserve the wire order for every fixture',()=>{
 const {budget,write,read}=fixture('several.json');
 for(const sample of budget.cases)assert.equal(forwardLimit(sample.count),sample.limit);
 for(const sample of write){
  const xml=sample.blocks.map(b=>wireForwardBlock({kind:'forward',title:b.title,url:b.url,msg:b.msg,...b.attributes,text:b.text},'persona-context')).join('\n')+'\n\n'+sample.note;
  assert.equal(xml,sample.message);
 }
 for(const sample of read){
  const decoded=forwardPrefix(sample.message);
  assert.equal(decoded.rest,sample.rest);assert.deepEqual(decoded.entries.map(r=>r.msg),sample.msgs);
  assert.deepEqual(forwardGroups(decoded.entries).map(group=>group.map(r=>decoded.entries.indexOf(r))),sample.groups);
 }
});
