import { util } from '@kit.ArkTS';
import type { ClientModel } from './ClientModel';
import { Wire } from './Wire';

export const intakeRoute: string = '/api/v1/assistant/health/intake';
const healthQuery: string = '?space=health';

export class IntakePortion {
  today: number; goal: number;
  constructor(today: number, goal: number) { this.today = today; this.goal = goal; }
}
export class IntakeDay {
  water: IntakePortion; protein: IntakePortion; zone: string; date: string;
  constructor(water: IntakePortion, protein: IntakePortion, zone: string, date: string) {
    this.water = water; this.protein = protein; this.zone = zone; this.date = date;
  }
  totals(water: number, protein: number, zone: string): IntakeDay {
    return new IntakeDay(new IntakePortion(water,this.water.goal),new IntakePortion(protein,this.protein.goal),zone,this.date);
  }
}
function validNumber(value: Object | undefined): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new Error('health_intake_invalid');
  return value;
}
function portion(raw: Record<string, Object>, key: string): IntakePortion {
  const value = Wire.object(raw,key), today = validNumber(value['today']), goal = validNumber(value['goal']);
  if (!Number.isSafeInteger(goal)) throw new Error('health_intake_invalid');
  return new IntakePortion(today,goal);
}
export function intakeDay(raw: Record<string, Object>): IntakeDay {
  const zone=Wire.text(raw,'zone'),days=Wire.rows(raw,'days'),date=days.length ? Wire.text(days[days.length-1],'date') : '';
  if (!zone || !/^\d{4}-\d\d-\d\d$/.test(date)) throw new Error('health_intake_invalid');
  return new IntakeDay(portion(raw,'water'),portion(raw,'protein'),zone,date);
}
export function intakeAmount(kind: string, text: string): number | undefined {
  const normalized=text.replace(/,/g,'.');
  if (!['water','protein'].includes(kind) || !/^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(normalized)) return undefined;
  const n=Number(normalized);
  if (!Number.isFinite(n) || n < (kind==='water' ? 1 : 0.1) || n > (kind==='water' ? 3000 : 200) ||
    Math.abs(n*10-Math.round(n*10))>=0.000001) return undefined;
  return n;
}
function calendarDay(zone: string): string {
  try { return new Intl.DateTimeFormat('en-CA',{year:'numeric',month:'2-digit',day:'2-digit',timeZone:zone}).format(Date.now()); }
  catch (_) { return ''; }
}
class IntakeIntent {
  readonly id: string; readonly kind: string; readonly at: string; readonly amount: number | undefined; readonly undo: boolean;
  constructor(id: string, kind: string, at: string, amount?: number, undo: boolean = false) {
    this.id=id;this.kind=kind;this.at=at;this.amount=amount;this.undo=undo;
  }
  body(): Record<string,Object> {
    const data=Wire.payload(['id','kind','at'],[this.id,this.kind,this.at]);
    if (this.amount!==undefined) data['amount']=this.amount;
    return data;
  }
}

/** Only the default manager provides the intake totals and accepts these idempotent, UUID-bound records. */
@ObservedV2
export class HealthIntakeModel {
  @Trace ownerKey: string = '';
  @Trace supported: boolean = false;
  @Trace available: boolean = false;
  @Trace data: IntakeDay | undefined;
  @Trace loading: boolean = false;
  @Trace busy: boolean = false;
  @Trace error: string = '';
  @Trace refreshError: string = '';
  @Trace notice: string = '';
  @Trace actionId: string = '';
  private client: ClientModel | undefined;
  private target: string = '';
  private epoch: number = 0;
  private readId: number = 0;
  private queued: number = 0;
  private chain: Promise<void> = Promise.resolve();
  private needsRead: boolean = false;
  private cup: number | undefined = undefined;
  private daySeen: string = '';
  private offered: IntakeIntent | undefined;
  private stillCurrent: () => boolean = ():boolean => false;
  private authorize: () => Promise<boolean> = async ():Promise<boolean> => false;
  private stopEvents: (() => void) | undefined;

