import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks, stripTypeScriptTypes} from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.endsWith('/Wire') && context.parentURL?.endsWith('/MachineDashboard.ets')) {
      return {url:new URL(specifier+'.ets',context.parentURL).href,shortCircuit:true};
    }
    return next(specifier,context);
  },
  load(url, context, next) {
    if (!url.endsWith('/MachineDashboard.ets') && !url.endsWith('/Wire.ets')) return next(url,context);
    const raw=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(raw),shortCircuit:true};
  }
});
const {ResourceRow}=await import('../entry/src/main/ets/models/Wire.ets');
const {MachineReading,machineKey,machineAgents,machineAgentTimeBeside,machineAlerts,machineConcern,machineTrace,
  machineBytes,machinePercent,machineTokens,machineTokenTotal,machineAvatarSymbol, osFamily}=await import('../entry/src/main/ets/models/MachineDashboard.ets');

test('the dashboard orders concerns exactly as the iPhone and keeps the Hub order for equal priorities',()=>{
  const readings=new MachineReading({system:{cores:4}, latest:{cpu:90,mem_used:80,mem_total:100,disk_used:95,disk_total:100,load:4}});
  const alerts=machineAlerts(readings,readings.system);
  assert.deepEqual(alerts.map(a=>[a.key,a.severity]),[
    ['machine_high_cpu',2],['machine_full_disk',2],['machine_low_memory',1],['machine_high_load',1]
  ]);
  const normal=new MachineReading({latest:{cpu:74.9,mem_total:0,mem_used:999,load:90}});
  assert.deepEqual(machineAlerts(normal,{}),[]);
  const order=[
    ['normal',machineConcern({},true,[],0)],['warn-a',machineConcern({},true,[{severity:1}],0)],
    ['mismatch',machineConcern({node_mismatch:true},true,[],0)],['offline',machineConcern({},false,alerts,1)],
    ['asking',machineConcern({},true,[],1)],['pending',machineConcern({trust:'pending'},false,[],0)],
    ['critical',machineConcern({},true,alerts,1)],['warn-b',machineConcern({},true,[{severity:1}],0)]
  ];
  assert.deepEqual(order.sort((a,b)=>a[1]-b[1]).map(a=>a[0]),
    ['pending','offline','critical','warn-a','warn-b','asking','mismatch','normal']);
});

test('agent totals and detail rows fold only matching parents, preserve waiting subtasks, and demote background-only activity for counts',()=>{
  function row(id,state,more={}) { return ResourceRow.from({session_id:id,machine_id:'mac',status:'idle',state,...more},'session'); }
  const agents=machineAgents([
    row('idle','idle'),row('inflight','working'),row('p','idle',{children:{needs_you:1}}),
    row('folded','working',{folded_under:'p'}),row('local-bg','working',{background_waiting:true}),
    row('other-child','working',{source_kind:'runtime',is_subagent:true,parent_session_id:'else'}),
    row('needs-help','needs_you',{source_kind:'runtime',is_subagent:true,parent_session_id:'else'}),
    row('orphan','working',{folded_under:'missing'}),row('own','working',{assistant:true}),
    row('gone','idle',{status:'offline'}),row('unread','unread',{children:{working:2}}),
    row('with-working-child','idle',{children:{working:1}})
  ]);
  assert.deepEqual(agents.map(a=>a.row.id),['p','needs-help','inflight','local-bg','orphan','with-working-child','idle','unread']);
  assert.equal(agents.find(a=>a.row.id==='local-bg').counted,'idle');
  assert.equal(agents.find(a=>a.row.id==='with-working-child').counted,'working');
  assert.equal(agents.find(a=>a.row.id==='unread').counted,'unread');
});

test('machine session rows keep short text beside the time and move a wide time below long text',()=>{
  assert.equal(machineAgentTimeBeside(265,380,47),true);
  assert.equal(machineAgentTimeBeside(255,380,77),false);
  assert.equal(machineAgentTimeBeside(255,70,77),true);
  assert.equal(machineAgentTimeBeside(116,160,0),true);
});

