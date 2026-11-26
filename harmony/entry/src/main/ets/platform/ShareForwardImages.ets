import { LengthMetricsUnit } from '@kit.ArkUI';
import { common } from '@kit.AbilityKit';
import { util } from '@kit.ArkTS';
import { fileIo, fileUri } from '@kit.CoreFileKit';
import { image } from '@kit.ImageKit';
import { systemShare } from '@kit.ShareKit';
import { uniformTypeDescriptor } from '@kit.ArkData';
import type { CompletedForward } from '../models/PhoneChatForward';
import { Wire } from '../models/Wire';

/** Compose private, bounded PNG pages. The system chooser controls whether anything leaves the device. */
export async function shareForwardImages(context:common.UIAbilityContext,ready:CompletedForward,valid:()=>boolean):Promise<void> {
  if(!context || !valid() || !ready.refs.length)throw new Error('request_updated');
  const dir=context.cacheDir+'/chat-export';
  if(!fileIo.accessSync(dir))fileIo.mkdirSync(dir,true);
  for(const name of fileIo.listFileSync(dir)){
    if(!/^[a-f\d-]+-\d+\.png$/i.test(name))continue;
    const path=dir+'/'+name;
    if(Date.now()-fileIo.statSync(path).mtime*1000>86400000)try{fileIo.unlinkSync(path);}catch(_){}
  }
  const width=1080,height=1552,margin=54,step=44;
  const canvas=new OffscreenCanvasRenderingContext2D(width,height,new RenderingContextSettings(true),LengthMetricsUnit.PX);
  const batch=util.generateRandomUUID(),paths:string[]=[];
  let top=margin,used=false,started=false;
  const reset=():void=>{canvas.fillStyle='#ffffff';canvas.fillRect(0,0,width,height);canvas.fillStyle='#191919';top=margin;used=false;};
  const page=async():Promise<void>=>{
    if(!used)return;if(!valid())throw new Error('request_updated');
    const h=Math.min(height,Math.ceil(top+margin)),pixels=canvas.getPixelMap(0,0,width,h),packer=image.createImagePacker();
    try{
      const png=await packer.packToData(pixels,{format:'image/png',quality:100});
      if(!valid())throw new Error('request_updated');
      const path=dir+'/'+batch+'-'+String(paths.length+1)+'.png';
      const f=fileIo.openSync(path,fileIo.OpenMode.CREATE|fileIo.OpenMode.WRITE_ONLY|fileIo.OpenMode.TRUNC);
      try{fileIo.writeSync(f.fd,png);}finally{fileIo.closeSync(f);}
      paths.push(path);
    }finally{await packer.release();await pixels.release();}
    reset();
  };
  const line=async(value:string):Promise<void>=>{
    if(top+step>height-margin)await page();
    canvas.fillText(value,margin,top+step*0.77);top+=step;used=true;
  };
  reset();
  try {
    for(const ref of ready.refs){
      if(!valid())throw new Error('request_updated');
      canvas.font='bold 34px sans-serif';await line(Wire.text(ref,'author'));
      canvas.font='30px sans-serif';
      for(const para of Wire.text(ref,'text').split('\n')){
        let current='';
        for(const ch of Array.from(para)){
          if(current && canvas.measureText(current+ch).width>width-2*margin){await line(current);current='';}
          current+=ch;
        }
        await line(current);
      }
      for(const photo of ready.images.filter(f=>f.item===Wire.text(ref,'msg'))){
        if(!valid())throw new Error('request_updated');
        const bytes=new util.Base64Helper().decodeSync(photo.data);
        const input=image.createImageSource(bytes.buffer as ArrayBuffer);
        const bitmap=await input.createPixelMap();
        try{
          const info=await bitmap.getImageInfo(),scale=Math.min((width-2*margin)/info.size.width,(height-2*margin)/info.size.height,1);
          const h=info.size.height*scale;
          if(top+h>height-margin)await page();
          canvas.drawImage(bitmap,margin,top,info.size.width*scale,h);top+=h+12;used=true;
        }finally{await bitmap.release();await input.release();}
      }
      top+=24;
    }
    await page();if(!valid() || !paths.length)throw new Error('request_updated');
    const record=(path:string):systemShare.SharedRecord=>({utd:uniformTypeDescriptor.UniformDataType.PNG,uri:fileUri.getUriFromPath(path)});
    const records=new systemShare.SharedData(record(paths[0]));
    for(const path of paths.slice(1))records.addRecord(record(path));
    await new systemShare.ShareController(records).show(context,{});started=true;
  }finally{if(!started)for(const path of paths)try{fileIo.unlinkSync(path);}catch(_){}}
}
