// In the compact Separate layout each primary place owns a stack. A pushed destination has one
// Back button, including a session opened directly from a repository row.
const PLACE_ROOTS: string[] = ['', '', 'resources', 'settings_root', '', 'resources', 'resources', 'resources',
  'settings', 'settings', 'recordings', 'applications'];

export function phonePlaceAtRoot(section: number, routes: string[], assistantConversation: boolean): boolean {
  if (!routes.length) return true;
  if (routes.length !== 1) return false;
  if (section === 0) return assistantConversation && routes[0] === 'conversation';
  return !!PLACE_ROOTS[section] && routes[0] === PLACE_ROOTS[section];
}

// Keep drags starting in the composer, vertical lists and nested scrollers with their owners.
// The exposed side of a closed peer also works when the header's buttons are off screen.
export function phonePanIntent(section: number, atRoot: boolean, drawerOpen: boolean, x: number,
  y: number, width: number, height: number, dx: number, dy: number): string {
  if (width <= 0 || height <= 0 || x < 0 || x > width || Math.abs(dx) < 12 || Math.abs(dx) <= Math.abs(dy)) return '';
  if (drawerOpen) return dx < 0 ? 'close' : '';
  if (!atRoot || y > height - 88) return '';
  if (section === 0 && dx < 0) return 'agents';
  if ((section === 1 || section === 4) && dx > 0) return 'persona';
  return dx > 0 ? 'open' : '';
}

export function finishesPhonePan(dx: number, vx: number, width: number): boolean {
  if (!dx || width <= 0) return false;
  const velocity = dx < 0 ? -vx : vx;
  if (Math.abs(velocity) > 300) return velocity > 0;
  return Math.abs(dx) + velocity * 0.2 > width / 2;
}
