import test from 'node:test';
import assert from 'node:assert/strict';
import * as transport from '../entry/src/main/ets/core/TransportPolicy.ts';
const {heldReceipt202, isAgentMessagePost, writeResultUnknown} = transport;

test('only a matching held Agent receipt can acknowledge a 202 session send', () => {
  const path = '/api/v1/sessions/my%20laptop/chat%2Fone/messages';
  const reply = {delivered:'later', undelivered:{id:'operation',state:'waiting',computer:'Desk'}};
  assert.ok(isAgentMessagePost('POST', path));
  assert.ok(!isAgentMessagePost('GET', path));
  assert.ok(!isAgentMessagePost('POST', path + '/retry'));
  assert.equal(heldReceipt202(JSON.stringify(reply),'operation'), 'held');
  for (const value of ['{}','bad',JSON.stringify({...reply,undelivered:{...reply.undelivered,id:'other'}}),JSON.stringify({state:'pending'}),
    JSON.stringify({...reply,undelivered:{...reply.undelivered,computer:''}})]) assert.equal(heldReceipt202(value,'operation'),'pending');
  assert.equal(heldReceipt202('{"state":"unknown"}','operation'),'unknown');
});
test('a lost dispatched write stays unknown even after closing the connection', () => {
 for (const status of [0,202,302,408,429,500,502,503,504]) assert.equal(writeResultUnknown(status),true);
});
test('only explicit refusal evidence permits a new operation', () => {
 for (const status of [400,401,403,404,405,409,410,413,415,422]) assert.equal(writeResultUnknown(status),false);
 assert.equal(writeResultUnknown(503,'{"delivered":"none"}'),false);
});
test('unknown or pending receipt evidence takes precedence over HTTP status', () => {
 for (const status of [200,400,409,503]) {
  assert.equal(writeResultUnknown(status,'{"state":"unknown"}'),true);
  assert.equal(writeResultUnknown(status,'{"state":"pending","delivered":"none"}'),true);
  assert.equal(writeResultUnknown(status,'{"delivered":"unknown"}'),true);
  assert.equal(writeResultUnknown(status,'{"delivered":"yes"}'),true);
 }
});

test('only a typed refusal before a Git pull is definitive on a gateway error',()=>{
  const body=JSON.stringify({code:'uncommitted_changes',error:'Dirty working tree'});
  assert.equal(writeResultUnknown(502,body,true),false);
  assert.equal(writeResultUnknown(502,body),true);
  assert.equal(writeResultUnknown(502,'{"code":"uncommitted_changes","delivered":"unknown"}',true),true);
  assert.equal(writeResultUnknown(502,'{"code":"pull_failed"}',true),true);
  assert.equal(writeResultUnknown(502,'{"error":"upstream timeout"}',true),true);
});

test('only the known mutation routes expose bounded public Hub feedback',()=>{
  const message='Close the approval dialog first.';
  const body=JSON.stringify({error:' '+message+' ',code:'dialog_open',delivered:'none'});
  for(const path of ['/api/v1/todos/a%2Fb/accept','/api/v1/later/a%2Fb/handled?manager=work%2Fa']) {
    assert.equal(transport.publicMutationError('POST',path,423,body),message);
    assert.equal(transport.publicMutationError('GET',path,423,body),'');
    assert.equal(transport.publicMutationError('POST',path,500,body),'');
  }
  for(const path of ['/api/v1/todos/a/accept/extra','/api/v1/later/a/handled?manager=x&other=y','/api/v1/todos/',
    '/api/v1/sessions/m/s/messages','/api/v1/later/a/handled?manager=x#other']) {
    assert.equal(transport.publicMutationError('POST',path,423,body),'');
  }
  const direction='/api/v1/assistant/sessions/m/s/direction';
  assert.equal(transport.publicMutationError('PUT',direction,409,body),message);
  assert.equal(transport.publicMutationError('PUT',direction,502,body),'');
  for(const invalid of ['null','{}','invalid JSON','{"error":{}}',' '.repeat(65536)]) {
    assert.equal(transport.publicMutationError('PUT',direction,409,invalid),'');
  }
  assert.equal(transport.publicMutationError('PUT',direction,409,JSON.stringify({error:'a'.repeat(800)})).length,512);
});
