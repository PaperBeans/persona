import type { KnowledgeWorkspace } from './KnowledgeWorkspace';

export function knowledgePriorityTone(priority:string):string { return priority==='P0' ? 'attention' : priority==='P1' ? 'working' : priority==='P2' ? 'blue' : 'secondary'; }
export function knowledgeRelevanceTone(relevance:number):string { return relevance>=70 ? 'attention' : relevance>=40 ? 'working' : 'neutral'; }
export function knowledgeInk(tone:string,dark:boolean):string {
  if (tone==='attention') return dark ? '#0A84FF' : '#007AFF';
  if (tone==='working') return dark ? '#FFB224' : '#E8890C';
  if (tone==='blue') return dark ? '#5B9BFF' : '#2E6FD0';
  return dark ? '#98989F' : '#7C7C80';
}
export function knowledgeWash(tone:string,dark:boolean):string {
  return tone==='neutral' ? dark ? '#0FFFFFFF' : '#0F000000' : '#21'+knowledgeInk(tone,dark).slice(1);
}

/** The phone's root navigation bar borrows actions only while their actual knowledge page is visible. */
@ObservedV2
export class KnowledgeLandingMenu {
  @Trace tags:string[]=[];
  @Trace selected:string='';
  @Trace query:string='';
  private source:KnowledgeWorkspace|undefined;
  private valid:(()=>boolean)|undefined;
  private openMap:(()=>void)|undefined;
  bind(source:KnowledgeWorkspace,current:()=>boolean,map:()=>void):void {
    this.source=source;this.valid=current;this.openMap=map;
    this.tags=source.tags.slice();this.selected=source.tag;this.query=source.query.trim();
  }
  clear(source:KnowledgeWorkspace):void {
    if (this.source!==source) return;
    this.source=undefined;this.valid=undefined;this.openMap=undefined;this.tags=[];this.selected='';this.query='';
  }
  choose(tag:string):void {
    if (this.source && this.valid?.() && this.source.query.trim()===this.query &&
      (!tag || this.tags.includes(tag) && this.source.tags.includes(tag))) this.source.setTag(tag);
  }
  map():void { if (this.source && this.valid?.()) this.openMap?.(); }
}
