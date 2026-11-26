/** Compact Knowledge Map hierarchy, following the native iPhone topic and project outline. */
export type MapFields = Record<string, Object>;
type Label = (key: string) => string;
export interface KnowledgeMapNode {
  id: string;
  title: string;
  kind: string;
  tone: string;
  meta: string;
  children: KnowledgeMapNode[];
  collapsed: boolean;
  targetKind: string;
  targetID: string;
  point?: MapFields;
  links: MapFields[];
  crumbs: string[];
}
export interface KnowledgeMapLine { node: KnowledgeMapNode; depth: number }
export const mapText = (item: MapFields, key: string): string => typeof item[key] === 'string' ? item[key] as string : '';
function object(value: Object | undefined): MapFields { return value && typeof value === 'object' && !Array.isArray(value) ? value as MapFields : {}; }
function rows(value: Object | undefined): MapFields[] { return Array.isArray(value) ? value.filter(row => row && typeof row === 'object' && !Array.isArray(row)) : []; }
function words(value: Object | undefined): string[] { return Array.isArray(value) ? value.filter(row => typeof row === 'string') : []; }
function node(id: string, title: string, kind: string, tone: string, children: KnowledgeMapNode[] = [], collapsed = false, meta = ''): KnowledgeMapNode {
  return { id, title, kind, tone, children, collapsed, meta, targetKind: '', targetID: '', links: [], crumbs: [] };
}
function target(value: KnowledgeMapNode, kind: string, id: string): KnowledgeMapNode { value.targetKind = kind; value.targetID = id; return value; }
export function flattenMap(roots: KnowledgeMapNode[], expanded: Set<string>): KnowledgeMapLine[] {
  const result: KnowledgeMapLine[] = [];
  function visit(value: KnowledgeMapNode, depth: number) {
    result.push({ node:value, depth });
    if (expanded.has(value.id)) value.children.forEach(child => visit(child, depth+1));
  }
  roots.forEach(root => visit(root,0));
  return result;
}
export function initialMapExpansion(roots: KnowledgeMapNode[]): Set<string> {
  const expanded = new Set<string>();
  function visit(value: KnowledgeMapNode) {
    if (value.children.length && !value.collapsed) expanded.add(value.id);
    value.children.forEach(visit);
  }
  roots.forEach(visit);
  return expanded;
}
interface PointGroup { name: string; groups: PointGroup[]; points: MapFields[]; count: number }
function topicGroups(points: MapFields[]): PointGroup {
  const root: PointGroup = { name:'', groups:[], points:[], count:0 };
  for (const p of points) {
    if (!mapText(p,'text')) continue;
    let group=root;group.count++;
    for (const part of words(p.path).map(s => s.trim()).filter(Boolean)) {
      let sub=group.groups.find(candidate => candidate.name===part);
      if (!sub) { sub={name:part, groups:[], points:[], count:0};group.groups.push(sub); }
      group=sub;group.count++;
    }
    group.points.push(p);
  }
  return root;
}
function topicBranch(g: PointGroup, title: string, key: string, tone: string, links: MapFields[], crumbs: string[], t: Label): KnowledgeMapNode {
  const path = [...crumbs,title];
  const subs=g.groups.map(sub => topicBranch(sub,sub.name,key+'/'+encodeURIComponent(sub.name),tone,links,path,t));
  const points=g.points.map((p,i) => {
    const value = node('point:'+key+'#'+(mapText(p,'id')||i),mapText(p,'text'),'point',tone);
    value.point=p;value.links=links;value.crumbs=path;
    return value;
  });
  const children=subs.length && points.length>3 ? [...subs,node(key+'#other',t('map_other'),'topic',tone,points,true,String(points.length))] : [...subs,...points];
  const result=node(key,title,crumbs.length ? 'topic' : 'topicRoot',tone,children,crumbs.length>0,String(g.count));result.crumbs=crumbs;return result;
}
export function topicOutline(items: MapFields[], details: Map<string,MapFields>, t: Label): KnowledgeMapNode[] {
  const order=items.map(entry => {
    const detail=details.get(mapText(entry,'id'));
    const group=topicGroups(rows(detail?.points));
    const count=Array.isArray(detail?.points) ? group.count : Number.parseInt(mapText(entry,'extra'),10) || 0;
    return { entry, detail, group, count };
  }).sort((a,b) => b.count-a.count || mapText(a.entry,'name').localeCompare(mapText(b.entry,'name'),undefined,{numeric:true}));
  return order.map((v,i) => {
    const id=mapText(v.entry,'id'),root=topicBranch(v.group,mapText(v.entry,'name'), 'topic:'+id,['accent','green','orange'][i%3],rows(v.detail?.links),[],t);
    root.collapsed=i>=6;root.meta=String(v.count);return target(root,'topics',id);
  });
}
interface SummarySection { title: string; children: KnowledgeMapNode[] }
function cleanText(text: string): string {
  return text.replace(/\*\*|__|`/g,'').replace(/\[([^\]]+)\]\([^)]+\)/g,'$1').replace(/\s+/g,' ').trim();
}
function summarySections(markdown: string, base: string, tone: string, t: Label): SummarySection[] {
  const sections: SummarySection[]=[];
  const stack: { indent: number; line: KnowledgeMapNode }[]=[];
  for (const raw of markdown.split('\n')) {
    const text=raw.trim();if (!text || raw.startsWith('# ')) continue;
    const heading=text.match(/^#{2,}\s+(.+)/);
    if (heading) { sections.push({title:cleanText(heading[1]),children:[]});stack.length=0;continue; }
    if (!sections.length) sections.push({title:t('map_summary'),children:[]});
    const section=sections[sections.length-1],list=raw.match(/^(\s*)(?:[-*+]|\d+[.)])\s+/);
    const indent=list ? list[1].replace(/\t/g,'  ').length : 0;
    const title=cleanText(list ? raw.slice(list[0].length) : raw);if (!title) continue;
    while (stack.length && stack[stack.length-1].indent>=indent) stack.pop();
    const parent=list && stack.length ? stack[stack.length-1].line : undefined;
    const children=parent ? parent.children : section.children;
    const id=(parent?.id || base+'#s'+(sections.length-1))+'.'+children.length;
    const line=node(id,title,'text',tone,[],true);children.push(line);
    if (list) stack.push({indent,line});
  }
  return sections.filter(s=>s.children.length);
}
function personaLog(raw:string): {time:string;text:string} | undefined {
  const parts=raw.trim().replace(/^-\s*/,'').split(' · ');
  if(parts.length<3 || parts[1]!=='Persona' || parts[0].length!==16 || !/^\d/.test(parts[0]))return undefined;
  let text=parts.slice(2).join(' · ');
  const repeated=/^\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}(?:\s+[^·]{1,10})?\s*·\s*Persona\s*·\s*/;
  while(repeated.test(text)){const next=text.replace(repeated,'');if(!next) break;text=next;}
  return {time:parts[0],text:text.replace(/[*`#]+/g,'').replace(/\s+/g,' ').trim()};
}
function projectStatus(e:MapFields,d:MapFields):string {
  return mapText(object(d.meta),'status').trim() || (mapText(e,'extra').split(' · ')[1]||'').trim() || 'active';
}
function projectBranch(e:MapFields,d:MapFields,tone:string,t:Label):KnowledgeMapNode {
  const id=mapText(e,'id'),base='project:'+id,meta=object(d.meta),parts:KnowledgeMapNode[]=[];
  const sections=summarySections(mapText(d,'summary'),base,tone,t);
  function field(key:string,title:string,value:string){if(value.trim())parts.push(node(base+'#'+key,title,'section',tone,[node(base+'#'+key+'.0',value.trim(),'text',tone)]));}
  if(!sections.some(s=>s.title==='目标'))field('goal',t('map_goal'),mapText(meta,'goal'));
  sections.forEach((s,i)=>parts.push(node(base+'#s'+i,s.title,'section',tone,s.children,!['目标','现状'].includes(s.title),t('map_summary'))));
  if(!sections.some(s=>s.title==='我的角色'))field('role',t('map_role'),mapText(meta,'my_role'));
  const lines=words(d.log).map(personaLog).filter((v):v is {time:string;text:string}=>!!v).reverse();
  if(lines.length){
    const latest=lines[0];parts.push(node(base+'#latest',t('map_latest'),'section',tone,[node(base+'#latest.0',latest.text,'text',tone,[],false,latest.time)],false,latest.time.slice(5)));
    const days=new Map<string,KnowledgeMapNode[]>();
    for(const l of lines){const day=l.time.slice(0,10),list=days.get(day)||[];list.push(node(base+'#day'+day+'.'+list.length,l.time.slice(11)+' · '+l.text,'text',tone,[],false,l.time));days.set(day,list);}
    const notes=Array.from(days,([day,kids])=>node(base+'#day'+day,day.slice(5),'day',tone,kids,true,String(kids.length)));
    parts.push(node(base+'#notes',t('map_notes'),'section',tone,notes,true,String(lines.length)));
  }
  const dirs=words(meta.dirs);if(dirs.length)parts.push(node(base+'#dirs',t('map_dirs'),'section',tone,dirs.map(dir=>node(base+'#dir'+dir,dir,'text',tone)),true,String(dirs.length)));
  for(const kind of ['chats','people']){
    const links=rows(d.links).filter(v=>mapText(v,'kind')===kind);
    if(links.length)parts.push(node(base+'#'+kind,t('map_'+kind),'section',tone,links.map(v=>target(node(base+'#'+kind+'.'+mapText(v,'id'),mapText(v,'name'),'link',tone),kind,mapText(v,'id'))),true,String(links.length)));
  }
  const status=projectStatus(e,d);
  return target(node(base,mapText(e,'name'),'project',tone,parts,true,[mapText(e,'importance'),t('map_'+(status==='done' || status==='paused' ? status : 'active'))].filter(Boolean).join(' · ')),'projects',id);
}
export function projectOutline(items: MapFields[],details:Map<string,MapFields>,t:Label):KnowledgeMapNode[] {
  const priorities=['P0','P1','P2','P3'];
  const enriched=items.map(e=>{const d=details.get(mapText(e,'id'))||{};const score=priorities.indexOf(mapText(e,'importance'));return {e,d,status:projectStatus(e,d),rank:score<0 ? 9 : score};})
    .filter(v=>v.status!=='merged' && v.status!=='ignored')
    .sort((a,b)=>a.rank-b.rank || (Date.parse(mapText(b.e,'updated'))||0)-(Date.parse(mapText(a.e,'updated'))||0));
  const active=enriched.filter(v=>!['done','paused'].includes(v.status)),groups=new Map<string,KnowledgeMapNode[]>();
  for(const v of active){const key=v.rank<4 ? priorities[v.rank] : 'unrated';const g=groups.get(key)||[];g.push(projectBranch(v.e,v.d,'accent',t));groups.set(key,g);}
  const sets=Array.from(groups,([key,g],i)=>node('group:'+key,key==='unrated'?t('map_unrated'):key,'group','accent',g,i>=2,String(g.length)));
  const result=[node('branch:active',t('map_active'),'branch','accent',sets,false,String(active.length))];
  for(const k of ['done','paused']){const tone=k==='done'?'green':'orange';const kids=enriched.filter(v=>v.status===k).map(v=>projectBranch(v.e,v.d,tone,t));if(kids.length)result.push(node('branch:'+k,t('map_'+k),'branch',tone,kids,true,String(kids.length)));}
  return result;
}
export function pointSources(value:KnowledgeMapNode):MapFields[] {
  const seen=new Set<string>(),out:MapFields[]=[];
  for(const s of rows(value.point?.sources)){const k=mapText(s,'kind'),id=mapText(s,'id'),key=k+'/'+id;const link=value.links.find(v=>mapText(v,'kind')===k && mapText(v,'id')===id);if(link && !seen.has(key)){out.push(link);seen.add(key);}}
  return out;
}
export interface KnowledgeMapStat { count:number; key:string }
export function knowledgeMapStats(mode:string, roots:KnowledgeMapNode[], summaryLabel:string):KnowledgeMapStat[] {
  if (mode==='topics') {
    let count=0;
    const sources=new Set<string>();
    function visit(n:KnowledgeMapNode) {
      if (n.point) {
        count++;
        rows(n.point.sources).forEach(s=>sources.add(mapText(s,'kind')+'/'+mapText(s,'id')));
      }
      n.children.forEach(visit);
    }
    roots.forEach(visit);
    return [{count:roots.length,key:'map_topic_count'},{count,key:'map_points'},{count:sources.size,key:'map_sources'}];
  }
  const active=roots.find(b=>b.id==='branch:active')?.children.flatMap(g=>g.children)||[];
  const projects=[...active,...roots.filter(b=>b.id!=='branch:active').flatMap(b=>b.children)];
  return [{count:active.length,key:'map_active'},{count:projects.filter(n=>n.children.some(s=>s.meta===summaryLabel)).length,key:'map_with_summary'},
    {count:projects.reduce((v,p)=>v+Number(p.children.find(s=>s.id.endsWith('#notes'))?.meta||0),0),key:'map_notes'}];
}

export function formatMapDay(date:Date,locale:string):string {
  const options:Intl.DateTimeFormatOptions={month:'short',day:'numeric'};
  if(date.getFullYear()!==new Date().getFullYear())options.year='numeric';
  return new Intl.DateTimeFormat(locale || undefined,options).format(date);
}
