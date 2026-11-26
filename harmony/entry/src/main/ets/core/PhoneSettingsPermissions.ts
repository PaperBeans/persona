export const conversationPermissionModes: string[] = ['default', 'acceptEdits', 'plan', 'auto', 'bypassPermissions'];

export function bindingPermission(purposeID: string, value: string): string {
  if (purposeID === 'inbox.agent' && (value === 'plan' || !conversationPermissionModes.includes(value))) return '';
  return value;
}

export function purposePermissionChoices(purposeID: string, supports: boolean, providerID: string,
  providerAgent: string, listed: string[]): string[] {
  if (!supports) return [];
  const agent = providerAgent || providerID.replace(/^launcher\./, '');
  let modes: string[];
  if (listed.length) {
    if (listed.length < 2) return [];
    modes = listed;
  } else {
    if (['omp', 'traex', 'pi', 'cursor'].includes(agent)) return [];
    modes = conversationPermissionModes;
  }
  return purposeID === 'inbox.agent' ? modes.filter((mode: string) => mode !== 'plan') : modes.slice();
}
