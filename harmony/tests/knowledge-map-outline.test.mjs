import test from 'node:test';
import assert from 'node:assert/strict';
import { flattenMap, initialMapExpansion, knowledgeMapStats, pointSources, projectOutline, topicOutline } from '../entry/src/main/ets/core/KnowledgeMapOutline.ts';

const t = key => key;
test('projects group real entries by status and rank, with independent target and folding actions', () => {
  const items = ['current','next','later','done','paused','hidden'].map((id, i) => ({
    id, name: id, updated:'2026-11-01T10:00:00Z', importance:['P0','P2','P3','P1','P2','P0'][i]
  }));
  const details = new Map([
    ['current', {meta:{status:'active', goal:'Keep the launch reliable', dirs:['/tmp/example']},summary:'## 现状\n- First\n  - Nested',log:['2026-11-01 14:00 · Persona · Reviewed','2026-11-01 14:01 · Agent · Ignore'],links:[{kind:'chats',id:'real',name:'Team'}]}],
    ['done',{meta:{status:'done'}}], ['paused',{meta:{status:'paused'}}], ['hidden',{meta:{status:'merged'}}]
  ]);
  const roots=projectOutline(items,details,t),expanded=initialMapExpansion(roots);
  assert.deepEqual(flattenMap(roots,expanded).map(r=>r.node.id),['branch:active','group:P0','project:current','group:P2','project:next','group:P3','branch:done','branch:paused']);
  const row=roots[0].children[0].children[0];assert.equal(row.targetID,'current');assert.equal(row.targetKind,'projects');
  expanded.add(row.id);
  const tree=flattenMap(roots,expanded);
  const nested=tree.find(v=>v.node.title==='First');
  assert.equal(nested.node.children[0].title,'Nested');assert.ok(!expanded.has(nested.node.id));
  assert.equal(tree.find(v=>v.node.id==='project:current#goal.0').node.title,'Keep the launch reliable');
  assert.equal(row.children.find(n=>n.id.endsWith('#chats')).children[0].targetID,'real');
  assert.deepEqual(knowledgeMapStats('projects',roots,'map_summary').map(x=>x.count),[3,1,1]);
});
test('topics retain nested paths, point kinds, source names, loose-point cutoff and top-six expansion', () => {
  const items=Array.from({length:7},(_,i)=>({kind:'topics',id:'t'+i,name:'Topic '+i,extra:String(i)}));
  const points=[{id:'decision',text:'Choose a release train',kind:'decision',path:['  Rollout ',''],why:'Repeatability',sources:[{kind:'projects',id:'weather'},{kind:'projects',id:'weather'},{kind:'people',id:'missing'}]},
    ...['a','b','c','d'].map(id=>({id,text:id,path:[]})),{id:'blank',text:''}];
  const details=new Map([['t6',{points,links:[{kind:'projects',id:'weather',name:'Actual weather project'}]}]]);
  const roots=topicOutline(items,details,t);const main=roots.find(n=>n.id==='topic:t6');
  assert.deepEqual(main.children.map(n=>n.title),['Rollout','map_other']);
  assert.ok(main.children.every(n=>n.collapsed));assert.ok(roots[6].collapsed);
  const nested=main.children[0].children[0];assert.equal(nested.point.why,'Repeatability');assert.deepEqual(nested.crumbs,['Topic 6','Rollout']);
  assert.deepEqual(pointSources(nested).map(r=>r.name),['Actual weather project']);
  assert.deepEqual(knowledgeMapStats('topics',roots,'map_summary').map(x=>x.count),[7,5,2]);
});