  bind(client: ClientModel, supported: boolean, current: () => boolean, authorize: () => Promise<boolean>): void {
    const target=JSON.stringify([client.profile.id,client.scope(),client.connectionGeneration()]);
    if (client!==this.client || target!==this.target || supported!==this.supported) {
      this.close();
      this.client=client;this.target=target;this.ownerKey=JSON.stringify([target,this.epoch]);this.supported=supported;this.available=client.connected;
      if (supported) this.stopEvents=client.observeEvents((raw:string):void => {
        if (!this.current() || !raw.includes('"health_intake"')) return;
        let event:Record<string,Object>;
        try { event=Wire.parse(raw); } catch (_) { return; }
        if (Wire.text(event,'type')==='health_intake' && (!Wire.has(event,'manager') || Wire.text(event,'manager')===client.profile.manager)) this.refresh();
      });
    }
    this.stillCurrent=current;this.authorize=authorize;this.available=client.connected && current();
  }
  close(): void {
    this.stopEvents?.();this.stopEvents=undefined;this.epoch++;this.readId++;this.client=undefined;this.target='';this.ownerKey='';
    this.supported=false;this.available=false;this.data=undefined;this.busy=false;this.loading=false;this.error='';this.refreshError='';this.notice='';this.actionId='';
    this.offered=undefined;this.cup=undefined;this.daySeen='';this.queued=0;this.needsRead=false;this.chain=Promise.resolve();
  }
  private current(): boolean { return !!this.client?.connected && this.supported && this.stillCurrent(); }
  currentClient(): ClientModel | undefined { return this.current() && this.available && this.data ? this.client : undefined; }
  async refresh(): Promise<void> {
    const client=this.client;
    if (!client || !this.current()) return;
    if (this.busy) {this.needsRead=true;return;}
    const read=++this.readId,epoch=this.epoch;this.loading=true;
    try {
      const raw=await client.repo().call(intakeRoute+healthQuery);
      if (!this.current() || read!==this.readId || epoch!==this.epoch) return;
      const snapshot=intakeDay(raw);this.data=snapshot;this.daySeen=calendarDay(snapshot.zone);this.refreshError='';
      if (!this.offered || !this.error) this.error='';
    } catch (failure) {
      if (!this.current() || read!==this.readId || epoch!==this.epoch) return;
      const reason=this.errorKey(failure as Error,'health_intake_load_failed');
      if (reason==='permission_denied') {
        this.data=undefined;this.error=reason;this.refreshError='';this.offered=undefined;this.actionId='';this.notice='';
      } else if (this.data || this.offered) {
        // An acknowledged write can still be undone if the separate read fails. Retrying that read is always a GET.
        this.refreshError=reason;
      } else {
        this.error=reason;
      }
    } finally { if (epoch===this.epoch && read===this.readId) this.loading=false; }
  }
  refreshDay(): void {
    if (!this.data || !this.daySeen) return;
    const observed=calendarDay(this.data.zone);
    if (observed && observed!==this.daySeen) { this.data=undefined;this.daySeen='';this.refresh(); }
  }
  record(kind: string, amount?: number): Promise<void> {
    if (!this.currentClient() || this.busy || !['water','protein'].includes(kind) || (amount===undefined ? kind!=='water' : intakeAmount(kind,String(amount))===undefined)) return Promise.resolve();
    const intent=new IntakeIntent(util.generateRandomUUID().toLowerCase(),kind,new Date().toISOString(),amount);
    return this.enqueue(intent);
  }
  act(id: string): Promise<void> {
    const intent=this.offered;
    if (!intent || !id || this.actionId!==id || !this.currentClient()) return Promise.resolve();
    this.offered=undefined;this.actionId='';return this.enqueue(intent);
  }
  private enqueue(intent: IntakeIntent): Promise<void> {
    const epoch=this.epoch;this.busy=true;this.queued++;this.readId++;this.loading=false;
    const operation=this.chain.then(async ():Promise<void> => {
      try { if (epoch===this.epoch && this.current()) await this.apply(intent,epoch); }
      finally {
        if (epoch===this.epoch && --this.queued===0) {
          this.busy=false;
          if (this.needsRead) { this.needsRead=false;await this.refresh(); }
        }
      }
    });
    this.chain=operation.catch(() => {});return operation;
  }
  private async apply(intent: IntakeIntent, epoch: number): Promise<void> {
    const client=this.client,baseline=this.data;
    if (!client || !baseline) return;
    const current=():boolean => epoch===this.epoch && client===this.client && this.current();
    try {
      if (!await this.authorize() || !current()) throw new Error('request_updated');
      this.readId++;this.error='';this.refreshError='';this.notice='';this.actionId='';this.offered=undefined;
      const add=intent.amount ?? this.cup;
      if (!intent.undo && add!==undefined) this.data=baseline.totals(baseline.water.today+(intent.kind==='water'?add:0),baseline.protein.today+(intent.kind==='protein'?add:0),baseline.zone);
      // Retrying the same POST reuses both id and timestamp. DELETE is idempotent even after the first reply was lost.
      const path=intakeRoute+(intent.undo ? '/'+encodeURIComponent(intent.id) : '')+healthQuery;
      const response=Wire.parse(await client.repo().transport.request(path,intent.undo?'DELETE':'POST',intent.undo?'':JSON.stringify(intent.body())));
      if (!current()) return;
      const totals=Wire.object(response,'today'),entry=Wire.object(response,'entry'),zone=Wire.text(response,'zone');
      if (!zone || Object.keys(entry).length && (Wire.text(entry,'id').toLowerCase()!==intent.id || Wire.text(entry,'kind')!==intent.kind)) throw new Error('health_intake_invalid');
      this.data=baseline.totals(validNumber(totals['water']),validNumber(totals['protein']),zone);
      this.needsRead=true;
      if (intent.undo || Wire.text(entry,'deleted_at')) this.notice='health_intake_undone';
      else {
        if (intent.kind==='water' && intent.amount===undefined && typeof entry['amount']==='number') this.cup=validNumber(entry['amount']);
        this.notice='health_intake_logged';this.offer(new IntakeIntent(intent.id,intent.kind,intent.at,intent.amount,true));
      }
    } catch (failure) {
      if (!current()) return;
      this.data=baseline;this.error=this.errorKey(failure as Error,intent.undo?'health_intake_undo_failed':'health_intake_record_failed');
      if (this.error==='permission_denied' || this.error==='request_updated') {this.data=undefined;this.actionId='';this.offered=undefined;}
      else this.offer(intent);
    }
  }
  private offer(intent: IntakeIntent): void {
    this.offered=intent;this.actionId=JSON.stringify([this.epoch,intent.id,intent.undo,this.error]);
  }
  private errorKey(error: Error,fallback: string): string {
    const status=Wire.number(error as Object as Record<string,Object>,'status');
    if (status===401 || status===403) return 'permission_denied';
    return error.message==='request_updated' ? 'request_updated' : fallback;
  }
}
