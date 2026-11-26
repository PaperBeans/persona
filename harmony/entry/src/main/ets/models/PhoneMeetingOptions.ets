export interface MeetingInputChoice { readonly id:number;readonly bluetooth:boolean;readonly name:string; }

export function meetingDefaultTitle(kind:string,date:Date,language:string,meeting:string,note:string):string {
  const locale=language || undefined;
  const day=date.toLocaleDateString(locale,{year:'numeric',month:'short',day:'numeric'});
  const cjk=/^(zh|ja)(-|_|$)/i.test(language);
  const clock=cjk ? String(date.getHours()).padStart(2,'0')+':'+String(date.getMinutes()).padStart(2,'0') :
    date.toLocaleTimeString(locale,{hour:'numeric',minute:'2-digit'});
  return (kind==='note' ? note : meeting)+' · '+day+' '+clock;
}

/** Fit the complete date and minutes in the field, whose own native inset differs across phone widths. */
export function meetingPlaceholderVp(width:number,measured:number,preferred:number):string {
  const fitted=width>12 && measured>0 ? Math.min(preferred,(width-12)*preferred/measured) : Math.min(16,preferred);
  return String(Math.max(10,Math.floor(fitted*10)/10))+'vp';
}

// UIFontMetrics body values used by the recording hero's scaled 44pt label.
// The prominent button adds seven points above and below that label.
const recordingBodyMetrics: number[][] = [
  [14, 14], [15, 46/3], [16, 49/3], [17, 17], [19, 56/3], [21, 20], [23, 67/3]
];
export function meetingCaptureHeight(fontScale: number, pageHeight: number): number {
  const points = Math.max(14, Math.min(23, 17 * (fontScale > 0 ? fontScale : 1)));
  let index = 1;
  while (index < recordingBodyMetrics.length-1 && points > recordingBodyMetrics[index][0]) index++;
  const lower = recordingBodyMetrics[index-1], upper = recordingBodyMetrics[index];
  const body = lower[1] + (upper[1]-lower[1]) * (points-lower[0]) / (upper[0]-lower[0]);
  const preferred = 44 * body / 17 + 14;
  // Leave a tappable row in the independently scrollable form above the footer.
  // The page insets, footer insets and spacing occupy another 44vp.
  return pageHeight > 0 ? Math.max(44, Math.min(preferred, pageHeight-88)) : preferred;
}

/** The recordings destination owns its draft. An open header menu may only change that same, idle draft. */
@ObservedV2
export class PhoneMeetingOptions {
  @Trace available:boolean=false;
  @Trace allowsPrivate:boolean=false;
  @Trace marked:boolean=false;
  @Trace reading:boolean=false;
  @Trace languageAvailable:boolean=false;
  @Trace inputs:MeetingInputChoice[]=[];
  @Trace selectedInput:number=-1;
  @Trace problem:boolean=false;
  private owner:Object|undefined;
  private epoch:number=0;
  private permitted:()=>boolean=()=>false;
  private setPrivate:(value:boolean)=>void=()=>{};
  private readLanguages:()=>Promise<string[]>=async()=>[];
  private readInputs:()=>MeetingInputChoice[]=()=>[];
  bind(owner:Object,idle:boolean,allowsPrivate:boolean,marked:boolean,permitted:()=>boolean,
    setPrivate:(value:boolean)=>void,readLanguages:()=>Promise<string[]>,readInputs:()=>MeetingInputChoice[]):void {
    const first=this.owner!==owner;
    if (first) { this.reset();this.owner=owner; }
    this.available=idle;this.allowsPrivate=allowsPrivate;this.marked=marked;
    this.permitted=permitted;this.setPrivate=setPrivate;this.readLanguages=readLanguages;this.readInputs=readInputs;
    if (first) this.prepare();
  }
  private reset():void {
    this.epoch++;this.owner=undefined;this.available=false;this.allowsPrivate=false;this.marked=false;this.reading=false;
    this.problem=false;this.languageAvailable=false;this.inputs=[];this.selectedInput=-1;this.permitted=()=>false;this.setPrivate=()=>{};this.readLanguages=async()=>[];this.readInputs=()=>[];
  }
  clear(owner:Object):void { if (this.owner===owner) this.reset(); }
  togglePrivate():void { if (this.available && this.allowsPrivate && this.permitted()) { this.marked=!this.marked;this.setPrivate(this.marked); } }
  selectInput(id:number):void {
    if (!this.available || !this.permitted()) return;
    const available=this.readInputs();
    this.inputs=available;
    if (id===-1 || available.some((input:MeetingInputChoice)=>input.id===id)) this.selectedInput=id;
  }
  async prepare():Promise<void> {
    if (!this.available || !this.permitted()) return;
    this.inputs=this.readInputs();
    if (!this.inputs.some((input:MeetingInputChoice)=>input.id===this.selectedInput)) this.selectedInput=-1;
    if (this.reading || this.languageAvailable) return;
    const n=++this.epoch;this.reading=true;this.problem=false;
    try {
      const locales=await this.readLanguages();
      if (this.epoch===n && this.permitted()) this.languageAvailable=locales.includes('zh-CN');
    } catch (_) { if (this.epoch===n && this.permitted()) {this.problem=true;this.languageAvailable=false;} }
    finally { if (this.epoch===n) this.reading=false; }
  }
}
