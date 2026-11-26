export interface MessageLiftFrame {
  left: number;
  top: number;
  width: number;
  height: number;
  press: number;
  fontScale: number;
}

export interface MessageLiftPlacement {
  top: number;
  height: number;
  offset: number;
  barTop: number;
}

/** Keep the pressed words in place, moving the action bar above them when the space below is too small. */
export function placeMessageLift(frame: MessageLiftFrame, safeTop: number, safeBottom: number, barHeight: number): MessageLiftPlacement {
  const margin = 8, gap = 8;
  const top = safeTop + margin;
  const bottom = Math.max(top, safeBottom - margin);
  const height = Math.min(frame.height, Math.max(1,bottom-top-gap-barHeight));
  const offset = Math.min(Math.max(0,frame.press-height/2),Math.max(0,frame.height-height));
  let y = Math.min(Math.max(frame.top+offset,top),bottom-height);
  let below = y+height+gap+barHeight <= bottom;
  if (!below && y-gap-barHeight < top) {
    below = true;
    y = Math.max(top,bottom-barHeight-gap-height);
  }
  return { top:y,height:height,offset:offset,barTop:below ? y+height+gap : y-gap-barHeight };
}
