import test from 'node:test';
import assert from 'node:assert/strict';
import { splitBackgroundRounds, activityFeedRound } from '../entry/src/main/ets/core/BackgroundActivity.ts';

const at = (seq) => new Date(Date.UTC(2026, 1, 1, 12, 0, seq)).toISOString();
const item = (seq, kind, role, fields = {}) => ({ id: 'item-' + seq, seq, ts: at(seq), kind, role, ...fields });
const input = (seq, fields = {}) => item(seq, 'text', 'user', {text: 'Read progress', injected: {state:'supervise',level:'triage'}, ...fields});
const command = (seq, name, id, fields = {}) => item(seq, 'tool_use', 'assistant', {tool_name:name, tool_use_id:id, ...fields});
const result = (seq, id, visibility, fields = {}) => item(seq, 'tool_result', 'user', {tool_use_id:id, text:JSON.stringify({visibility}), ...fields});
const first = input(1);
const finish = command(2, 'mcp__persona__supervision_finish', 'finish');
const receipt = result(3,'finish','silent');
const next = item(7,'text','user',{client_msg_id:'real',text:'My next request'});

test('only confirmed quiet rounds move out of the main conversation, including intervening recaps', () => {
  const local = item(4,'recap','system',{text:'recap'});
  const reply = item(5,'text','assistant',{text:'Finished',narration:true});
  const base = [first, finish, receipt, local, reply, next];
  const view = splitBackgroundRounds(base, false);
  assert.deepEqual(view.timeline,[next]);
  assert.deepEqual(view.rounds[0].items,base.slice(0,-1));
  assert.equal(activityFeedRound(view.rounds,'answered',Date.parse(at(1))),first.id);
  assert.equal(activityFeedRound(view.rounds,'routed',Date.parse(at(6))),first.id);
  assert.equal(activityFeedRound(view.rounds,'task',Date.parse(at(7))),'');
  for (const kind of ['notice','plan','message','brief']) assert.equal(activityFeedRound(view.rounds,kind,Date.parse(at(3))),'');
  assert.equal(splitBackgroundRounds(base.slice(0,-1),true).rounds.length,0);
});

test('pending, failed, promoted and unrelated provider receipts remain visible', () => {
  for (const rows of [
    [first,finish],
    [first,finish,result(3,'finish','silent',{is_error:true})],
    [first,command(2,'mcp__other__supervision_finish','finish'),receipt],
    [first,finish,result(3,'finish','user')],
    [first,finish,receipt,item(4,'fold','assistant',{fold:{through_seq:9,through_id:'x',supervision_user:true}})],
    [first,finish,receipt,command(4,'mcp__persona__ask_user','question'),result(5,'question','silent')],
    [first,finish,receipt,command(4,'task_review','review',{tool_input:{verdict:'needs_user'}}),result(5,'review','silent')]
  ]) {
    const view=splitBackgroundRounds(rows,false);
    assert.deepEqual(view.timeline,rows);
    assert.deepEqual(view.rounds,[]);
  }
});

test('server receipts may follow the next genuine input and personal forwarded text remains in Main', () => {
  const later=result(8,'finish','silent');
  const view=splitBackgroundRounds([first,finish,next,later],false);
  assert.deepEqual(view.rounds[0].items,[first,finish]);
  assert.deepEqual(view.timeline,[next,later]);
  const signedUser=input(1,{text:'<persona-context kind="message-source">{"author":"user","kind":"conversation"}</persona-context>My note'});
  assert.equal(splitBackgroundRounds([signedUser,finish,receipt],false).rounds.length,0);
  const unrelated=item(1,'text','user',{client_msg_id:'mine',text:'📋 督办：你派出去的任务有 2 个要你处理\n这是 Persona 的督办消息'});
  assert.equal(splitBackgroundRounds([unrelated,finish,receipt],false).rounds.length,0);
  const source=item(1,'text','user',{source:{kind:'followup',title:'Quarterly review'}});
  assert.equal(splitBackgroundRounds([source,finish,receipt],false).rounds.length,1);
  assert.equal(splitBackgroundRounds([{...source,injected:{state:'limit_continue'}},finish,receipt],false).rounds.length,0);
});
