import { application, common } from '@kit.AbilityKit';

export const TEST_MODULE: string = 'entry_test';

let context: common.UIAbilityContext | undefined;
export function bindNativeTestContext(value: common.UIAbilityContext): void { context = value; }
export function nativeTestContext(): common.UIAbilityContext {
  if (!context) throw new Error('test_context_unavailable');
  return context;
}

// The replay corpus is packaged in the test feature. Start from its running Stage ability.
export function nativeTestModuleContext(): Promise<common.Context> {
  return application.createModuleContext(nativeTestContext(), TEST_MODULE);
}
