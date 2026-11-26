import test from 'node:test';
import assert from 'node:assert/strict';
import { liftedMessageWords } from '../entry/src/main/ets/core/Markdown.ts';
import { placeMessageLift } from '../entry/src/main/ets/core/MessageLift.ts';

test('a lifted selection copies visible reply text and preserves the Markdown table and source of user prompts',()=>{
  const table='| Code | Result |\n| --- | --- |\n| 42 | Done |';
  const input='Use **the cache** and [the guide](https://example.com).\n\n'+table;
  assert.equal(liftedMessageWords(input,false),'Use the cache and the guide.\n\n'+table);
  assert.equal(liftedMessageWords(input,true),input);
  assert.equal(liftedMessageWords('- First\n- Second',false),'• First\n• Second');
});

test('the lifted message holds its position when the native action bar fits and keeps a tall selection reachable',()=>{
  const source={left:8,top:150,width:386,height:118,press:52,fontScale:1};
  assert.deepEqual(placeMessageLift(source,62,840,62),{top:150,height:118,offset:0,barTop:276});
  const lower=placeMessageLift({...source,top:670},62,840,62);assert.equal(lower.top,670);assert.equal(lower.barTop,600);
  const long=placeMessageLift({...source,top:-420,height:1500,press:865},62,840,62);
  assert.equal(long.height,692);assert.ok(long.offset>0);assert.ok(long.top>=70);assert.ok(long.barTop+62<=832);
});
