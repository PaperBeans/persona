import { legacyContextTag, legacyFileTag, legacyProduct } from './WireCompatibility.ts';

/** The shared forward block contract from spec/fixtures/forward. Local image handles never go on the wire. */
export type ForwardFields = Record<string,Object>;
export const forwardMaximum = 30;
export const forwardTotalCharacters = 60000;
export function forwardLimit(count:number):number { return Math.min(20000,Math.floor(forwardTotalCharacters/Math.max(1,count))); }
export interface ForwardSource {
  machineID:string; sessionID:string; item:string; role:string; author:string; chat:string; project:string; machine:string; time:string;
}
export interface ForwardBlock {
  kind:string; title:string; url:string; msg:string; role:string; author:string; chat:string;
  project:string; machine:string; time:string; text:string;
}
export function wireContextAttribute(value:string):string {return value.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;');}
export function wireContextBody(value:string):string {
 const closer=new RegExp('<(\\s*\\/\\s*(?:persona|' + legacyProduct + ')-context\\s*>)','gi');
 return value.replace(closer,'').replace(closer,'&lt;$1');
}
export function forwardUnescape(value:string):string {return value.replace(/&quot;/g,'"').replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&amp;/g,'&');}
export function forwardURL(machine:string,session:string,item:string):string {
 return 'persona://session/'+encodeURIComponent(machine)+'/'+encodeURIComponent(session)+(item?'?at='+encodeURIComponent(item):'');
}
export function forwardTimestamp(value:number):string {
 if(!Number.isFinite(value)||value<=0)return '';
 const date=new Date(value<100000000000 ? value*1000 : value),offset=-date.getTimezoneOffset(),pad=(v:number)=>String(v).padStart(2,'0');
 return date.getFullYear()+'-'+pad(date.getMonth()+1)+'-'+pad(date.getDate())+'T'+pad(date.getHours())+':'+pad(date.getMinutes())+':'+pad(date.getSeconds())+
   (offset===0?'Z':(offset<0?'-':'+')+pad(Math.floor(Math.abs(offset)/60))+':'+pad(Math.abs(offset)%60));
}
export function wireForwardBlock(source:ForwardFields,tag:string):string {
 const value=(key:string):string=>typeof source[key]==='string'?source[key] as string:'';
 const keys=['kind','title','url','msg','role','author','chat','project','machine','time'];
 const attrs=keys.filter(k=>k==='kind'||k==='title'||!!value(k)).map(k=>k+'="'+wireContextAttribute(value(k))+'"').join(' ');
 return '<'+tag+' '+attrs+'>\n'+wireContextBody(value('text'))+'\n</'+tag+'>';
}
export interface ParsedForwardPrefix { entries:ForwardFields[]; rest:string }
export function forwardPrefix(input:string):ParsedForwardPrefix {
 const entries:ForwardFields[]=[];let rest=input;
 while(entries.length<60){
  const mt=new RegExp('^<(persona-context|' + legacyContextTag + ')\\b((?:[^">]|"[^"]*")*)>([\\s\\S]*?)<\\/\\1>\\s*').exec(rest);
  if(!mt)break;
  const fields:ForwardFields={};for(const a of mt[2].matchAll(/([\w-]+)="([^"]*)"/g))fields[a[1]]=forwardUnescape(a[2]);
  fields.text=mt[3].replace(/^\n|\n$/g,'');entries.push(fields);rest=rest.slice(mt[0].length);
 }
 return {entries,rest};
}
interface ForwardSegment { index:number; segment:string }
interface ForwardSegmenter { segment(input:string):Iterable<ForwardSegment> }
function cutBody(body:string,cap:number,cut:string):string {
 if(body.length<=cap)return body;
 const factory=(Intl as unknown as {Segmenter?:new(locale:string,options:{granularity:string})=>ForwardSegmenter}).Segmenter;
 if(factory){
  let count=0;
  for(const part of new factory('en',{granularity:'grapheme'}).segment(body)){
   if(count++===cap)return body.slice(0,part.index)+'\n\n'+cut;
  }
  return body;
 }
 // Older runtimes group combining marks, paired flags, modifiers and ZWJ emoji before counting.
 const graphemes=body.match(/(?:\r\n|\p{Regional_Indicator}{2}|[^\r\n](?:\p{Mark}|\p{Emoji_Modifier}|\uFE0F)*(?:\u200D[^\r\n](?:\p{Mark}|\p{Emoji_Modifier}|\uFE0F)*)*|[\r\n])/gu)||[];
 return graphemes.length>cap?graphemes.slice(0,cap).join('')+'\n\n'+cut:body;
}
export function forwardedBody(raw:string,machine:string,cap:number,cut:string,file:(name:string,machine:string)=>string,
                             content:(text:string)=>{text:string;files:string[]}):string {
 const cleaned=raw.replace(new RegExp('<(persona-context|' + legacyContextTag + ')\\b([^>]*)>[\\s\\S]*?<\\/\\1>\\s*','g'),
   (match,_tag,attrs)=>/\bkind="message-source"/.test(attrs)?'':match);
 const parsed=forwardPrefix(cleaned),parts:string[]=[];
 for(const r of parsed.entries){
  if(r.kind!=='quote' && r.kind!=='forward')continue;
  const text=String(r.text||''),heading=String(r.title||'');
  parts.push([heading,...text.split('\n')].map(line=>'> '+line).join('\n'));
 }
 const files:string[]=[];
 const rest=parsed.rest.replace(new RegExp('\\s*<(persona-file|' + legacyFileTag + ')\\b([^>]*?)\\/?>(?:\\s*<\\/\\1>)?\\s*','g'),(_match,_tag,attrs:string)=>{
  const values:Record<string,string>={};for(const a of attrs.matchAll(/(\w+)="([^"]*)"/g))values[a[1]]=forwardUnescape(a[2]);
  if(values.path){const name=values.name||values.path.split('/').filter(Boolean).pop();if(name)files.push(file(name,machine));}
  return '\n';
 });
 const words=content(rest).text.trim();if(words)parts.push(words);
 return cutBody(parts.concat(files).join('\n\n'),cap,cut);
}
export function makeForward(source:ForwardSource,text:string,title:string):ForwardBlock {
 return {kind:'forward',title,url:forwardURL(source.machineID,source.sessionID,source.item),msg:source.item,role:source.role,author:source.author,chat:source.chat,project:source.project,machine:source.machine,time:source.time,text};
}
export function forwardSendNote(text:string,count:number,single:string,several:string):string {
 return text.trim() || !count ? text : count===1 ? single : several;
}