test('a machine session shows its live waiting reason and task without repeating an ordinary working state',()=>{
  const agent=(state,extra)=>machineAgents([ResourceRow.from({session_id:'agent',machine_id:'computer',state,
    agent:'claude',slot_name:'reports',last_text:'Latest result',...extra},'session')])[0];
  const waiting=agent('needs_you',{attention_reason:'Choose a report',todo:{title:'Prepare report'}});
  assert.deepEqual([waiting.note,waiting.stateLabel,waiting.context,waiting.todoTitle],['Choose a report','needs_you','Claude · reports','Prepare report']);
  assert.equal(agent('working',{attention_reason:'Earlier question'}).note,'Latest result');
  assert.equal(agent('working',{}).stateLabel,'');
  assert.equal(agent('idle',{awaits_reply:true,asks:'Which report?'}).note,'Which report?');
});

test('machine previews use the paged Hub order within each priority and preserve legacy order for older owners',()=>{
  const row=(id,state,at)=>ResourceRow.from({session_id:id,machine_id:'mac',state,updated_at:at},'session');
  const rows=[row('older','needs_you','2026-01-01T00:00:00Z'),row('z','working','2026-01-02T00:00:00.001006Z'),
    row('newer','needs_you','2026-01-03T00:00:00Z'),row('a','working','2026-01-02T08:00:00.001006+08:00'),
    row('b','working','2026-01-02T00:00:00.001002Z'),row('unknown','working','')];
  assert.deepEqual(machineAgents(rows,true).map(a=>a.row.id),['newer','older','a','z','b','unknown']);
  assert.deepEqual(machineAgents(rows,false).map(a=>a.row.id),['older','newer','z','a','b','unknown']);
});

test('plots use bucket timestamps and memory percentages, retain gaps, separate upload and download and never draw missing values as zero',()=>{
  const sample=new MachineReading({system:{memory:1000},latest:{mem_total:2000}, points:[
    {t:1,mem:2000,rx:8},{t:95,cpu:0,mem:1000,rx:43,tx:17},{t:98,gap:true,mem:1500},
    {t:99,rx:42},{t:100,cpu:37,mem:0,rx:0,tx:0},{t:103,cpu:99}
  ],usage:{recent:[0,7,11]}});
  assert.deepEqual(machineTrace(sample,'mem',7,100).map(v=>[v.time,v.value]),[[95,50],[98,-1],[99,-1],[100,0]]);
  assert.deepEqual(machineTrace(sample,'network',7,100).map(v=>[v.value,v.alternate]),[[43,17],[-1,-1],[42,-1],[0,0]]);
  assert.deepEqual(machineTrace(sample,'cpu',7,100).map(v=>v.value),[0,-1,-1,37]);
  assert.deepEqual(machineTrace(sample,'tokens',3600,661).map(v=>[v.time,v.value]),[[540,0],[600,7],[660,11]]);
});

test('system identity and aggregates match the iOS OS-family and token wire formats',()=>{
  assert.equal(osFamily({distro:'raspbian',like:'debian',os:'linux'},''),'debian');
  assert.equal(osFamily({distro:'unknown',like:'linuxmint debian',os:'linux'},''),'ubuntu');
  assert.equal(osFamily({distro:'nixos',os:'linux'},''),'nixos');
  assert.equal(osFamily({},'darwin'),'macos');
  assert.equal(osFamily({},'unknown'),'');
  assert.equal(machineTokenTotal({in:100,out:50,cache_write:6,cache_read:40,req:4}),196);
  for (const [raw,expected] of [[0,'0'],[1250,'1.25K'],[10000,'10K'],[106099,'106K'],[999500,'1M'],
    [2960000,'2.96M'],[168347846,'168M'],[999500000,'1B'],[314159410,'314M'],[NaN,'—'],[-1,'—']]) {
    assert.equal(machineTokens(raw),expected);
  }
  assert.equal(machineBytes(48*2**30),'48.0 GB');
  assert.equal(machinePercent(4.48),'4.5%');
  assert.equal(machinePercent(-1),'—');
  assert.notEqual(machineKey('a','b|c','mac'),machineKey('a|b','c','mac'));
});

