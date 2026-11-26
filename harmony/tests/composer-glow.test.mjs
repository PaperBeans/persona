import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

const source = readFileSync(new URL('../entry/src/main/ets/components/PhoneComposerGlow.ets', import.meta.url), 'utf8');
const methods = source.slice(source.indexOf('  private schedule():'), source.indexOf('  build()'));
const colors = source.slice(source.indexOf('export const COMPOSER_GLOW_OUTSET'), source.indexOf('/**')).replace('export ', '');

function harness(reduced) {
  const masks=[], phases=[], callbacks=new Map();
  let timer=0, now=251;
  class Path {
    arcs=[];
    moveTo(...args){this.start=args;}
    arcTo(...args){this.arcs.push(args);}
    closePath(){}
  }
  class Mask {
    constructor(w,h){this.size=[w,h];this.steps=[];masks.push(this);}
    stroke(path){this.path=path;this.steps.push(['stroke',this.lineWidth,this.filter,this.globalAlpha]);}
    fill(){this.steps.push(['fill',this.globalCompositeOperation]);}
    transferToImageBitmap(){
      this.image={closed:false,close(){this.closed=true;}};
      return this.image;
    }
  }
  const ctx={ highlights:[], drawMasks:[], blends:[],
    createConicGradient(phase){phases.push(phase);return {addColorStop(){}};},
    drawImage(mask){this.drawMasks.push(mask);},fillRect(){this.blends.push(this.globalCompositeOperation);},
    clearRect(){}, stroke(path){this.highlights.push(path);}
  };
  const Rim=new Function('Date','setInterval','clearInterval','Path2D','OffscreenCanvasRenderingContext2D','RenderingContextSettings',
    stripTypeScriptTypes(colors+'\nclass Rim {\n'+methods+'}\n')+'\nreturn Rim;')(
    {now:()=>now},fn=>{callbacks.set(++timer,fn);return timer;},id=>callbacks.delete(id),Path,Mask,class {});
  const rim=new Rim();
  Object.assign(rim,{ctx,ready:true,visible:true,reduced,timer:-1,frameWidth:426,frameHeight:100,env:{dark:false,foreground:true}});
  return {rim,ctx,masks,phases,callbacks,advance(){now+=370;callbacks.forEach(fn=>fn());}};
}

test('the outside mask clears the composer interior and restores a sharp two-point edge',()=>{
  const {rim,masks,ctx}=harness(true);rim.schedule();
  assert.equal(masks.length,1);
  assert.deepEqual(masks[0].steps,[['stroke',12,'blur(8vp)',.55],['stroke',5,'blur(3vp)',.8],
    ['fill','destination-out'],['stroke',2,'none',1]]);
  assert.deepEqual(masks[0].path.start,[50,24]);assert.deepEqual(masks[0].path.arcs[0],[402,24,402,76,26]);
  assert.deepEqual(ctx.blends,['source-in']);assert.equal(ctx.globalCompositeOperation,'source-over');
});

test('a fixed mask serves both schemes and moving frames; reduced motion stays stationary',()=>{
  const h=harness(false);h.rim.schedule();h.advance();
  assert.equal(h.masks.length,1);assert.notEqual(h.phases[0],h.phases[2]);
  assert.equal(h.ctx.highlights.length,2);assert.equal(h.callbacks.size,1);
  h.rim.env.dark=true;h.rim.reduced=true;h.rim.schedule();h.advance();h.rim.paint();
  assert.equal(h.callbacks.size,0);assert.equal(h.ctx.highlights.length,2);assert.deepEqual(h.phases.slice(-2),[0,0]);
  assert.equal(h.masks.length,1);assert.ok(h.ctx.drawMasks.every(m=>m===h.masks[0].image));
});

test('size changes release the old mask, offscreen or background compositors do no work',()=>{
  const h=harness(false);h.rim.schedule();
  h.rim.dropMask();h.rim.frameHeight=123;h.rim.paint();
  assert.ok(h.masks[0].image.closed);assert.equal(h.masks.length,2);assert.equal(h.masks[1].path.arcs[0][4],32);
  h.rim.env.foreground=false;h.rim.schedule();h.rim.paint();assert.equal(h.callbacks.size,0);assert.equal(h.ctx.drawMasks.length,2);
  h.rim.env.foreground=true;h.rim.visible=false;h.rim.schedule();h.rim.paint();assert.equal(h.ctx.drawMasks.length,2);
  h.rim.dropMask();assert.ok(h.masks[1].image.closed);
});
