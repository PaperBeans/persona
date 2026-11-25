import { ResourceRow, Wire } from './Wire';

export function sessionModelLauncher(data: Record<string, Object>): string {
  const named = Wire.text(data, 'launcher');
  if (named) return named;
  const agent = Wire.text(data, 'agent'), source = Wire.text(data, 'source_kind');
  if (source === 'runtime') return '';
  const space = Wire.text(data, 'space_kind'), desktop = Wire.text(data, 'agent_surface') === 'desktop';
  if (space === 'codex_app') return 'codex-app';
  if (agent === 'codex' || source === 'codex_app_server' || source === 'codex_history') return desktop ? 'codex-app' : 'codex';
  if (source === 'claude_desktop' || space === 'claude_desktop') return 'claude-desktop';
  if (!agent || agent === 'claude') return desktop ? 'claude-desktop' : 'claude';
  return '';
}

export function sessionModeAvailable(row: ResourceRow, field: string): boolean {
  if (row.kind !== 'session' || !row.machine || !row.session) return false;
  const data = row.data(), caps = Wire.object(data, 'capabilities');
  if (['offline', 'unknown'].includes(row.state) || ['offline', 'unknown'].includes(Wire.text(data, 'state')) ||
    ['offline', 'unknown'].includes(Wire.text(data, 'status')) ||
    Wire.bool(data, 'read_only') || Wire.text(data, 'read_only_reason') ||
    !Wire.bool(caps, 'send', Wire.bool(caps, 'model') || Wire.bool(caps, 'modes'))) return false;
  if (field === 'effort') return Wire.strings(data, 'efforts').length > 0;
  if (field === 'model') return Wire.bool(caps, 'model') && !!sessionModelLauncher(data);
  if (field !== 'permission_mode' || !Wire.bool(caps, 'modes')) return false;
  // A runtime may implement session.modes for effort alone.
  return Wire.text(data, 'source_kind') !== 'runtime' || !!Wire.text(data, 'permission_mode') ||
    Wire.strings(caps, 'permission_modes').length > 0;
}

export function sessionModeSettingsPage(field: string): string {
  if (field === 'permission_mode') return 'session_permissions';
  if (field === 'effort') return 'session_effort';
  return field === 'model' ? 'session_model' : '';
}