const directorySource=readFileSync(new URL('../entry/src/main/ets/components/MachineDirectory.ets',import.meta.url),'utf8');
const loader=directorySource.slice(directorySource.indexOf('  private async load('),directorySource.indexOf('  private chooseRange('));
const {Wire}=await import('../entry/src/main/ets/models/Wire.ets');
const Directory=new Function('Wire','MachineReading','machineKey','RANGES','RANGE_SECONDS','setTimeout',
  'return '+stripTypeScriptTypes('class Directory {'+loader+'}'))(Wire,MachineReading,machineKey,['1h','6h','24h'],[3600,21600,86400],()=>7);

test('a machine detail never installs a late or differently owned measurement when switching chart ranges',async()=>{
  const directory=new Directory(),calls=[],pending=[];
  const machine=ResourceRow.from({id:'mac/one',manager:'assistant',online:true},'machine');machine.group='hub';
  let epoch=2;
  const owner={connected:true,scope:()=> 'hub/assistant',connectionGeneration:()=>epoch,owns:(e,s)=>e===epoch&&s==='hub/assistant',
    repo:()=>({call:url=>new Promise(r=>{calls.push(url);pending.push(r)})})};
  Object.assign(directory,{model:{scope:()=> 'root',connectionGeneration:()=>1,owns:(e,s)=>e===1&&s==='root',directoryOwnerFor:()=>owner},
    revision:1,active:true,selected:machine,range:0});
  const old=directory.load(1,false);directory.range=1;directory.revision=2;const newer=directory.load(2,false);
  pending[1]({metrics:{machine_id:machine.id,latest:{cpu:61}}});await newer;
  pending[0]({metrics:{machine_id:machine.id,latest:{cpu:99}}});await old;
  assert.equal(directory.detailSample.latest.cpu,61);assert.deepEqual(calls,[
    '/api/v1/machines/mac%2Fone/metrics?range=1h','/api/v1/machines/mac%2Fone/metrics?range=6h']);
  const replaced=directory.load(2,false);epoch++;pending[2]({metrics:{machine_id:machine.id,latest:{cpu:10}}});await replaced;
  assert.equal(directory.detailSample.latest.cpu,61);
  const wrong=directory.load(2,false);pending[3]({metrics:{machine_id:'another',latest:{cpu:18}}});await wrong;
  assert.equal(directory.failed,true);assert.equal(directory.detailSample.latest.cpu,61);
});

test('overview keeps a failed manager’s last sample while removing vanished or unlisted machine totals',async()=>{
  const d=new Directory(),row=(group,id)=>{const v=ResourceRow.from({id,manager:'default'},'machine');v.group=group;return v};
  const owner=(group,answer)=>({connected:true,profile:{id:group,manager:'default'},scope:()=>group,connectionGeneration:()=>2,owns:()=>true,repo:()=>({call:answer})});
  const a=owner('home',async()=>({machines:[{machine_id:'mac',latest:{cpu:20}},{machine_id:'unlisted',latest:{cpu:80}}]}));
  const b=owner('travel',async()=>{throw Error('temporarily offline')});
  Object.assign(d,{subscriptionUsage:{refresh:async()=>{}},model:{scope:()=> 'global',connectionGeneration:()=>1,owns:()=>true,directoryOwners:()=>[a,b]},
    visibleRows:[row('home','mac'),row('travel','mac')],active:true,revision:1,range:0,
    readings:new Map([[machineKey('travel','default','mac'),new MachineReading({latest:{cpu:13}})],
      [machineKey('gone','default','mac'),new MachineReading({latest:{cpu:88}})]])});
  await d.load(1,false);
  assert.deepEqual([...d.readings.keys()].sort(),[machineKey('home','default','mac'),machineKey('travel','default','mac')].sort());
  assert.equal(d.readings.get(machineKey('home','default','mac')).latest.cpu,20);
  assert.equal(d.readings.get(machineKey('travel','default','mac')).latest.cpu,13);
});

