import { ResourceRow, Wire } from './Wire';

const standard: string[] = ['default','acceptEdits','plan','auto'];

export class SessionHeaderPermissions {
  visible: boolean = false;
  switchable: boolean = false;
  listed: boolean = false;
  unavailable: boolean = false;
  restarts: boolean = false;
  pending: boolean = false;
  running: boolean = false;
  current: string = '';
  choices: string[] = [];
  private reversible: boolean = false;
  private direct: boolean = false;

  constructor(row: ResourceRow) {
    if (row.kind !== 'session' || !row.machine) return;
    const data=row.data(), caps=Wire.object(data,'capabilities');
    if (Wire.bool(data,'assistant') || Wire.bool(data,'deleted')) return;
    const agent=Wire.text(data,'agent'), source=Wire.text(data,'source_kind');
    const claude=(!agent || agent==='claude') && source!=='codex_app_server' && source!=='codex_history';
    const live=Wire.text(data,'status',row.state)!=='offline' && row.state!=='offline';
    this.running=live;
    const send=Wire.has(data,'capabilities') ? Wire.bool(caps,'send') : !source;
    const declared=Wire.has(caps,'modes');
    const modes=send && live && (declared ? Wire.bool(caps,'modes') : claude);
    const ids=Wire.strings(caps,'permission_modes');
    this.listed=ids.length>=2 && !Wire.bool(caps,'modes') && (declared || !claude);
    this.current=Wire.text(data,'permission_mode');
    this.visible=typeof data['permission_mode']==='string' || modes || this.listed;
    this.switchable=send && live && (modes || this.listed);
    this.pending=Wire.bool(data,'bypass_pending');
    this.reversible=Wire.bool(data,'bypass_available');
    this.direct=agent==='agy';
    if (this.listed) {
      this.choices=ids;
      if (!ids.includes(this.current)) this.current='default';
      return;
    }
    const terminal=Wire.has(data,'capabilities') ? Wire.bool(caps,'terminal') : !source;
    this.restarts=!this.direct && modes && !source && !!Wire.text(data,'pane_id') && claude && terminal &&
      !this.reversible && this.current!=='bypassPermissions';
    this.choices=standard.slice();
    if (this.reversible || this.direct || this.restarts || this.current==='bypassPermissions') this.choices.push('bypassPermissions');
    if (this.current && !this.choices.includes(this.current)) this.choices.push(this.current);
    this.unavailable=modes && !this.choices.includes('bypassPermissions');
  }

  permits(id: string): boolean {
    return this.switchable && this.choices.includes(id) && (this.current!==id || this.pending);
  }
  confirmation(id: string): string {
    if (!this.permits(id) || this.direct || id===this.current) return '';
    if (id==='bypassPermissions') return 'enter';
    return !this.listed && this.current==='bypassPermissions' && !this.reversible ? 'leave' : '';
  }
  nameKey(id: string): string {
    if (this.listed && id==='default') return 'header_permission_launcher';
    return standard.includes(id) || id==='bypassPermissions' ? 'assistant_feed_permission_'+id : '';
  }
  detailKey(id: string): string {
    if (this.listed && id==='default') return 'header_permission_launcher_detail';
    if (id==='bypassPermissions') {
      return this.pending ? 'header_permission_pending' : this.restarts ? 'header_permission_restart' :
        this.listed || this.reversible || this.direct ? 'header_permission_full_detail' : 'header_permission_start_only';
    }
    return standard.includes(id) ? 'header_permission_detail_'+id : '';
  }
}

export function headerPermissionRow(selected:ResourceRow,sessions:ResourceRow[],group:string,manager:string):ResourceRow {
  if (selected.kind!=='session' || selected.group && selected.group!==group || selected.manager && selected.manager!==manager) return new ResourceRow();
  return sessions.find((row:ResourceRow) => row.machine===selected.machine && (row.session || row.id)===(selected.session || selected.id) &&
    row.manager===manager && (!row.group || row.group===group)) || selected;
}
