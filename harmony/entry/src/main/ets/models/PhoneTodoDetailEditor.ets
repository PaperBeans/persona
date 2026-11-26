import type { TodoWorkspace, TodoWorkspaceItem } from './TodoWorkspace';
import { Wire } from './Wire';

class PlanPatch {
  due_at?: string | null;
  due_date?: string;
  remind_at?: string | null;
  priority?: string;
  project?: string;
}

function dayKey(value: number): string {
  const d=new Date(value);
  return d.getFullYear()+'-'+String(d.getMonth()+1).padStart(2,'0')+'-'+String(d.getDate()).padStart(2,'0');
}

export class PhoneTodoPlan {
  hasDue: boolean = false;
  allDay: boolean = true;
  due: number = 0;
  hasReminder: boolean = false;
  reminder: number = 0;
  priority: string = '';
  project: string = '';

  static from(todo: TodoWorkspaceItem, now: number = Date.now()): PhoneTodoPlan {
    const p=new PhoneTodoPlan(), tomorrow=new Date(now), hour=new Date(now);
    tomorrow.setDate(tomorrow.getDate()+1); tomorrow.setHours(18,0,0,0);
    hour.setHours(hour.getHours()+1,0,0,0);
    p.due=tomorrow.getTime();
    if (Number.isFinite(todo.due)) {
      p.hasDue=true; p.allDay=!todo.dueAt; const time=new Date(todo.due);
      if (p.allDay) time.setHours(18,0,0,0);
      p.due=time.getTime();
    }
    const reminder=Date.parse(Wire.text(todo.data(),'remind_at'));
    p.hasReminder=Number.isFinite(reminder); p.reminder=p.hasReminder ? reminder : hour.getTime();
    p.priority=todo.priority==='normal' ? '' : todo.priority; p.project=todo.project;
    return p;
  }

  copy(): PhoneTodoPlan {
    const p=new PhoneTodoPlan();
    p.hasDue=this.hasDue; p.allDay=this.allDay; p.due=this.due; p.hasReminder=this.hasReminder;
    p.reminder=this.reminder; p.priority=this.priority; p.project=this.project; return p;
  }

  changeFrom(saved: PhoneTodoPlan, fields: string[]): Record<string,Object> {
    const patch=new PlanPatch();
    if (fields.includes('due') && (this.hasDue!==saved.hasDue || this.hasDue &&
      (this.allDay!==saved.allDay || (this.allDay ? dayKey(this.due)!==dayKey(saved.due) : this.due!==saved.due)))) {
      if (!this.hasDue) patch.due_at=null;
      else if (this.allDay) patch.due_date=dayKey(this.due);
      else patch.due_at=new Date(this.due).toISOString();
    }
    if (fields.includes('remind') && (this.hasReminder!==saved.hasReminder || this.hasReminder && this.reminder!==saved.reminder))
      patch.remind_at=this.hasReminder ? new Date(this.reminder).toISOString() : null;
    if (fields.includes('priority') && this.priority!==saved.priority) patch.priority=this.priority;
    if (fields.includes('project') && this.project!==saved.project) patch.project=this.project;
    return Wire.parse(JSON.stringify(patch));
  }
}

export function todoDetailPatchAccepted(request: Record<string,Object>, row: Record<string,Object>): boolean {
  return Object.keys(request).every((key: string): boolean => {
    const want=Wire.text(request,key), actual=Wire.text(row,key);
    if (key==='due_at' && !want) return !actual && !Wire.text(row,'due_date');
    if (key==='due_date') return want===actual && !Wire.text(row,'due_at');
    if (key==='due_at' || key==='remind_at') return want ? Date.parse(want)===Date.parse(actual) : !actual;
    return want===actual || key==='priority' && !want && actual==='normal';
  });
}

@ObservedV2
export class PhoneTodoDetailEditor {
  @Trace title: string = '';
  @Trace plan: PhoneTodoPlan = new PhoneTodoPlan();
  @Trace unavailable: string[] = [];
  private key: string = '';
  private revision: number = 0;
  private titleBaseline: string = '';
  private titleTouched: boolean = false;
  private titleFocused: boolean = false;
  private pending: string[] = [];
  private running: Promise<void> | undefined;
  private timer: number = -1;