test('both Machines entry points load subscription windows with their authenticated client',async()=>{
  const model={scope:()=> 'only',connectionGeneration:()=>1,owns:()=>true,directoryOwners:()=>[]};
  const calls=[];
  for (const settingsScope of [false,true]) {
    const page=Object.assign(new Directory(),{model,settingsScope,active:true,revision:1,visibleRows:[],readings:new Map(),
      subscriptionUsage:{refresh:async(client,force)=>calls.push([client,force])}});
    await page.load(1,false);
    assert.equal(page.failed,false);
  }
  assert.deepEqual(calls,[[model,false],[model,false]]);
});

const chartSource=readFileSync(new URL('../entry/src/main/ets/components/MachineChart.ets',import.meta.url),'utf8');
const scrub=chartSource.slice(chartSource.indexOf('  private scrub('),chartSource.indexOf('  private drag('));
const HistoryChart=new Function('return '+stripTypeScriptTypes('class HistoryChart {'+scrub+'}'))();
const history=directorySource.slice(directorySource.indexOf('  private historyValue('),directorySource.indexOf('  private liveMetrics('));
const HistoryReadout=new Function('Wire','return '+stripTypeScriptTypes('class HistoryReadout {'+history+'}'))(Wire);

test('scrubbing a monitoring gap preserves the requested time instead of presenting a distant measurement',()=>{
  const chart=new HistoryChart(),picked=[];
  Object.assign(chart,{interactive:true,plotWidth:1002,end:2000,seconds:1000,step:10,selectedTime:0,
    points:[{time:1000,value:9,alternate:-1},{time:1400,value:-1,alternate:-1},
      {time:1700,value:-1,alternate:40}],pick:t=>picked.push(t)});
  chart.scrub(11); chart.scrub(401); chart.scrub(685);
  assert.deepEqual(picked,[1000,1400,1700]);
  chart.interactive=false; chart.scrub(11); assert.equal(picked.length,3);
});

test('offline histories retain genuine past readings without advertising the previous connection as live',()=>{
  const history=new HistoryReadout();
  history.latest=()=>12;
  const m=new MachineReading({latest:{net_rx:12,net_tx:14}});
  const points=[{time:700,value:100,alternate:150},{time:800,value:-1,alternate:-1}];
  history.inspected=0;
  assert.equal(history.historyValue(m,'network',points,false),-1);
  assert.equal(history.historyValue(m,'network',points,false,true),-1);
  assert.equal(history.historyValue(m,'network',points,true,true),14);
  history.inspected=700; assert.equal(history.historyValue(m,'network',points,false,true),150);
  history.inspected=800; assert.equal(history.historyValue(m,'network',points,true),-1);
  history.inspected=910; assert.equal(history.historyValue(m,'network',points,true,true),-1);
  assert.equal(history.chartCeiling('cpu',points),100);
  assert.ok(history.chartCeiling('network',points)>150);
});

