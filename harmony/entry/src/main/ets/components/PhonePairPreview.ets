import { abilityAccessCtrl, common, Permissions } from '@kit.AbilityKit';
import { camera } from '@kit.CameraKit';
import { scanCore } from '@kit.ScanKit';
import { PhoneStyle } from '../design/PhoneStyle';
import { tr } from '../design/Strings';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { phonePairScanner } from '../platform/PhonePairScanner';

@ComponentV2
export struct PhonePairPreview {
  @Param active: boolean = true;
  @Event onCode: (value: string) => void = () => {};
  @Local private environment: NativeEnvironment = nativeEnvironment;
  @Local private available: boolean = false;
  private controller: XComponentController = new XComponentController();
  private lifetime: number = 0;
  private surfaceId: string = '';
  private widthVp: number = 0;
  private heightVp: number = 0;
  private visible: boolean = false;
  private owner: number = 0;

  aboutToAppear(): void {
    const lifetime = ++this.lifetime;
    this.checkCamera(lifetime).catch(() => {});
  }

  aboutToDisappear(): void {
    this.lifetime++;
    this.surfaceId = '';
    this.stop();
  }

  private async checkCamera(lifetime: number): Promise<void> {
    const context = this.getUIContext().getHostContext() as common.UIAbilityContext;
    if (!context || !canIUse('SystemCapability.Multimedia.Scan.ScanBarcode') ||
      !scanCore.isCustomScanSupported() || !camera.getCameraManager(context).getSupportedCameras().length) return;
    const permission: Permissions = 'ohos.permission.CAMERA';
    const manager = abilityAccessCtrl.createAtManager();
    if (manager.getSelfPermissionStatus(permission) === abilityAccessCtrl.PermissionStatus.NOT_DETERMINED) {
      await manager.requestPermissionsFromUser(context, [permission]);
    }
    if (this.lifetime === lifetime) this.available = manager.getSelfPermissionStatus(permission) === abilityAccessCtrl.PermissionStatus.GRANTED;
  }

  private stop(): void {
    if (this.owner) phonePairScanner.stop(this.owner);
    this.owner = 0;
  }

  @Monitor('active', 'environment.foreground')
  private updateStream(): void {
    if (!this.active || !this.environment.foreground || !this.visible || !this.surfaceId || !this.widthVp || !this.heightVp) {
      this.stop();
      return;
    }
    if (!this.owner) {
      this.owner = phonePairScanner.start({ surfaceId: this.surfaceId, width: this.widthVp, height: this.heightVp },
        (value: string) => { this.owner = 0; this.onCode(value); },
        () => { this.owner = 0; this.available = false; });
    }
  }

  build() {
    Column() {
      if (this.available) {
        XComponent({ id: 'pairCamera', type: XComponentType.SURFACE, controller: this.controller })
          .width('100%').height(PhoneStyle.formSize(280, 360, this.environment.fontScale))
          .borderRadius(20).clip(true).accessibilityText(tr('scan_qr')).id('pair.camera')
          .onLoad(() => { this.surfaceId = this.controller.getXComponentSurfaceId(); this.updateStream(); })
          .onDestroy(() => { this.surfaceId = ''; this.stop(); })
          .onAreaChange((_previous: Area, next: Area) => {
            const width = Number(next.width), height = Number(next.height);
            if (width === this.widthVp && height === this.heightVp) return;
            this.widthVp = width; this.heightVp = height; this.stop(); this.updateStream();
          })
          .onVisibleAreaChange([0, 0.01], (visible: boolean, ratio: number) => {
            this.visible = visible && ratio > 0; this.updateStream();
          })
        Text(tr('phone_pair_scan_info')).fontSize(PhoneStyle.formSize(13, 17, this.environment.fontScale) + 'vp')
          .lineHeight(PhoneStyle.formSize(16, 22, this.environment.fontScale) + 'vp')
          .fontColor($r('app.color.project_card_secondary')).width('100%').padding({ left: 20, right: 20, top: 7, bottom: 7 })
      }
    }.width('100%').margin({ top: this.available ? 17.33 : 0 })
  }
}
