const interfaceStyles: string[] = ['glass', 'clay', 'island', 'lark', 'beautiful'];

export function storedPhoneInterfaceStyle(value: string): string {
  return interfaceStyles.includes(value) || value === 'brief' || value === 'board' ? value : 'system';
}

/** Earlier releases used “System” for the default iPhone appearance and offered two local styles. */
export function phoneInterfaceStyleID(value: string): string {
  const saved = storedPhoneInterfaceStyle(value);
  return saved === 'system' ? 'glass' : saved;
}

export function phoneInterfaceStyleChoices(current: string): string[] {
  return current === 'brief' || current === 'board' ? interfaceStyles.concat(current) : interfaceStyles.slice();
}