test('a mounted plot and its value read the accepted sample again after a poll or range switch',()=>{
  const getter=directorySource.slice(directorySource.indexOf('  @Computed get historySeries()'),directorySource.indexOf('  private historyValue('))
    .replace('@Computed ', '');
  const Series=class { constructor(points,ceiling) { this.points=points; this.ceiling=ceiling; } };
  const Mounted=new Function('HISTORY_FIELDS','RANGE_SECONDS','HistorySeries','EMPTY_SERIES','machineTrace','Wire',
    'return '+stripTypeScriptTypes('class Mounted {'+getter+history+'}'))(
    ['cpu','mem','network','load'],[3600,21600,86400],Series,new Series([],1),machineTrace,Wire);
  const view=Object.assign(new Mounted(),{shownRange:0,chartEnd:30000,inspected:0,cards:[{online:true}],
    latest:(m,key)=>m.latest[key],detailSample:new MachineReading({points:[{t:29000,cpu:40}],latest:{cpu:40}})});
  // Evaluate the plot's real points expression through a retained key. An array bound when
  // ForEach first creates its row cannot see the later range or a replacement sample.
  const plot=directorySource.slice(directorySource.indexOf('  @Builder historyPlot('),directorySource.indexOf('  @Builder chartRow('));
  const pointsExpression=/MachineChart\(\{points:([^,]+),/.exec(plot)[1];
  const readPoints=new Function('key','return '+pointsExpression).bind(view,'cpu');
  const readCPU=view.currentChartValue.bind(view,'cpu',false);
  assert.deepEqual(readPoints().map(p=>p.value),[40]);
  assert.equal(readCPU(),40);
  view.shownRange=1;
  view.detailSample=new MachineReading({points:[{t:10000,cpu:35},{t:29000,cpu:83}],latest:{cpu:83}});
  assert.deepEqual(readPoints().map(p=>p.value),[35,83]);
  assert.equal(readCPU(),83);
  view.inspected=10000; assert.equal(readCPU(),35);
  view.detailSample=undefined;
  assert.deepEqual(readPoints(),[]); assert.equal(readCPU(),-1);
});

test('the monitor keeps its six-hour clock grid aligned as a new CPU sample arrives', async () => {
  const {dashboardTimeTicks} = await import('../entry/src/main/ets/models/MachineDashboard.ets');
  const end = new Date(2026, 10, 21, 20, 37).getTime() / 1000;
  const ticks = dashboardTimeTicks(end, 21600);
  assert.equal(ticks.length, 6);
  assert.deepEqual(ticks.map(t => new Date(t*1000).getHours()), [15,16,17,18,19,20]);
  assert.ok(ticks.every(t => new Date(t*1000).getMinutes() === 0));
  assert.deepEqual(dashboardTimeTicks(end+15,21600),ticks);
  assert.deepEqual(dashboardTimeTicks(end,0),[]);
  for (const seconds of [3600,21600,86400]) {
    assert.ok(dashboardTimeTicks(end,seconds).every(t => t>=end-seconds && t<=end));
  }
});

test('monitor clock labels fit their measured plot at normal and 3.2× type, including both limits on crowded axes', async () => {
  const {dashboardLabelTicks,dashboardTickLeft,dashboardTimeTicks} = await import('../entry/src/main/ets/models/MachineDashboard.ets');
  const end = new Date(2026,10,21,20,37).getTime()/1000;
  for (const seconds of [3600,21600,86400]) {
    const regular = dashboardLabelTicks(end,seconds,300,25);
    assert.deepEqual(regular,dashboardTimeTicks(end,seconds));
    for (const [width,labelWidth] of [[260,32],[213,80],[166,80]]) {
      const times = dashboardLabelTicks(end,seconds,width,labelWidth);
      const left = times.map(t => dashboardTickLeft(t,end,seconds,width,labelWidth));
      assert.ok(left.every((x,i) => x>=0 && x+labelWidth<=width && (!i || x-left[i-1]>=labelWidth+6)));
      if (labelWidth===80) assert.deepEqual(times,[end-seconds,end]);
    }
    // Below this size both clocks move into a wrapping native row, so neither is clipped.
    assert.deepEqual(dashboardLabelTicks(end,seconds,150,80),[]);
  }
  const axis=directorySource.slice(directorySource.indexOf('struct MachineTimeAxis'), directorySource.indexOf('export struct MachineDirectory'));
  assert.match(axis,/this\.labelWidth = Math\.ceil\(Number\(area\.width\)\)/);
  assert.match(axis,/this\.axisWidth = Number\(area\.width\)/);
  assert.match(axis,/wrap:FlexWrap\.Wrap[\s\S]*?clockLabel\(this\.end-this\.seconds\)[\s\S]*?clockLabel\(this\.end\)/);
  assert.doesNotMatch(axis,/\.height\(\d+\)/);
});

test('the full localized range remains readable at large type and each segment fetches the corresponding interval', async () => {
  const control=directorySource.slice(directorySource.indexOf('ForEach(RANGE_LABELS'),directorySource.indexOf('if (this.detailSample)',directorySource.indexOf('ForEach(RANGE_LABELS')));
  assert.match(control,/Text\(tr\(key\)\)\.fontSize\(14\)\.maxFontScale\(Tokens\.navigationTextMaxScale\)/);
  assert.match(control,/constraintSize\(\{minHeight:44\}\)/);
  assert.match(control,/accessibilityText\(tr\(key\)\)\.accessibilitySelected\(this\.range === i\)/);
  assert.match(control,/onClick\(\(\) => this\.chooseRange\(i\)\)/);
  assert.doesNotMatch(control,/\.height\(|\.maxLines\(1\)/);
  const source=directorySource.match(/  private chooseRange\([\s\S]*?\n  \}/)[0];
  const Picker=new Function('Directory','RANGES','return '+stripTypeScriptTypes('class Picker extends Directory {'+source+'}'))(Directory,['1h','6h','24h']);
  const screen=new Picker(), calls=[], machine=ResourceRow.from({id:'MacBook'},'machine');
  const owner={connected:true,scope:()=> 'hub',connectionGeneration:()=>1,owns:()=>true,repo:()=>({call:async url=>{
    calls.push(url); return {metrics:{machine_id:machine.id}};
  }})};
  let refreshed;
  Object.assign(screen,{revision:1,active:true,range:0,selected:machine,model:{...owner,directoryOwnerFor:()=>owner},
    sync:()=>{refreshed=screen.load(++screen.revision,false)}});
  await screen.load(1,false);
  for(const range of [1,2,0]) { screen.chooseRange(range); await refreshed; assert.equal(screen.shownRange,range); }
  assert.deepEqual(calls.map(url=>url.split('?range=')[1]),['1h','6h','24h','1h']);
});

test('live monitoring keeps the iPhone two-column summary on a phone and collapses only a genuinely narrow pane',()=>{
  const live=directorySource.slice(directorySource.indexOf('  @Builder liveTile('),directorySource.indexOf('  @Builder seriesValue('));
  const expression=live.match(/GridRow\(\{columns:([^,]+),gutter/)[1];
  const columns=new Function('return '+expression);
  for(const width of [0,258,288,320,328,370,402])assert.equal(columns.call({liveGridWidth:width}),2,'width '+width);
  for(const width of [160,220,257])assert.equal(columns.call({liveGridWidth:width}),1,'width '+width);
  assert.match(live,/Text\(tile\.title\)[^\n]*\.maxLines\(2\)/);
  assert.match(live,/Text\(tile\.detail[^\n]*\.minFontSize\(9\)[\s\S]*?\.maxLines\(2\)/);
});


test('machine detail session avatars use the explicit machine role and its legacy laptop default',()=>{
  for(const icon of ['server','desktop','laptop']) assert.equal(machineAvatarSymbol(icon),icon);
  for(const icon of ['','unknown','Server']) assert.equal(machineAvatarSymbol(icon),'laptop');
});

test('the standard machine session avatar composites to the measured system fill on both cards',()=>{
  for (const [mode,background,expected] of [['base',[255,255,255],[239,239,240]],['dark',[44,44,46],[62,62,66]]]) {
    const colors=JSON.parse(readFileSync(new URL('../entry/src/main/resources/'+mode+'/element/color.json',import.meta.url),'utf8')).color;
    const value=colors.find(color=>color.name==='machine_session_avatar_fill').value;
    const [alpha,...rgb]=value.slice(1).match(/../g).map(byte=>parseInt(byte,16));
    assert.deepEqual(rgb.map((v,ix)=>Math.round((v*alpha+background[ix]*(255-alpha))/255)),expected);
  }
});
