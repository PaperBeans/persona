import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';
import { Wire } from './Wire';

type Fields=Record<string,Object>;
export class MeetingSpeechService {
  ref:string;name:string;contribution:string;
  constructor(ref:string,name:string,contribution:string) { this.ref=ref;this.name=name;this.contribution=contribution; }
}

export function meetingGlossary(lines:string[]):string[]|undefined {
  const seen=new Set<string>(), clean:string[]=[];
  for (const raw of lines) {
    const term=raw.trim().replace(/\s+/g,' '), key=term.toLowerCase();
    if (!term || seen.has(key)) continue;
    if (Array.from(term).length>40 || clean.length>=200) return undefined;
    seen.add(key);clean.push(term);
  }
  return clean;
}

export function meetingSpeechServices(rows:Fields[]):MeetingSpeechService[] {
  const choices=[new MeetingSpeechService('','','')], seen=new Set<string>();
  for (const r of rows) {
    const id=Wire.text(r,'id'), name=Wire.text(r,'name') || id;
    if (!id) continue;
    if (id==='voice') { choices[0].name=name; continue; }
    for (const item of Wire.strings(Wire.object(r,'contributes'),'transcription')) {
      const ref=id+'/'+item;
      if (item && !seen.has(ref)) { seen.add(ref); choices.push(new MeetingSpeechService(ref,name,item)); }
    }
  }
  return choices;
}

@ObservedV2
export class PhoneMeetingTranscription {
  @Trace offered:boolean=false;
  @Trace ready:boolean=false;
  @Trace retentionDays:number=0;
  @Trace mode:string='device';
  @Trace glossary:string[]=[];
  @Trace extensionRef:string='';
  @Trace choices:MeetingSpeechService[]=[];
  @Trace saving:boolean=false;
  @Trace error:string='';
  private owner:ClientModel|undefined;
  private repo:HubRepository|undefined;
  private generation:number=0;
  private scope:string='';
  private revision:number=0;
  close():void {
    this.revision++;this.offered=false;this.ready=false;this.retentionDays=0;this.mode='device';this.glossary=[];this.extensionRef='';this.choices=[];
    this.saving=false;this.error='';this.owner=undefined;this.repo=undefined;
  }
  private current(n:number):boolean {
    const owner=this.owner;
    return !!owner && n===this.revision && owner.connected && owner.owns(this.generation,this.scope) && owner.repo()===this.repo;
  }
  private apply(data:Fields):void {
    const days=Wire.number(data,'recording_days');
    this.retentionDays=Wire.text(data,'manager',this.owner?.profile.manager || '')===(this.owner?.profile.manager || '') &&
      Number.isInteger(days) && days>=1 && days<=365 ? days : 0;
    const mode=Wire.text(data,'meeting_transcription_default');
    this.offered=Wire.text(data,'manager',this.owner?.profile.manager || '')===(this.owner?.profile.manager || '') && ['device','hub'].includes(mode);this.mode=this.offered ? mode : 'device';this.ready=Wire.bool(data,'meeting_transcription_ready');
    this.glossary=Wire.strings(data,'meeting_glossary');this.extensionRef=Wire.text(data,'meeting_transcription_extension');
  }
  async open(owner:ClientModel):Promise<void> {
    this.close();this.owner=owner;this.generation=owner.connectionGeneration();this.scope=owner.scope();
    if (!owner.connected) return;
    const n=this.revision,repo=owner.repo();this.repo=repo;
    try {
      const data=await repo.call('/api/v1/assistant');if (!this.current(n)) return;
      this.apply(data);if (!this.offered) return;
      const plugins=await repo.call(owner.features.includes('extensions') ? '/api/v1/extensions' : '/api/v1/plugins');
      if (this.current(n)) this.choices=meetingSpeechServices(Wire.has(plugins,'extensions') ? Wire.rows(plugins,'extensions') : Wire.rows(plugins,'plugins'));
    } catch (_) { if (this.current(n) && !this.offered) this.error='failed'; }
  }
  serviceName():string {
    if (!this.extensionRef) return '';
    return this.choices.find((value:MeetingSpeechService) => value.ref===this.extensionRef)?.name || this.extensionRef.split('/')[0];
  }
  private previous(field:string):Object {
    if (field==='meeting_transcription_default') return this.mode;
    if (field==='meeting_glossary') return this.glossary;
    return this.extensionRef;
  }
  async choose(field:string,raw:Object):Promise<boolean> {
    const repo=this.repo,owner=this.owner,n=this.revision,path='/api/v1/assistant';
    if (!repo || !owner || this.saving || !this.offered || !this.current(n)) return false;
    let value:Object=raw;
    if (field==='meeting_glossary') {
      if (!Array.isArray(raw) || !(raw as Object[]).every((s:Object) => typeof s==='string')) return false;
      const clean=meetingGlossary(raw as string[]); if (!clean) { this.error='meeting_glossary_limit'; return false; } value=clean;
    } else if (field==='meeting_transcription_default') {
      if (typeof raw!=='string' || !['device','hub'].includes(raw as string)) return false;
    } else if (field==='meeting_transcription_extension') {
      if (typeof raw!=='string' || !this.choices.some((c:MeetingSpeechService) => c.ref===raw)) return false;
    } else return false;
    const expected=JSON.stringify(this.previous(field)), requested=JSON.stringify(value);
    if (expected===requested) return true;
    if (owner.operationBlocked(path)) { this.error='pending_action';return false; }
    this.saving=true;this.error='';
    try {
      const latest=await repo.call(path);if (!this.current(n)) return false;
      this.apply(latest);
      if (!this.offered || JSON.stringify(this.previous(field))!==expected) { this.error='request_updated';return false; }
      await repo.call(path,'PUT',Wire.payload([field],[value]),'',() => this.current(n));
      if (!this.current(n)) return false;
      const confirmed=await repo.call(path); if (!this.current(n)) return false;
      this.apply(confirmed);if (!this.offered || JSON.stringify(this.previous(field))!==requested) { this.error='request_updated';return false; }
      return true;
    } catch (error) {
      if (this.current(n)) this.error=Wire.bool(error as Object as Fields,'resultUnknown') ? 'result_unknown' : 'failed';
      return false;
    } finally { if (this.revision===n) this.saving=false; }
  }
}
