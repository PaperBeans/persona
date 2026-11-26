// The developer chat keeps a short navigation row while the reader moves toward the latest messages.
@ObservedV2
export class AgentReadingHeader {
  static readonly slimHeight:number=40;
  @Trace height:number=0;
  @Trace collapse:number=0;
  private owner:string='';
  private dragging:boolean=false;
  private timer:number=-1;
  private overlays:Set<string>=new Set();
  private covered:() => boolean=():boolean=>false;
  private change:(value:number) => void=(value:number):void=>{this.collapse=value;};

  get limit():number { return Math.max(0,this.height-AgentReadingHeader.slimHeight); }
  get progress():number { return this.limit>0 ? Math.min(1,this.collapse/this.limit) : 0; }
  owns(owner:string):boolean { return !!owner && this.owner===owner; }

  attach(owner:string,covered:() => boolean,change:(value:number) => void):void {
    if (this.owner!==owner) {
      this.detach(this.owner); this.owner=owner; this.height=0; this.collapse=0;
    }
    this.covered=covered; this.change=change; this.interacted(owner);
  }
  detach(owner:string):void {
    if (this.owner!==owner) return;
    clearTimeout(this.timer); this.timer=-1; this.owner=''; this.dragging=false; this.overlays.clear();
    this.covered=():boolean=>false; this.change=(value:number):void=>{this.collapse=value;};
  }
  measure(owner:string,height:number):void {
    if (!this.owns(owner) || !Number.isFinite(height) || height<=AgentReadingHeader.slimHeight || Math.abs(height-this.height)<0.1) return;
    const percent=this.progress;
    this.height=height; this.collapse=percent*this.limit;
  }
  private schedule(owner:string,ms:number):void {
    clearTimeout(this.timer);
    this.timer=setTimeout(() => {
      if (!this.owns(owner) || this.dragging || this.collapse>0) return;
      if (this.overlays.size || this.covered() || !this.limit) this.schedule(owner,500);
      else this.change(this.limit);
    },ms);
  }
  interacted(owner:string):void {
    if (this.owns(owner) && !this.dragging && !this.collapse) this.schedule(owner,3000);
  }
  overlay(owner:string,key:string,shown:boolean):void {
    if (!this.owns(owner)) return;
    if (shown) this.overlays.add(key); else this.overlays.delete(key);
    this.interacted(owner);
  }
  reveal(owner:string):void {
    if (!this.owns(owner)) return;
    this.dragging=false; this.change(0); this.schedule(owner,3000);
  }
  scroll(owner:string,offset:number,glide:boolean):void {
    if (!this.owns(owner) || !offset || !this.limit || this.overlays.size || this.covered() || (glide && !this.dragging)) return;
    clearTimeout(this.timer); this.dragging=true;
    this.collapse=Math.max(0,Math.min(this.limit,this.collapse+offset));
  }
  settle(owner:string,nearLatest:boolean):void {
    if (!this.owns(owner) || !this.dragging) return;
    this.dragging=false;
    if (this.overlays.size || this.covered()) { this.interacted(owner); return; }
    const target=nearLatest || this.collapse>this.limit/2 ? this.limit : 0;
    if (target!==this.collapse) this.change(target);
    if (!target) this.schedule(owner,3000);
  }
  jumped(owner:string):void {
    if (this.owns(owner) && !this.overlays.size && !this.covered()) {
      this.dragging=false; clearTimeout(this.timer); this.change(this.limit);
    }
  }
}
