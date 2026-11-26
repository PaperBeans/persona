import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';
registerHooks({load(url,context,next){if(url.endsWith('/Wire.ets'))return{format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''))};return next(url,context);}});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const source=readFileSync(new URL('../entry/src/main/ets/components/SettingsPage.ets',import.meta.url),'utf8');
function method(name){const start=source.search(new RegExp('^  private (?:async )?'+name+'\\(','m'));assert.ok(start>=0,name);const body=source.indexOf('{',start);let depth=1,quote='',escape=false,index=body+1;for(;index<source.length&&depth;index++){const c=source[index];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}return source.slice(start,index);}
const methods=['chooseMachine','machineRow','screenMachine','canOpenScreen','clearMachineChoice','sameRoutedMachine','machineOptions','loadMachineDirectory'];
const Page=new Function('Wire','ResourceRow','return '+stripTypeScriptTypes('class Page{'+methods.map(method).join('\n')+'}'))((await import('../entry/src/main/ets/models/Wire.ets')).Wire,ResourceRow);
function deferred(){let resolve;return{promise:new Promise(r=>resolve=r),resolve:value=>resolve(value)};}
function fixture(){const page=new Page(),calls=[];let generation=1;
 const machine=(group)=>{const row=ResourceRow.from({id:'same-mac',manager:'work',online:true,trust:'confirmed',os:'mac'},'machine');row.group=group;return row;};
 const a=machine('hub-a'),b=machine('hub-b');const owner=row=>({connected:true,demo:false,machines:[row]});const owners={a:owner(a),b:owner(b)};
 page.model={demo:false,connectionGeneration:()=>generation,scope:()=> 'root',owns:(g,s)=>g===generation&&s==='root',viewFor:async row=>row.group==='hub-b'?owners.b:owners.a};
 page.management={loading:false,rows:[],setAdmission(admit){this.admit=admit;},async load(_page,owner){calls.push(owner);this.rows=owner.machines.map(row=>({id:row.id,raw:row.data()}));}};page.mounted=true;page.active=true;page.machineRevision=0;
 return{page,calls,a,b,owners,bump:()=>generation++};}
test('machine details and screen eligibility use the selected Hub despite duplicate machine ids',async()=>{const f=fixture();await f.page.chooseMachine(f.b);assert.equal(f.page.machineOwner,f.owners.b);assert.equal(f.calls[0],f.owners.b);assert.equal(f.page.screenMachine().group,'hub-b');assert.equal(f.page.canOpenScreen(),true);f.owners.b.connected=false;assert.equal(f.page.canOpenScreen(),false);});
test('a late owner lookup cannot replace the current machine or revive a hidden page',async()=>{const f=fixture(),gate=deferred();f.page.model.viewFor=row=>row.group==='hub-a'?gate.promise:Promise.resolve(f.owners.b);const old=f.page.chooseMachine(f.a);await f.page.chooseMachine(f.b);gate.resolve(f.owners.a);await old;assert.equal(f.page.machineOwner,f.owners.b);assert.deepEqual(f.calls,[f.owners.b]);const hidden=deferred();f.page.model.viewFor=()=>hidden.promise;const next=f.page.chooseMachine(f.a);f.page.active=false;hidden.resolve(f.owners.a);await next;assert.deepEqual(f.calls,[f.owners.b]);});

test('choosing an unavailable Hub clears the former machine and its pair code before lookup',async()=>{const f=fixture();await f.page.chooseMachine(f.a);f.page.management.pairing={pair_url:'synthetic-owner-a-code',machine:f.a.id};f.page.pairingOwnerScope='hub-a';f.page.computerName='Old draft';f.page.model.viewFor=async()=>{throw Error('offline');};await f.page.chooseMachine(f.b);assert.equal(f.page.machineOwner,undefined);assert.equal(f.page.screenMachine(),undefined);assert.equal(f.page.canOpenScreen(),false);assert.equal(f.page.management.selectedId,'');assert.deepEqual(f.page.management.pairing,{});assert.equal(f.page.computerName,'');assert.equal(f.page.pairingOwnerScope,'');});

test('management starts at the routed machine across equal IDs and clears stale actions when that specific machine disappears',async()=>{
  const f=fixture(), page=f.page;
  Object.assign(page.model,{profile:{id:'hub-a',manager:'work'},directoryReady:true,directoryMachines:[f.a,f.b],
    selected:f.a,refreshDirectory:async()=>{}});
  page.page='machines';page.initialMachine=f.b;
  await page.loadMachineDirectory();
  assert.equal(page.machineOwner,f.owners.b);assert.equal(page.machineDirectoryKey,f.b.key());assert.equal(page.screenMachine(),f.b);
  page.management.pairing={pair_url:'fixture-code'};page.model.directoryMachines=[f.a];await page.loadMachineDirectory();
  assert.equal(page.management.error,'admin_machine_unavailable');assert.equal(page.machineOwner,undefined);
  assert.equal(page.screenMachine(),undefined);assert.deepEqual(page.management.pairing,{});assert.equal(page.management.admit(),false);
  assert.deepEqual(f.calls,[f.owners.b]);
});

test('when the route changes during lookup, only its latest bound machine can become active',async()=>{
  const f=fixture(), page=f.page, gate=deferred();let request=0;
  Object.assign(page.model,{profile:{id:'hub-a',manager:'work'},directoryReady:true,directoryMachines:[f.a,f.b],selected:f.a,
    refreshDirectory:()=>++request===1?gate.promise:Promise.resolve()});
  page.page='machines';page.initialMachine=f.a;
  const first=page.loadMachineDirectory();page.initialMachine=f.b;await page.loadMachineDirectory();gate.resolve();await first;
  assert.equal(page.machineDirectoryKey,f.b.key());assert.equal(page.machineOwner,f.owners.b);
  assert.deepEqual(f.calls,[f.owners.b]);
});

test('an owner-local row without a group opens its actual computer, and a mismatched manager is never rebound',async()=>{
  const f=fixture(), page=f.page;f.owners.b.profile={id:'hub-b',manager:'work'};
  await page.chooseMachine(f.b);
  const local=ResourceRow.from({id:f.b.id,manager:'work',os:'darwin',online:true},'machine');
  f.owners.b.machines=[local];
  const result=page.screenMachine();assert.equal(result.group,'hub-b');assert.equal(result.manager,'work');assert.equal(page.canOpenScreen(),true);
  f.owners.b.profile.manager='different';assert.equal(page.screenMachine(),undefined);assert.equal(page.canOpenScreen(),false);
});

test('the general machine manager remembers an explicit dropdown choice before the previously selected resource',async()=>{
  const f=fixture(), page=f.page;
  Object.assign(page.model,{profile:{id:'hub-a',manager:'work'},directoryReady:true,directoryMachines:[f.a,f.b],selected:f.a,
    refreshDirectory:async()=>{}});
  page.page='machines';await page.loadMachineDirectory();await page.chooseMachine(f.b);await page.loadMachineDirectory();
  assert.equal(page.machineOwner,f.owners.b);assert.equal(page.machineDirectoryKey,f.b.key());
});