export interface ForwardOrigin { machine:string; session:string; item:string }
export function forwardOrigin(ref:ForwardFields):ForwardOrigin|undefined {
 const url=String(ref.url||''),m=url.match(new RegExp('^(?:persona|' + legacyProduct + ')://session/([^/?#]+)/([^/?#]+)(?:\\?([^#]*))?$'));
 if(!m)return undefined;
 try{
  const machine=decodeURIComponent(m[1]),session=decodeURIComponent(m[2]);
  const at=(m[3]||'').split('&').find(v=>v.startsWith('at='));
  const item=at?decodeURIComponent(at.slice(3)):String(ref.msg||'');
  if(!machine||!session||!item||[machine,session,item].some(v=>v.length>1024 || /[\x00-\x1f]/.test(v)))return undefined;
  return {machine,session,item};
 }catch(_){return undefined;}
}

/** Only adjacent forwards from the same origin share a card. Other reference kinds end that group. */
export function forwardGroups(refs:ForwardFields[]):ForwardFields[][] {
 const groups:ForwardFields[][]=[];let previous='';
 for(const ref of refs){
  if(ref.kind!=='forward'){previous='';continue;}
  const source=forwardOrigin(ref),key=source?JSON.stringify([source.machine,source.session]):'';
  if(key && key===previous)groups[groups.length-1].push(ref);else groups.push([ref]);
  previous=key;
 }
 return groups;
}
