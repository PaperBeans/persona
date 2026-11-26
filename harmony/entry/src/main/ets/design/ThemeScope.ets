import { Theme } from '@kit.ArkUI';
import { Colors } from './Colors';
import { PhoneStyleSurface } from '../core/DeviceLook';

/** Explicit attributes use resolved theme tokens rather than global system resource IDs. */
@ComponentV2
export struct ThemeScope {
  @Param systemControls: boolean = false;
  @Param selectedAccentInk: string = '';
  @Param styleID: string = 'glass';
  @Param interfaceAccent: string = '#007AFF';
  @Param surfaces: PhoneStyleSurface = { page: '', card: '', edge: '', shadow: '' };
  @BuilderParam content: () => void;
  aboutToAppear(): void { this.controlAppearanceChanged(); }
  @Monitor('systemControls', 'selectedAccentInk', 'styleID', 'surfaces', 'interfaceAccent') controlAppearanceChanged(): void {
    Colors.systemControls = this.systemControls;
    Colors.selectedAccentInk = this.selectedAccentInk;
    Colors.styleID = this.styleID;
    Colors.interfaceAccent = this.interfaceAccent;
    Colors.stylePage = this.surfaces.page;
    Colors.styleCard = this.surfaces.card;
    Colors.styleEdge = this.surfaces.edge;
    Colors.styleShadow = this.surfaces.shadow;
  }
  onWillApplyTheme(theme: Theme): void {
    Colors.action = theme.colors.compBackgroundEmphasize;
    Colors.onAction = theme.colors.fontOnPrimary;
    Colors.link = theme.colors.fontEmphasize;
    Colors.iconAccent = theme.colors.iconEmphasize;
  }
  build() { this.content() }
}
