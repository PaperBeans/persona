import { nativeEnvironment } from '../platform/NativeEnvironment';
import { PhoneStyle } from './PhoneStyle';
import { Tokens } from './Tokens';

// Capped interface typography stays independent of the user's transcript size.
export class ReadinessStyle {
  static readonly label: number = 12;
  static readonly body: number = 15;
  static metric(regular: number, largest: number): number {
    return PhoneStyle.formSize(regular, largest, nativeEnvironment.fontScale);
  }
  static line(regular: number, largest: number): string {
    return PhoneStyle.settingsExampleLineHeight(regular, largest, nativeEnvironment.fontScale);
  }
  static font(points: number): string { return Tokens.interfaceFontVp(points, nativeEnvironment.fontScale); }
  static shadow(ui:UIContext):ShadowOptions {
    // ArkUI uses physical pixels for shadow radius and offset, including on a 3x phone.
    return PhoneStyle.surfaceShadow({radius:ui.vp2px(ReadinessStyle.metric(24,27)),
      offsetY:ui.vp2px(ReadinessStyle.metric(6,9)),color:nativeEnvironment.dark ? '#2C000000' : '#1A000000'});
  }
  static get choiceInset(): number { return ReadinessStyle.metric(7, 4); }
}
