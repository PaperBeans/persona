import { Colors } from './Colors';

interface GlyphDrawing {
  name: string;
  width: number;
  height: number;
  path: string;
  pen: number;
  filled?: boolean;
}

const cube = 'M12 1.2 L21.5 6.6 L21.5 17.4 L12 22.8 L2.5 17.4 L2.5 6.6 Z M2.5 6.6 L12 12 L21.5 6.6 M12 12 L12 22.8';
const sparkles =
  'M11.2 8.02 Q11.93 15.42 19.33 16.15 Q11.93 16.88 11.2 24.28 Q10.47 16.88 3.07 16.15 Q10.47 15.42 11.2 8.02 Z ' +
  'M4 4.87 Q4.28 8.32 7.73 8.6 Q4.28 8.88 4 12.33 Q3.72 8.88 0.27 8.6 Q3.72 8.32 4 4.87 Z ' +
  'M9.25 0.47 Q9.42 2.53 11.48 2.7 Q9.42 2.87 9.25 4.93 Q9.08 2.87 7.02 2.7 Q9.08 2.53 9.25 0.47 Z';
const drawings: GlyphDrawing[] = [
  {name:'dispatchToAgent',width:18,height:18,pen:2.1,path:'M2.1 3.8 V6.1 Q2.1 10.0 6.2 10.0 H15.4 M10.5 5.4 L15.4 10.0 L10.5 14.6'},
  {name:'beautifulSparkle',width:24,height:24,path:'M12 2l2.4 7.2L22 12l-7.6 2.8L12 22l-2.4-7.2L2 12l7.6-2.8z',pen:0,filled:true},
  {name:'networkShield',width:25,height:25,pen:1.45,path:'M16 20.5 A9.8 9.8 0 1 1 20 12 M2 10.6 H20 M4 5.4 Q11 9 18 5.4 M4.4 16.7 Q8.2 14.6 12.8 15 M10.7 1.5 Q2.7 11 11.3 21.1 M11 1.5 Q15.5 6.1 15.2 11.4 M11 1.9 V20.5 M18.5 12.5 Q20.9 14.1 23.6 14.2 V17.7 Q23 21.3 18.5 23.8 Q14 21.3 13.5 17.7 V14.2 Q16.1 14.1 18.5 12.5 Z M18.5 14.8 V21.4'},
  {name:'cube',width:24,height:24,path:cube,pen:1.5},
  {name:'package',width:24,height:24,path:cube+' M7.2 4 L16.8 9.4',pen:1.5},
  {name:'laptop',width:36,height:22,path:'M4.7 17.8 V4.4 Q4.7 3 6.2 3 H29.8 Q31.3 3 31.3 4.4 V17.8 Z M2.5 19 H33.5',pen:2.2},
  {name:'server',width:29,height:22,path:'M3.5 2 H25.5 Q26.5 2 26.5 3 V19 Q26.5 20 25.5 20 H3.5 Q2.5 20 2.5 19 V3 Q2.5 2 3.5 2 Z M2.5 8 H26.5 M2.5 14 H26.5 M21.5 5 H23 M21.5 11 H23 M21.5 17 H23',pen:2},
  {name:'desktop',width:30,height:24,path:'M3 1.8 H27 Q28 1.8 28 3 V18 H2 V3 Q2 1.8 3 1.8 Z M2 15 H28 M15 18 V21.8 M10 22 H20',pen:2},
  {name:'sparkles',width:20,height:24.67,path:sparkles,pen:0.45,filled:true},
  {name:'inlinePickerCheck',width:15,height:14.67,pen:2.45,path:'M1.23 7.55 L5.6 13.43 L13.77 1.23'},
  {name:'decisionEnlarge',width:12.33,height:12.33,pen:1.7,path:'M4.8 .85 H.85 V4.8 M.85 .85 L4.95 4.95 M7.38 7.38 L11.48 11.48 M11.48 7.53 V11.48 H7.53'},
  {name:'decisionChevron',width:11.33,height:6.67,pen:1.85,path:'M.93 .93 L5.67 5.74 L10.4 .93'},
  {name:'decisionClipboard',width:10.33,height:15.33,pen:0,filled:true,path:'M5.17 0 Q6.67 0 7.03 1.5 H8.56 Q10.33 1.5 10.33 3.35 V13.45 Q10.33 15.33 8.51 15.33 H1.83 Q0 15.33 0 13.45 V3.35 Q0 1.5 1.83 1.5 H3.3 Q3.68 0 5.17 0 Z M4.55 1.6 A.62 .62 0 1 0 5.79 1.6 A.62 .62 0 1 0 4.55 1.6 Z M1.8 4.9 V6.1 H3 V4.9 Z M4.1 4.9 V6.1 H8.55 V4.9 Z M1.8 7.5 V8.7 H3 V7.5 Z M4.1 7.5 V8.7 H8.55 V7.5 Z M1.8 10.1 V11.3 H3 V10.1 Z M4.1 10.1 V11.3 H8.55 V10.1 Z'},
  {name:'navigationBack',width:11,height:18.67,pen:2.6,path:'M9.7 1.3 L1.3 9.33 L9.7 17.37'},
  {name:'navigationClose',width:17,height:17,pen:2.5,path:'M1.25 1.25 L15.75 15.75 M15.75 1.25 L1.25 15.75'},
  {name:'forward',width:9,height:14,path:'M1.7 1.7 L6.9 7 L1.7 12.3',pen:1.9},
  {name:'calendarAgenda',width:27,height:20,path:'M7 1.8 H25 Q26 1.8 26 3 V17 Q26 18.2 24.8 18.2 H7 Q5.8 18.2 5.8 17 V3 Q5.8 1.8 7 1.8 Z M6 8 H26 M1.6 3 H2.3 M1.6 9 H2.3 M1.6 15 H2.3',pen:1.6},
  {name:'books',width:25,height:24,path:'M1.5 5 H6 V21.5 H1.5 Z M8.1 1.7 H13.2 V21.5 H8.1 Z M8.5 6 H12.8 M1.9 17 H5.6 M15.1 3.9 L19.4 2.9 L23.6 20.4 L19.3 21.5 Z',pen:1.6},
  {name:'choices',width:11,height:15,path:'M1.6 5.4 L5.5 1.6 L9.4 5.4 M1.6 9.6 L5.5 13.4 L9.4 9.6',pen:1.8},
];

// The SVG path commands and their pen use physical pixels within the viewport. The outer frame follows the interface size.
@ComponentV2
export struct PhoneOutlineIcon {
  @Param name: string = 'cube';
  @Param iconScale: number = 1;
  @Param color: ResourceColor = Colors.link;

  @Computed get drawing(): GlyphDrawing { return drawings.find((entry: GlyphDrawing) => entry.name === this.name) || drawings[0]; }

  build() {
    Shape() {
      Path().commands(this.drawing.path).fill(this.drawing.filled ? this.color : Color.Transparent).stroke(this.color)
        .strokeWidth(String(this.drawing.pen)+'px').strokeLineCap(LineCapStyle.Round).strokeLineJoin(LineJoinStyle.Round)
    }.viewPort({x:0,y:0,width:String(this.drawing.width)+'px',height:String(this.drawing.height)+'px'})
      .width(this.drawing.width*this.iconScale).height(this.drawing.height*this.iconScale)
      .accessibilityLevel('no')
  }
}