  reset(): void {
    this.revision++; clearTimeout(this.timer); this.timer=-1; this.running=undefined; this.pending=[];
    this.key=''; this.title=''; this.titleBaseline=''; this.titleTouched=false; this.titleFocused=false;
    this.unavailable=[]; this.plan=new PhoneTodoPlan();
  }

  sync(workspace: TodoWorkspace): void {
    const row=workspace.selected, key=JSON.stringify([row.manager,row.id]);
    if (this.key!==key) { this.reset(); this.key=key; }
    if (!this.titleFocused && !this.titleTouched && !this.pending.includes('title') && !this.running) {
      this.title=row.title; this.titleBaseline=row.title;
    }
    if (!this.pending.length && !this.running) this.plan=PhoneTodoPlan.from(row);
  }

  focus(workspace: TodoWorkspace): void {
    this.sync(workspace); this.titleFocused=true; this.titleBaseline=workspace.selected.title;
  }

  type(value: string): void { this.title=value; this.titleTouched=true; }

  commitTitle(workspace: TodoWorkspace): void {
    this.titleFocused=false;
    if (!this.titleTouched) { this.sync(workspace); return; }
    this.titleTouched=false;
    const value=this.title.trim(), current=workspace.selected.title;
    if (!value || value===current || value===this.titleBaseline) { this.title=current; this.titleBaseline=current; return; }
    this.title=value; this.queue(workspace,'title',0);
  }

  change(workspace: TodoWorkspace, value: PhoneTodoPlan, field: string): void {
    if (!workspace.detailEditable() || this.unavailable.includes(field)) return;
    this.plan=value;
    this.queue(workspace,field,field==='project' ? 0 : 400);
  }

  private queue(workspace: TodoWorkspace, field: string, delay: number): void {
    if (!this.pending.includes(field)) this.pending=this.pending.concat([field]);
    clearTimeout(this.timer); this.timer=setTimeout(() => { this.flush(workspace).catch(() => {}); },delay);
  }

  async finish(workspace: TodoWorkspace): Promise<void> {
    this.commitTitle(workspace);
    await this.flush(workspace);
  }

  async flush(workspace: TodoWorkspace): Promise<void> {
    clearTimeout(this.timer); this.timer=-1;
    if (this.running) { await this.running; if (this.pending.length) await this.flush(workspace); return; }
    if (!this.pending.length) return;
    const fields=this.pending, revision=this.revision, title=this.title.trim(), next=this.plan.copy();
    this.pending=[];
    const run=this.saveFields(workspace,fields,next,title,revision); this.running=run;
    try { await run; } finally {
      if (this.running===run) {
        this.running=undefined;
        if (this.pending.length) await this.flush(workspace); else this.sync(workspace);
      }
    }
  }

  private async saveFields(workspace: TodoWorkspace, fields: string[], plan: PhoneTodoPlan, title: string, revision: number): Promise<void> {
    // Independent PATCHes keep a renamed KB project from preventing a title or due-date save.
    for (const part of [['title'],['due','remind','priority'],['project']]) {
      if (revision!==this.revision || !workspace.detailEditable()) return;
      const active=part.filter((name: string) => fields.includes(name));
      if (!active.length) continue;
      const selected=workspace.selected;
      const body=active[0]==='title' ? Wire.payload(['title'],[title]) : plan.changeFrom(PhoneTodoPlan.from(selected),active);
      if (!Object.keys(body).length) continue;
      const ok=await workspace.updateInlineDetail(selected,body);
      if (revision!==this.revision) return;
      if (!ok && workspace.error==='todo_detail_unsupported') this.unavailable=this.unavailable.concat(active);
      if (!ok && (workspace.writeState==='pending' || workspace.writeState==='unknown' || workspace.error==='permission_denied')) return;
    }
  }
}
