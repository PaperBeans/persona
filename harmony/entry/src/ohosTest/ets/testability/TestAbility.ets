import { UIAbility } from '@kit.AbilityKit';
import { abilityDelegatorRegistry } from '@kit.TestKit';
import { Hypium } from '@ohos/hypium';
import nativeTransportTests, { fixtureArgument, nativeIPv6TransportTests } from '../test/NativeTransport.test';
import nativeStorageTests from '../test/NativeStorage.test';
import nativeRFBTests from '../test/NativeRFB.test';
import nativePreviewTests from '../test/NativePreview.test';
import nativeFontTests from '../test/NativeFont.test';
import nativeSshTests from '../test/NativeSsh.test';
import nativeRecordingTests from '../test/NativeRecording.test';
import nativeMeetingAudioTests from '../test/NativeMeetingAudio.test';
import nativeAlarmsTests from '../test/NativeAlarms.test';
import nativeMeetingServicesTests from '../test/NativeMeetingServices.test';
import nativePhoneTests from '../test/NativePhone.test';
import nativeCoreTests from '../test/NativeCore.test';
import { bindNativeTestContext } from '../test/NativeContext';

export default class TestAbility extends UIAbility {
  onCreate(): void {
    bindNativeTestContext(this.context);
    const testArguments = abilityDelegatorRegistry.getArguments();
    if (fixtureArgument('fontScale')) testArguments.parameters['-s timeout'] = '150000';
    if (fixtureArgument('sshFixtureFile')) testArguments.parameters['-s timeout'] = '60000';
    if (fixtureArgument('sshRecheck') === '1') testArguments.parameters['-s timeout'] = '175000';
    if (fixtureArgument('recordingFixtureId')) testArguments.parameters['-s timeout'] = '120000';
    if (fixtureArgument('phoneCheck')) testArguments.parameters['-s timeout'] = '90000';
    Hypium.hypiumTest(abilityDelegatorRegistry.getAbilityDelegator(), testArguments, (): void => {
      if (fixtureArgument('coreReplay')) nativeCoreTests();
      else if (fixtureArgument('ipv6FixturePort')) nativeIPv6TransportTests();
      else if (fixtureArgument('fontScale')) nativeFontTests();
      else if (fixtureArgument('sshFixtureFile')) nativeSshTests();
      else if (fixtureArgument('meetingAudioFixture')==='synthetic') nativeMeetingAudioTests();
      else if (fixtureArgument('alarmFixture')==='synthetic') nativeAlarmsTests();
      else if (fixtureArgument('meetingReminderFixture')==='synthetic') nativeMeetingServicesTests();
      else if (fixtureArgument('recordingFixtureId')) nativeRecordingTests();
      else if (fixtureArgument('phoneCheck')) nativePhoneTests();
      else { nativeStorageTests(); nativeRFBTests(); if (fixtureArgument('fixtureAddress')) nativeTransportTests(); if (fixtureArgument('previewCheck')) nativePreviewTests(); }
    });
  }
}
