import { accessibility } from '@kit.AccessibilityKit';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

export const COMPOSER_GLOW_OUTSET: number = 24;
const LIGHT: string[] = ['#2E6FD0', '#5856D6', '#7A5AF8', '#D62E5C'];
const DARK: string[] = ['#5B9BFF', '#5E5CE6', '#9D8BFF', '#FF6B8B'];

/** A three-second angular sweep around the inline composer. Reduce Motion leaves a stationary colored rim. */
@ComponentV2
export struct PhoneComposerGlow {
  @Param barWidth: number = 0;
  @Param barHeight: number = 0;
  @Local env: NativeEnvironment = nativeEnvironment;
  private ctx: CanvasRenderingContext2D = new CanvasRenderingContext2D(new RenderingContextSettings(true));
  private ready: boolean = false;
  private visible: boolean = true;
  private reduced: boolean = false;
  private timer: number = -1;
  private frameWidth: number = 0;
  private frameHeight: number = 0;
  private haloMask: ImageBitmap | undefined = undefined;
  private rimPath: Path2D | undefined = undefined;
  private readonly animationChanged = (value: boolean): void => {
    this.reduced = value;
    this.schedule();
  };

  aboutToAppear(): void {
    this.reduced = this.env.reduceMotion;
    try { accessibility.onAnimationReduceStateChange(this.animationChanged); } catch (_) {}
  }

  aboutToDisappear(): void {
    this.ready = false;
    this.schedule();
    this.dropMask();
    try { accessibility.offAnimationReduceStateChange(this.animationChanged); } catch (_) {}
  }

  @Monitor('env.foreground', 'env.dark') refresh(): void { this.schedule(); }

  private schedule(): void {
    if (this.timer >= 0) {
      clearInterval(this.timer);
      this.timer = -1;
    }
    if (!this.ready || !this.visible || !this.env.foreground) return;
    this.paint();
    if (!this.reduced) this.timer = setInterval(() => this.paint(), 40);
  }

  private dropMask(): void {
    this.haloMask?.close();
    this.haloMask = undefined;
    this.rimPath = undefined;
  }

  private makeMask(w: number, h: number): void {
    const inset = COMPOSER_GLOW_OUTSET;
    const r = Math.min(32, (w - 2 * inset) / 2, (h - 2 * inset) / 2);
    const edge = new Path2D();
    edge.moveTo(inset + r, inset);
    edge.arcTo(w - inset, inset, w - inset, h - inset, r);
    edge.arcTo(w - inset, h - inset, inset, h - inset, r);
    edge.arcTo(inset, h - inset, inset, inset, r);
    edge.arcTo(inset, inset, w - inset, inset, r);
    edge.closePath();

    // Only the edge and outer halo receive the moving gradient. Blur the two masks once per size.
    const mask = new OffscreenCanvasRenderingContext2D(w, h, new RenderingContextSettings(true));
    mask.strokeStyle = '#FFFFFF';
    mask.filter = 'blur(8vp)';
    mask.globalAlpha = .55;
    mask.lineWidth = 12;
    mask.stroke(edge);
    mask.filter = 'blur(3vp)';
    mask.globalAlpha = .8;
    mask.lineWidth = 5;
    mask.stroke(edge);
    mask.filter = 'none';
    mask.globalAlpha = 1;
    mask.globalCompositeOperation = 'destination-out';
    mask.fill(edge);
    mask.globalCompositeOperation = 'source-over';
    mask.lineWidth = 2;
    mask.stroke(edge);
    this.haloMask = mask.transferToImageBitmap();
    this.rimPath = edge;
  }

  private paint(): void {
    const ctx = this.ctx, w = this.frameWidth, h = this.frameHeight;
    if (!this.ready || !this.visible || !this.env.foreground ||
      !(w > 2 * COMPOSER_GLOW_OUTSET && h > 2 * COMPOSER_GLOW_OUTSET)) return;
    if (!this.haloMask) this.makeMask(w, h);
    if (!this.haloMask || !this.rimPath) return;
    const colors = this.env.dark ? DARK : LIGHT;
    const angle = this.reduced ? 0 : Date.now() % 3000 / 3000 * 2 * Math.PI;
    const color = ctx.createConicGradient(angle, w / 2, h / 2);
    colors.forEach((value: string, index: number) => color.addColorStop(index / 4, value));
    color.addColorStop(1, colors[0]);
    ctx.clearRect(0, 0, w, h);
    ctx.drawImage(this.haloMask, 0, 0, w, h);
    ctx.globalCompositeOperation = 'source-in';
    ctx.fillStyle = color;
    ctx.fillRect(0, 0, w, h);
    ctx.globalCompositeOperation = 'source-over';
    if (!this.reduced) {
      const highlight = ctx.createConicGradient(angle, w / 2, h / 2);
      highlight.addColorStop(0, '#00FFFFFF');
      highlight.addColorStop(.42, '#00FFFFFF');
      highlight.addColorStop(.5, this.env.dark ? '#73FFFFFF' : '#D9FFFFFF');
      highlight.addColorStop(.58, '#00FFFFFF');
      highlight.addColorStop(1, '#00FFFFFF');
      ctx.strokeStyle = highlight;
      ctx.lineWidth = 2;
      ctx.stroke(this.rimPath);
    }
  }

  build() {
    Canvas(this.ctx).width(this.barWidth + 2 * COMPOSER_GLOW_OUTSET).height(this.barHeight + 2 * COMPOSER_GLOW_OUTSET).hitTestBehavior(HitTestMode.None).accessibilityLevel('no')
      .onReady(() => { this.ready = true; this.schedule(); })
      .onAreaChange((_: Area, area: Area) => {
        const width = Number(area.width), height = Number(area.height);
        if (width === this.frameWidth && height === this.frameHeight) return;
        this.dropMask();
        this.frameWidth = width;
        this.frameHeight = height;
        this.paint();
      })
      .onVisibleAreaChange([0], (visible: boolean, ratio: number) => {
        this.visible = visible || ratio > 0;
        this.schedule();
      }).id('chat.reply.glow')
  }
}
