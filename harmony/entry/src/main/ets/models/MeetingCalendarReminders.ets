export class MeetingOccurrence {
  id:string='';
  title:string='';
  start:number=0;
  end:number=0;
  allDay:boolean=false;
  declined:boolean=false;
  key():string { return JSON.stringify([this.id || this.title,Math.floor(this.start/1000)]); }
}

export function upcomingMeetingReminders(events:MeetingOccurrence[],now:number,recording:string=''):MeetingOccurrence[] {
  const seen=new Set<string>();
  return events.filter((e:MeetingOccurrence) => !e.allDay && !e.declined && !!e.title.trim() && e.start-60000>now &&
    e.start<=now+36*3600000 && (!recording || e.id!==recording)).sort((a:MeetingOccurrence,b:MeetingOccurrence) => a.start-b.start || a.end-b.end)
    .filter((e:MeetingOccurrence) => { const id=e.key(); if (seen.has(id)) return false; seen.add(id); return true; }).slice(0,16);
}

export function meetingReminderFailure(code:number):string {
  if (code===1700002) return 'meeting_reminder_quota';
  if (code===1700001) return 'notifications_denied';
  return 'meeting_reminder_unavailable';
}

export class PublishedMeetingReminder {
  systemID:number;
  key:string;
  title:string;
  constructor(systemID:number,key:string,title:string) { this.systemID=systemID;this.key=key;this.title=title; }
}

export interface MeetingReminderSystem {
  existing:()=>Promise<PublishedMeetingReminder[]>;
  calendar:()=>Promise<MeetingOccurrence[]>;
  publish:(event:MeetingOccurrence)=>Promise<void>;
  cancel:(id:number)=>Promise<void>;
}

/** Only the caller's meeting-reminder group is presented by existing(). Re-read after every queued lifecycle change. */
export class MeetingReminderQueue {
  private tail:Promise<void>=Promise.resolve();
  private revision:number=0;
  refresh(system:MeetingReminderSystem,allowed:()=>boolean,recording:()=>string,now:()=>number):Promise<void> {
    const rev=++this.revision;
    const run=async ():Promise<void> => {
      if (rev!==this.revision) return;
      const wanted=allowed() ? upcomingMeetingReminders(await system.calendar(),now(),recording()) : [];
      if (rev!==this.revision) return;
      const existing=await system.existing(), matches=new Set<string>();
      for (const item of existing) {
        if (rev!==this.revision) return;
        const keep=wanted.find((e:MeetingOccurrence) => e.key()===item.key && e.title===item.title);
        if (!keep || matches.has(item.key)) await system.cancel(item.systemID);
        else matches.add(item.key);
      }
      for (const event of wanted) {
        if (rev!==this.revision || !allowed()) return;
        if (!matches.has(event.key()) && event.start-60000>now()) await system.publish(event);
      }
    };
    const next=this.tail.then(run); this.tail=next.catch(() => {}); return next;
  }
}
