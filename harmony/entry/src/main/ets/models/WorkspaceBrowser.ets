export class FolderCrumb {
  title: string;
  path: string;
  home: boolean = false;
  current: boolean = false;
  session: boolean = false;
  constructor(title:string, path:string) { this.title=title; this.path=path; }
}

/** Keep the server's path syntax, including Windows drive roots and UNC shares. */
export function folderCrumbs(path:string, home:string = '', session:string = ''):FolderCrumb[] {
  if (!path) return [];
  const windows = /^[A-Za-z]:[\\/]/.test(path) || path.startsWith('\\\\');
  const separator = windows && path.includes('\\') ? '\\' : '/';
  const clean = (s:string):string => windows ? s.replace(/\\/g,'/') : s;
  const value = clean(path), homePath = clean(home).replace(/\/+$/,'');
  let root = '', label = '', rest = value;
  if (homePath && homePath !== '/' && (value === homePath || value.startsWith(homePath+'/'))) {
    root=homePath; label='~'; rest=value.slice(root.length);
  } else if (windows && value.startsWith('//')) {
    const parts=value.slice(2).split('/').filter((s:string) => !!s);
    root=parts.length<2 ? value : '//'+parts.slice(0,2).join('/');
    label=parts.length<2 ? path : parts[1]; rest=value.slice(root.length);
  } else if (windows && /^[A-Za-z]:\//.test(value)) {
    root=value.slice(0,3); label=root; rest=value.slice(3);
  } else if (value.startsWith('/')) {
    root='/'; label='/'; rest=value.slice(1);
  } else if (value === '~' || value.startsWith('~/')) {
    root='~'; label='~'; rest=value.slice(1);
  }
  const restore=(p:string):string => separator === '\\' ? p.replace(/\//g,'\\') : p;
  const result:FolderCrumb[]=root ? [new FolderCrumb(label,restore(root))] : [];
  for (const component of rest.split('/').filter((s:string) => !!s)) {
    root = root.endsWith('/') || !root ? root+component : root+'/'+component;
    result.push(new FolderCrumb(component,restore(root)));
  }
  if (result.length) {
    result[0].home=label==='~';
    result[result.length-1].current=true;
  }
  const pathKey=(p:string):string => {
    const value=clean(p).replace(/\/+$/,'');
    return windows ? value.toLowerCase() : value;
  };
  if (session) {
    const cwd=pathKey(session);
    result.forEach((part:FolderCrumb) => { part.session=pathKey(part.path)===cwd; });
  }
  return result;
}

export function parentFolder(path:string):string {
  const crumbs=folderCrumbs(path);
  return crumbs.length>1 ? crumbs[crumbs.length-2].path : '';
}
export function joinedFilePath(parent:string,name:string):string {
  if (!parent || /[\\/]$/.test(parent)) return parent+name;
  return parent+(parent.includes('\\') && !parent.includes('/') ? '\\' : '/')+name;
}

export class FileFindHit {
  relative:string;
  directory:boolean;
  constructor(relative:string,directory:boolean) { this.relative=relative; this.directory=directory; }
}
export function indexWorkspaceFiles(names:string[], hidden:boolean, windows:boolean=false):FileFindHit[] {
  const directories=new Set<string>(), files=new Set<string>();
  for (const raw of names) {
    if (!raw || raw.startsWith('/') || raw.startsWith('\\\\') || /^[A-Za-z]:[\\/]/.test(raw) || raw.includes('\u0000')) continue;
    const path=windows ? raw.replace(/\\/g,'/') : raw, parts=path.split('/').filter((p:string) => p.length>0);
    if (!parts.length || parts.some((p:string) => p==='..' || p==='.' || !hidden && p.startsWith('.'))) continue;
    let relative='';
    for (let i=0;i<parts.length;i++) {
      relative = relative ? relative+'/'+parts[i] : parts[i];
      if (i+1<parts.length || path.endsWith('/')) directories.add(relative);
      else files.add(relative);
    }
  }
  const index:FileFindHit[]=[];
  directories.forEach((p:string) => index.push(new FileFindHit(p,true)));
  files.forEach((p:string) => { if (!directories.has(p)) index.push(new FileFindHit(p,false)); });
  return index.sort((a:FileFindHit,b:FileFindHit) => a.relative.length-b.relative.length || a.relative.localeCompare(b.relative));
}

export function findWorkspaceFiles(index:FileFindHit[],query:string,limit:number=100):FileFindHit[] {
  const needle=query.trim().toLowerCase();
  if (!needle || limit<=0) return [];
  const groups:FileFindHit[][]=[[],[],[],[]];
  for (const hit of index) {
    const path=hit.relative.toLowerCase();
    if (!path.includes(needle)) continue;
    const file=path.slice(path.lastIndexOf('/')+1);
    const rank=file===needle ? 0 : file.startsWith(needle) ? 1 : file.includes(needle) ? 2 : 3;
    if (groups[rank].length<limit) groups[rank].push(hit);
  }
  return groups[0].concat(groups[1],groups[2],groups[3]).slice(0,limit);
}

/** Returns -1 for missing/invalid timestamps; a clock-skewed future remains an absolute date. */
export function workspaceAgeMinutes(raw:string,now:number):number {
  const time=Date.parse(raw);
  return Number.isFinite(time) && time>0 && now>=time ? Math.floor((now-time)/60000) : -1;
}

export class PatchLine {
  kind:string; oldLine:string; newLine:string; text:string;
  constructor(kind:string,oldLine:string,newLine:string,text:string) {
    this.kind=kind; this.oldLine=oldLine; this.newLine=newLine; this.text=text;
  }
}
/** Number actual hunk lines. Metadata (including --- and +++ file headers) never consumes a line number. */
export function patchLines(text:string, fileOnly:boolean = false):PatchLine[] {
  if (!text) return [];
  let oldNo = 0, newNo = 0, inHunk = false;
  const lines:PatchLine[] = [];
  for (const line of text.split('\n')) {
    const match=/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (match) {
      oldNo=Number(match[1]); newNo=Number(match[2]); inHunk=true; lines.push(new PatchLine('hunk','','',line));
    } else if (inHunk && line.startsWith('+')) lines.push(new PatchLine('added','',String(newNo++),line));
    else if (inHunk && line.startsWith('-')) lines.push(new PatchLine('removed',String(oldNo++),'',line));
    else if (inHunk && line.startsWith(' ')) lines.push(new PatchLine('context',String(oldNo++),String(newNo++),line));
    else {
      if (line.startsWith('diff --git ') || line.startsWith('index ')) inHunk=false;
      lines.push(new PatchLine('meta','','',line));
    }
  }
  if (fileOnly) {
    const first = lines.findIndex((line: PatchLine) => line.kind === 'hunk');
    if (first > 0) return lines.slice(first);
  }
  return lines;
}

/** Git quotes control characters and, with core.quotePath, UTF-8 bytes in path headers. */
function gitPatchPath(value: string): string {
  const path = value.split('\t')[0];
  if (!path.startsWith('"') || !path.endsWith('"')) return path;
  try {
    let bytes = '';
    const parts = path.slice(1, -1).match(/\\[0-7]{3}|\\.|[^\\]+/g) || [];
    for (const part of parts) {
      if (/^\\[0-7]{3}$/.test(part)) bytes += '%' + parseInt(part.slice(1), 8).toString(16).padStart(2, '0');
      else if (part.startsWith('\\')) {
        const escaped = part[1];
        bytes += encodeURIComponent(escaped === 't' ? '\t' : escaped === 'n' ? '\n' : escaped === 'r' ? '\r' : escaped);
      } else bytes += encodeURIComponent(part);
    }
    return decodeURIComponent(bytes);
  } catch (_) { return path; }
}

/** Select only the requested file from the commit's immutable patch, including additions, deletions and renames. */
export function commitFilePatch(diff: string, path: string): string {
  if (!path || !diff) return '';
  const sections = diff.split(/\n(?=diff --(?:git |cc |combined ))/);
  for (const section of sections) {
    const lines = section.split('\n');
    const header = lines[0];
    if (!header.startsWith('diff --')) continue;
    let source = '', destination = '';
    for (const line of lines.slice(1)) {
      if (line.startsWith('@@') || line.startsWith('GIT binary patch')) break;
      if (line.startsWith('--- ')) source = gitPatchPath(line.slice(4));
      else if (line.startsWith('+++ ')) destination = gitPatchPath(line.slice(4));
      else if (line.startsWith('rename to ') || line.startsWith('copy to ')) destination = 'b/' + gitPatchPath(line.slice(line.indexOf(' to ') + 4));
    }
    if (destination === 'b/' + path || ((destination === '/dev/null' || !destination) && source === 'a/' + path)) return section;
    if (!source && !destination) {
      if (header === 'diff --git a/' + path + ' b/' + path || header === 'diff --cc ' + path || header === 'diff --combined ' + path) return section;
      const quoted = /^diff --git ("(?:\\.|[^"\\])*") ("(?:\\.|[^"\\])*")$/.exec(header);
      if (quoted && gitPatchPath(quoted[2]) === 'b/' + path) return section;
    }
  }
  return '';
}
