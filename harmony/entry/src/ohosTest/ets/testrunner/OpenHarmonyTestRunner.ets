import { abilityDelegatorRegistry, TestRunner } from '@kit.TestKit';
import { TEST_MODULE } from '../test/NativeContext';

export default class OpenHarmonyTestRunner implements TestRunner {
  onPrepare(): void {}
  onRun(): void {
    const delegator = abilityDelegatorRegistry.getAbilityDelegator();
    delegator.startAbility({ bundleName: abilityDelegatorRegistry.getArguments().bundleName, moduleName: TEST_MODULE, abilityName: 'TestAbility' })
      .catch(() => delegator.finishTest('Unable to start native test context', -1, () => {}));
  }
}
