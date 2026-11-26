// The physical two-finger scroll curve shared with the iPhone VNCWheel.
export const screenScrollChoices:string[]=['slower','default','faster'];

export function screenScrollFactor(choice:string):number {
  return choice==='slower' ? .6 : choice==='faster' ? 1.6 : 1;
}

const maxRate=2000, backlog=200, decay=.998, stopSpeed=30;
function rounded(value:number):number { return Math.sign(value)*Math.round(Math.abs(value)); }
function gain(velocity:number):number { return 1+3*Math.max(0,Math.min(1,(Math.abs(velocity)-250)/1750)); }

export class ScreenWheel {
  private total:number=0;
  private paced:number=0;
  private sent:number=0;
  private speed:number=0;
  get busy():boolean { return this.speed!==0 || rounded(this.total)!==this.sent; }

  begin():void { this.total=0;this.paced=0;this.sent=0;this.speed=0; }
  stop():void { this.total=this.sent;this.paced=this.sent;this.speed=0; }
  private hold():void {
    this.total=Math.max(this.sent-backlog,Math.min(this.sent+backlog,this.total));
    this.paced=Math.max(this.sent-backlog,Math.min(this.sent+backlog,this.paced));
  }
  move(dy:number,velocity:number,perPoint:number):void {
    this.total+=dy*gain(velocity)*Math.max(0,perPoint);this.hold();
  }
  release(velocity:number,perPoint:number):void {
    if (Math.abs(velocity)<250) return;
    const velocityInNotches=velocity*gain(velocity)*Math.max(0,perPoint);
    this.speed=Math.abs(velocityInNotches)<stopSpeed ? 0 : velocityInNotches;
  }
  frame(dt:number):number {
    if (!Number.isFinite(dt) || dt<=0) return 0;
    if (this.speed) {
      const v0=Math.abs(this.speed),v1=v0*Math.pow(decay,dt*1000),tau=-1/Math.log(decay)/1000;
      const distance=v0<=maxRate ? (v0-v1)*tau : v1>=maxRate ? maxRate*dt :
        maxRate*tau*Math.log(v0/maxRate)+(maxRate-v1)*tau;
      this.total+=Math.sign(this.speed)*distance;
      this.speed=v1<stopSpeed ? 0 : Math.sign(this.speed)*v1;this.hold();
    }
    this.paced+=(this.total-this.paced)*(1-Math.exp(-dt/.012));
    if (Math.abs(this.total-this.paced)<.05) this.paced=this.total;
    const most=Math.max(1,Math.round(maxRate*dt));
    const count=Math.max(-most,Math.min(most,rounded(this.paced)-this.sent));
    this.sent+=count;return count;
  }
}

export class ScreenTwoFingers {
  private originX:number=0;
  private originY:number=0;
  private originSpread:number=0;
  private meaning:string='';
  begin(x:number,y:number,spread:number):void {
    this.originX=x;this.originY=y;this.originSpread=spread;this.meaning='';
  }
  moved(x:number,y:number,spread:number):string {
    if (this.meaning) return this.meaning;
    const opened=Math.abs(spread-this.originSpread),travel=Math.hypot(x-this.originX,y-this.originY);
    if (opened>=Math.max(this.originSpread*.15,10) && opened>travel) this.meaning='pinch';
    else if (travel>=6 && travel>=opened) this.meaning='scroll';
    return this.meaning;
  }
}
