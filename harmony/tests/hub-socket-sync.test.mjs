import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith('@kit.')) return { url: 'hub-socket-test:platform', shortCircuit: true };
    if (context.parentURL?.endsWith('/HubTransport.ets') && specifier.startsWith('../core/'))
      return { url: new URL(specifier + '.ts', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'hub-socket-test:platform') return { format:'module', shortCircuit:true, source:`
      export const bundleManager={}, deviceInfo={}, i18n={}, hilog={error(){}}, cryptoFramework={}, cert={}, connection={}, socket={}, rcp={};
      export const url={URL:{parseURL:s=>new URL(s)}};
      export const util={TextDecoder:{create:(_name,options)=>{const d=new TextDecoder('utf-8',options);return {decodeToString:b=>d.decode(b)}}}};
    ` };
    if (url.endsWith('/HubTransport.ets')) return { format:'module', shortCircuit:true,
      source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8')) };
    return next(url, context);
  }
});
const { HubTransport, EndpointConfig } = await import('../entry/src/main/ets/platform/HubTransport.ets');
const accept = 's3pPLMBiTxaQ9kYGzzhZRbK+xOo=';
const upgrade = Buffer.from('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: '+accept+'\r\n\r\n');
function endpoint(path) {
  const t = new HubTransport(new EndpointConfig('https://example.invalid/','',''), path);
  t.socketTrusted = true; t.upgradeRequested = true; t.expectedAccept = accept;
  return t;
}
function frame(payload, opcode=1, fin=true) {
  const h = Buffer.alloc(payload.length < 126 ? 2 : payload.length < 65536 ? 4 : 10);
  h[0]=(fin?128:0)|opcode;
  if(h.length===2) h[1]=payload.length;
  else if(h.length===4) { h[1]=126; h.writeUInt16BE(payload.length,2); }
  else { h[1]=127; h.writeBigUInt64BE(BigInt(payload.length),2); }
  return Buffer.concat([h,payload]);
}
function receive(t,b) { t.receive(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength)); }

test('a complete legacy fleet snapshot larger than 5 MiB survives TLS chunk boundaries', () => {
  const t=endpoint('/api/v1/ws');
  const row={session_id:'example',machine_id:'machine',last_text:'x'.repeat(1024)};
  const payload=Buffer.from(JSON.stringify({type:'sessions',sessions:Array(5200).fill(row)}));
  assert.ok(payload.length>5*1024*1024);
  const counts=[];
  t.messageCallback=text=>counts.push(JSON.parse(text).sessions.length);
  const data=Buffer.concat([upgrade,frame(payload)]);
  for(let i=0;i<data.length;i+=16384) receive(t,data.subarray(i,i+16384));
  assert.deepEqual(counts,[5200]);assert.equal(t.closed,false);
});

test('several valid binary frames in one read are bounded individually and stay in order', () => {
  const t=endpoint('/api/v1/machines/example/ports/5900/connect'), got=[];
  t.binaryCallback=bytes=>got.push([bytes.byteLength,new Uint8Array(bytes)[0]]);
  const part=640*1024;
  receive(t,Buffer.concat([upgrade,frame(Buffer.alloc(part,3),2),frame(Buffer.alloc(part,5),2)]));
  assert.deepEqual(got,[[part,3],[part,5]]);assert.equal(t.closed,false);
});

test('a fragmented Hub event is delivered once, while stream-specific size limits retain their failure reason', () => {
  const t=endpoint('/api/v1/ws'), messages=[];
  t.messageCallback=text=>messages.push(JSON.parse(text).type);
  receive(t,Buffer.concat([upgrade,frame(Buffer.from('{"type":"ses'),1,false),frame(Buffer.from('sions_list"}'),0)]));
  assert.deepEqual(messages,['sessions_list']);
  const relay=endpoint('/api/v1/machines/example/ports/5900/connect');let fault;
  relay.stateCallback=(state,cause)=>{fault=[state,cause.message]};
  receive(relay,Buffer.concat([upgrade,frame(Buffer.alloc(1024*1024+1),2).subarray(0,10)]));
  assert.deepEqual(fault,['offline','frame_too_large']);
});
