import { util } from '@kit.ArkTS';
import type { ClientModel, ClientViewLease } from './ClientModel';
import type { MutationRecord } from './MutationStore';
import { ProjectWorktree } from './ProjectCatalog';
import { ResourceRow, Wire } from './Wire';
import { HttpFailure } from '../platform/HubTransport';

export class WorktreeChange {
  action: string = '';
  id: string = '';
  path: string = '';
  branch: string = '';
  parent: string = '';
  branchRetained: boolean = false;
  created: ProjectWorktree | undefined = undefined;
}

export function checkoutName(branch: string): string {
  return branch.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
}
export function checkoutPath(base: string, branch: string): string {
  const leaf = checkoutName(branch);
  if (!leaf || !base) return '';
  return base.replace(/[\\/]+$/, '') + (base.includes('\\') && !base.includes('/') ? '\\' : '/') + leaf;
}
export function validWorktreeBranch(input: string): boolean {
  const name = input.trim();
  return !!name && name.length <= 255 && name !== '@' && !name.startsWith('-') && !name.endsWith('.') &&
    !/[\s~^:?*\[\\\x00-\x1f\x7f]/.test(name) && !name.includes('..') && !name.includes('@{') &&
    name.split('/').every((part: string) => !!part && !part.startsWith('.') && !part.endsWith('.lock'));
}

const OP_UUID = /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i;
const NODE_CREATE_TIMEOUT = 150000;
const NODE_RENAME_TIMEOUT = 30000;
const RECEIPT_RETENTION = 30 * 24 * 60 * 60 * 1000;
const submittingRoutes = new Set<string>();
function checkoutComparable(path: string): string {
  const value = path.trim().replace(/\\/g, '/').replace(/\/+$/, '');
  return /^[a-z]:\//i.test(value) ? value.toLowerCase() : value;
}
function releaseSupportsCreateReceipts(version: string): boolean {
  // A clean deploy.sh build names the release tag in its ancestry. Receipts shipped in v0.1.0.
  const tag = /^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(?:alpha|beta|rc)(?:\.?\d+)*)?(?:\+[a-zA-Z0-9.-]+|(?:-[1-9]\d*-g[0-9a-f]{7,40})?-[0-9]{12})?$/.exec(version);
  if (!tag) return false;
  const major = Number(tag[1]), minor = Number(tag[2]), patch = Number(tag[3]);
  return major > 0 || minor > 1 || minor === 1 && (patch > 0 || !tag[4]);
}
function operationAcknowledged(action: string, sourceId: string, request: Record<string, Object>, value: Record<string, Object>): boolean {
  if (['pending','unknown','failed'].includes(Wire.text(value,'state')) || Wire.text(value,'delivered') === 'unknown') return false;
  if (action === 'rename') return Wire.bool(value,'ok');
  if (action === 'clean') return Wire.text(value,'workspace_id') === sourceId && Wire.bool(value,'removed') && Wire.text(value,'state') === 'removed';
  const id = Wire.text(value,'workspace_id'), path = Wire.text(value,'path'), branch = Wire.text(value,'branch');
  return !!id && !!path && !!branch && (Wire.bool(request,'auto_name') ||
    (checkoutComparable(path) === checkoutComparable(Wire.text(request,'path')) && branch === Wire.text(request,'branch')));
}

interface RemovedRenameReview {
  record:MutationRecord;
  key:string;
  snapshot:string;
  directory:string;
  currentName:string;
}

@ObservedV2
export class WorktreeActions {
  @Trace action: string = '';
  @Trace loading: boolean = false;
  @Trace busy: boolean = false;
  @Trace ready: boolean = false;
  @Trace unknown: boolean = false;
  @Trace error: string = '';
  @Trace title: string = '';
  @Trace locationTitle: string = '';
  @Trace machine: string = '';
  @Trace branch: string = '';
  @Trace name: string = '';
  @Trace path: string = '';
  @Trace baseBranch: string = '';
  @Trace dirty: number = 0;
  @Trace folder: boolean = false;
  @Trace deleteBranch: boolean = false;
  @Trace force: boolean = false;
  @Trace automatic: boolean = false;
  @Trace supportsAutomatic: boolean = false;
  @Trace retrySupported: boolean = false;
  @Trace cleanupRepreviewAvailable: boolean = false;
  @Trace legacy: boolean = false;
  @Trace releaseReady: boolean = false;
  @Trace renameCurrent: string = '';
  @Trace renameOriginal: string = '';
  @Trace removedRenameDirectory:string = '';
  private priorRenameId:string = '';
  private removedRenameReview:RemovedRenameReview | undefined;
  private renameSnapshot: string = '';
  private cleanupSnapshot: string = '';
  private renameReviewedRecord: MutationRecord | undefined;
  private reviewedRenameBase: string = '';
  private base: string = '';
  private pathEdited: boolean = false;
  private token: string = '';
  private requestId: string = '';
  private requestBody: string = '';
  private requestPath: string = '';
  private requestTime: number = 0;
  private originalReceiptRoute: boolean = false;
  private legacyId: string = '';
  private releaseTarget: string = '';
  private view: ClientViewLease | undefined;
  private target: string = '';
  private revision: number = 0;
  private owner: ClientModel | undefined;
  private root: ClientModel | undefined;
  private tree: ProjectWorktree | undefined;
  private ownerEpoch: number = 0;
  private rootEpoch: number = 0;
  private rootScope: string = '';
  private ownerScope: string = '';

  close(): void {
    this.revision++; this.owner = undefined; this.root = undefined; this.tree = undefined;
    this.token = ''; this.ready = false; this.busy = false; this.loading = false; this.unknown = false;
    this.view = undefined; this.requestId = ''; this.requestPath = ''; this.requestBody = ''; this.requestTime = 0;
    this.retrySupported = false; this.originalReceiptRoute = false; this.legacy = false; this.legacyId = ''; this.releaseReady = false;
    this.releaseTarget = ''; this.target = '';
    this.priorRenameId='';this.removedRenameReview=undefined;this.removedRenameDirectory='';
    this.cleanupSnapshot = ''; this.cleanupRepreviewAvailable = false;
    this.renameCurrent = ''; this.renameOriginal = ''; this.renameSnapshot = ''; this.renameReviewedRecord = undefined; this.reviewedRenameBase = '';
  }
  private current(revision: number): boolean {
    return revision === this.revision && !!this.root && !!this.owner && this.root.owns(this.rootEpoch, this.rootScope) &&
      this.owner.connected && this.owner.owns(this.ownerEpoch, this.ownerScope) && (!this.view || this.view.valid()) &&
      (!this.target || this.target === this.formTarget());
  }
  private staleError(online: boolean = this.root?.connected === true && this.owner?.connected === true): string {
    if (!online) return 'offline';
    return this.action === 'clean' ? 'tree_preview_changed' : 'request_updated';
  }
  private requireCurrent(revision: number): void {
    if (!this.current(revision)) throw new Error(this.staleError());
  }
  private formTarget(): string {
    const tree = this.tree!;
    return JSON.stringify([this.ownerScope, tree.group, tree.manager, tree.machine, tree.source, tree.sourceId, tree.cwd, this.action]);
  }
  private endpoint(action: string): string {
    return '/api/v1/machines/' + encodeURIComponent(this.tree!.machine) + '/worktree/' + action;
  }
  private body(): Record<string, Object> {
    return Wire.payload(['workspace_id', 'kind'], [this.tree!.sourceId, this.tree!.source]);
  }
  private exactRecord(id: string): MutationRecord | undefined {
    const path = this.endpoint(this.action === 'clean' ? 'delete' : this.action);
    return this.owner?.repo().mutations?.blocked().find((record: MutationRecord) => record.id === id && record.path === path && record.method === 'POST');
  }
  private async verifyCreateVersion(): Promise<boolean> {
    const rev = this.revision, owner = this.owner!;
    if (!owner.features.includes('client_ops')) return false;
    const version = owner.verifiedServerVersion('client_ops');
    if (version) return this.current(rev) && releaseSupportsCreateReceipts(version);
    try {
      const health = await owner.repo().call('/api/v1/health');
      return this.current(rev) && Wire.bool(health, 'ok') && !!owner.profile.hubId &&
        Wire.text(health, 'hub_id') === owner.profile.hubId && Wire.strings(health, 'features').includes('client_ops') &&
        releaseSupportsCreateReceipts(Wire.text(health, 'version'));
    } catch (_) { return false; }
  }
  private snapshot(): string {
    const saved = Wire.payload(['target','action','path','id','payload','at','receipts'],
      [this.target, this.action, this.requestPath, this.requestId, this.requestBody, this.requestTime, this.originalReceiptRoute]);
    if (this.action === 'rename') saved['originalName'] = this.title;
    return JSON.stringify(saved);
  }
  private restoreRequest(saved: Record<string, Object>, path: string): boolean {
    const id = Wire.text(saved, 'id'), payload = Wire.text(saved, 'payload'), body = Wire.parse(payload);
    if (!OP_UUID.test(id) || Wire.text(saved, 'target') !== this.target || Wire.text(saved, 'action') !== this.action ||
      Wire.text(saved, 'path') !== path || Wire.text(body, 'workspace_id') !== this.tree!.sourceId || Wire.text(body, 'kind') !== this.tree!.source) return false;
    if (this.action === 'create' && !Wire.bool(body,'auto_name') && (!Wire.text(body,'branch') || !Wire.text(body,'path'))) return false;
    if (this.action === 'rename' && !Wire.text(body,'name') || this.action === 'clean' && !Wire.text(body,'preview_token')) return false;
    this.requestId = id; this.requestPath = path; this.requestBody = payload; this.requestTime = Wire.number(saved,'at');
    this.originalReceiptRoute = Wire.bool(saved,'receipts');
    if (this.action === 'create') {
      this.automatic = Wire.bool(body,'auto_name'); this.supportsAutomatic = this.automatic || this.supportsAutomatic;
      this.branch = Wire.text(body,'branch'); this.path = Wire.text(body,'path'); this.pathEdited = true;
    } else if (this.action === 'rename') this.name = Wire.text(body,'name');
    else { this.token = Wire.text(body,'preview_token'); this.force = Wire.bool(body,'force'); this.deleteBranch = Wire.bool(body,'delete_branch'); }
    this.unknown = true; this.error = 'result_unknown'; return true;
  }
  private async query(action: string, body: Record<string, Object>): Promise<Record<string, Object>> {
    // These requests never dispatch a worktree change. Surrendering an unused token is idempotent.
    const rev = this.revision, route = this.endpoint(action), owner = this.owner!, payload = JSON.stringify(body);
    return this.read(rev, async (): Promise<Record<string,Object>> => Wire.parse(await owner.repo().transport.request(route, 'POST', payload)));
  }
  private async read(rev: number, fetch: () => Promise<Record<string,Object>>): Promise<Record<string,Object>> {
    this.requireCurrent(rev);
    try { return await fetch(); }
    catch (error) {
      if (!(error instanceof HttpFailure)) throw error as Error;
      if (error.status !== 0 || !error.code.startsWith('transport_failed_')) throw error;
      this.requireCurrent(rev);
      await new Promise<void>((resolve) => setTimeout(resolve, 350));
      this.requireCurrent(rev);
      return await fetch();
    }
  }
  private failure(err: Error): string {
    const code = err instanceof HttpFailure ? err.code : err.message;
    if (err instanceof HttpFailure && err.status === 403) return 'permission_denied';
    if (['permission_denied','request_updated','offline','result_unknown','pending_action','node_outdated'].includes(code)) return code;
    if (code.startsWith('transport_failed_')) return 'connection_failed';
    if (code.includes('dirty')) return 'tree_changed_files';
    if (code.includes('main_checkout') || code.includes('main_workspace')) return 'tree_main_protected';
    if (code.includes('stale') || code.includes('preview_required')) return 'tree_preview_changed';
    return code && code.length <= 180 ? code : 'failed';
  }
  async open(model: ClientModel, tree: ProjectWorktree, action: string, retiredToken: string = ''): Promise<void> {
    this.close();
    const revision = this.revision, scope = model.scope(), epoch = model.connectionGeneration();
    this.action = action; this.title = tree.title; this.locationTitle = tree.title; this.machine = tree.machineTitle;
    this.branch = ''; this.baseBranch = ''; this.name = tree.title; this.path = tree.cwd;
    this.error = ''; this.base = ''; this.token = ''; this.requestId = ''; this.dirty = 0; this.folder = false;
    this.force = false; this.deleteBranch = false; this.supportsAutomatic = false; this.automatic = false; this.pathEdited = false;
    this.loading = true;
    try {
      if (!model.connected || !tree.online) throw new Error('offline');
      if (!tree.permits(action)) throw new Error('permission_denied');
      const target = new ResourceRow(); target.group = tree.group; target.manager = tree.manager;
      const owner = await model.viewFor(target);
      if (this.revision !== revision) return;
      if (!model.owns(epoch, scope)) throw new Error(this.staleError(model.connected));
      this.root = model; this.rootScope = scope; this.rootEpoch = epoch;
      this.owner = owner; this.ownerScope = owner.scope(); this.ownerEpoch = owner.connectionGeneration(); this.tree = tree;
      this.requireCurrent(revision);
      this.target = this.formTarget();
      this.view = owner.captureView('worktree-request:' + this.target);
      const route = this.endpoint(action === 'clean' ? 'delete' : action);
      const snapshot = await this.view.read(), saved = Wire.parse(snapshot);
      this.requireCurrent(revision);
      if (this.restoreRequest(saved, route)) {
        if (action === 'rename') { this.renameSnapshot = snapshot; this.renameOriginal = Wire.text(saved,'originalName'); }
        if (action === 'clean') this.cleanupSnapshot = snapshot;
        const verified = action === 'create' && this.originalReceiptRoute && await this.verifyCreateVersion();
        if (this.current(revision)) { this.retrySupported = verified; this.ready = true; }
        return;
      }
      const unresolved = owner.repo().mutations?.blocked().find((item: MutationRecord) => item.path === route && item.method === 'POST');
      if (unresolved) {
        this.unknown = true; this.error = 'pending_action';
        if (action==='rename' && !snapshot && this.retirableRename(unresolved)) {
          this.priorRenameId=unresolved.id;
          await this.reviewRemovedRename(revision,false);
          return;
        }
        if (action !== 'create' || !OP_UUID.test(unresolved.id)) return;
        this.legacy = true; this.legacyId = unresolved.id; this.error = 'tree_verify_original_checkout';
      }
      if (action === 'create') {
        const loadingVersion = this.verifyCreateVersion();
        const prepared = await this.query('prepare', this.body());
        const supported = await loadingVersion;
        this.requireCurrent(revision);
        this.originalReceiptRoute = supported;
        this.base = Wire.text(prepared, 'path'); this.baseBranch = Wire.text(prepared, 'branch');
        if (!this.base || !Wire.text(prepared, 'root')) throw new Error('tree_repo_unavailable');
        this.path = ''; this.supportsAutomatic = Wire.bool(prepared, 'automatic_name');
      } else if (action === 'clean') {
        const body = this.body(); body['preview'] = true;
        const preview = await this.query('delete', body);
        this.requireCurrent(revision);
        const path = Wire.text(preview, 'path'), dirty = Wire.number(preview, 'dirty', -1);
        if (Wire.text(preview, 'workspace_id') !== tree.sourceId || Wire.text(preview, 'kind') !== tree.source ||
          !path || dirty < 0 || !Wire.text(preview, 'preview_token') || Wire.text(preview,'preview_token') === retiredToken ||
          Wire.bool(preview, 'close_only')) throw new Error('tree_preview_changed');
        this.token = Wire.text(preview, 'preview_token'); this.path = path; this.branch = Wire.text(preview, 'branch');
        this.dirty = dirty; this.folder = Wire.bool(preview, 'folder');
      }
      this.ready = true;
    } catch (error) {
      if (this.revision === revision) this.error = this.failure(error as Error);
    } finally { if (this.revision === revision) this.loading = false; }
  }
  setBranch(value: string): void { if (this.busy || this.unknown && !this.legacy) return; this.branch = value; this.releaseReady = false; if (!this.pathEdited) this.path = checkoutPath(this.base, value); }
  setPath(value: string): void { if (this.busy || this.unknown && !this.legacy) return; const suggested = checkoutPath(this.base, this.branch); this.releaseReady = false; this.pathEdited = !!value && value !== suggested; this.path = value || suggested; }
  get canSubmit(): boolean {
    if (!this.ready || this.busy || this.loading || this.unknown) return false;
    if (this.action === 'rename') return !!this.name.trim() && this.name.trim().length <= 120 && this.name.trim() !== this.reviewedRenameBase.trim();
    if (this.action === 'create') return this.supportsAutomatic && this.automatic ||
      validWorktreeBranch(this.branch) && !!this.path.trim();
    return this.action === 'clean' && !!this.token && (!this.dirty || this.force);
  }
  private removed(value: Record<string, Object>): boolean {
    return Wire.text(value, 'workspace_id') === this.tree?.sourceId &&
      Wire.bool(value, 'removed') && Wire.text(value, 'state') === 'removed';
  }
  private acknowledged(value: Record<string, Object>): boolean {
    return operationAcknowledged(this.action,this.tree!.sourceId,Wire.parse(this.requestBody),value);
  }
  private change(value: Record<string, Object>): WorktreeChange {
    const change = new WorktreeChange();
    change.action = this.action; change.id = Wire.text(value, 'workspace_id', this.tree!.sourceId);
    change.path = Wire.text(value, 'path', this.path); change.branch = Wire.text(value, 'branch', this.branch);
    change.parent = Wire.text(value, 'parent_workspace_id') || this.tree!.sourceId;
    // Only the dispatched request records whether the user asked to delete the branch.
    change.branchRetained = this.action === 'clean' && Wire.bool(Wire.parse(this.requestBody), 'delete_branch') &&
      !Wire.bool(value, 'branch_deleted');
    return change;
  }
  private createdSelection(value: Record<string,Object>): ProjectWorktree | undefined {
    const parent = this.tree!, id = Wire.text(value,'workspace_id'), path = Wire.text(value,'path'), branch = Wire.text(value,'branch');
    const parentId = Wire.text(value,'parent_workspace_id') || parent.sourceId;
    if (id === parent.sourceId || parentId !== parent.sourceId || checkoutComparable(path) === checkoutComparable(parent.cwd) ||
      !this.acknowledged(value)) return undefined;
    const root = this.root!, owner = this.owner!, rootScope = this.rootScope, rootEpoch = this.rootEpoch;
    const scope = this.ownerScope, epoch = this.ownerEpoch, target = new ResourceRow();
    target.group = parent.group; target.manager = parent.manager;
    const child = new ProjectWorktree();
    child.group = parent.group; child.manager = parent.manager; child.machine = parent.machine; child.machineTitle = parent.machineTitle;
    child.source = parent.source; child.sourceId = id; child.cwd = path; child.branch = branch; child.title = branch;
    child.key = JSON.stringify([child.group,child.manager,child.machine,child.source,id]);
    child.parent = parentId; child.parentKey = parent.key; child.startAllowed = parent.startAllowed;
    child.receiptCurrent = (): boolean => root.connected && root.owns(rootEpoch,rootScope) && owner.connected &&
      owner.owns(epoch,scope) && root.directoryOwnerFor(target) === owner;
    return child;
  }
  async submit(): Promise<WorktreeChange | undefined> {
    if (!this.canSubmit) return undefined;
    const rev = this.revision;
    if (!this.current(rev)) { this.error = this.staleError(); this.ready = false; return undefined; }
    const owner = this.owner!, action = this.action, sourceId = this.tree!.sourceId, body = this.body(), view = this.view!;
    const path = this.endpoint(action === 'clean' ? 'delete' : action);
    const lane = JSON.stringify([this.ownerScope,path]);
    if (submittingRoutes.has(lane)) return undefined;
    const other = owner.repo().mutations?.blocked().some((item: MutationRecord) => item.path === path && item.method === 'POST');
    if (other) { this.ready = false; this.error = 'pending_action'; return undefined; }
    submittingRoutes.add(lane);
    this.busy = true; this.error = '';
    let snapshot = '', rejectedBeforeDispatch = false;
    const admit = (): boolean => {
      rejectedBeforeDispatch = !this.current(rev);
      return !rejectedBeforeDispatch;
    };
    try {
      if (action === 'create') {
        if (this.automatic && this.supportsAutomatic) body['auto_name'] = true;
        else { body['branch'] = this.branch.trim(); body['path'] = this.path.trim(); }
      } else if (action === 'rename') {
        if (this.reviewedRenameBase) {
          const live = await this.liveRenameName(rev);
          this.requireCurrent(rev);
          if (live !== this.reviewedRenameBase) {
            this.title = live; this.name = live; this.reviewedRenameBase = live;
            this.error = 'tree_rename_check_again'; return undefined;
          }
        }
        body['name'] = this.name.trim();
      }
      else { body['preview_token'] = this.token; body['force'] = this.force; body['delete_branch'] = !this.folder && this.deleteBranch; }
      this.requestId = util.generateRandomUUID();
      this.requestPath = path; this.requestBody = JSON.stringify(body); this.requestTime = Date.now();
      snapshot = this.snapshot();
      if (action === 'rename') { this.renameSnapshot = snapshot; this.renameOriginal = this.title; }
      if (action === 'clean') this.cleanupSnapshot = snapshot;
      await view.write(snapshot);
      if (!admit()) throw new Error('request_updated');
      const result = await owner.repo().call(path, 'POST', body, this.requestId, admit, false,
        (value: Record<string,Object>): boolean => operationAcknowledged(action,sourceId,body,value));
      if (!this.current(rev)) return undefined;
      const change = this.change(result);
      if (action === 'create') change.created = this.createdSelection(result);
      this.ready = false;
      if (action === 'rename' || action === 'clean') await view.clearIfUnchanged(snapshot).catch(() => undefined);
      else await view.write('').catch(() => undefined);
      return this.current(rev) ? change : undefined;
    } catch (error) {
      if (rejectedBeforeDispatch && !(error instanceof HttpFailure && error.resultUnknown)) {
        if (this.revision === rev) { this.error = this.staleError(); this.ready = false; }
        // The local admission check declined this request before transport. A replacement
        // sheet or pairing can own the same storage key by the time the write completes.
        await view.clearIfUnchanged(snapshot).catch(() => undefined);
        return undefined;
      }
      if (!this.current(rev)) return undefined;
      this.error = this.failure(error as Error);
      this.unknown = error instanceof HttpFailure && error.resultUnknown;
      if (this.unknown) {
        this.retrySupported = action === 'create' && this.originalReceiptRoute && !!this.exactRecord(this.requestId);
        this.error = error instanceof HttpFailure && error.resultPending ? 'pending' : 'result_unknown';
      } else if (action === 'rename' || action === 'clean') {
        if (snapshot) await view.clearIfUnchanged(snapshot).catch(() => undefined);
      } else if (view.valid()) await view.write('').catch(() => undefined);
      if (action === 'clean' && !this.unknown) this.ready = false;
      return undefined;
    } finally { submittingRoutes.delete(lane); if (this.revision === rev) this.busy = false; }
  }
  get requestCheckAvailable(): boolean {
    if (!this.unknown || !this.current(this.revision)) return false;
    if (this.action === 'create') return !!(this.requestId || this.legacyId) && this.owner!.features.includes('client_ops');
    if(this.action==='rename' && this.priorRenameId)return true;
    return !!this.requestBody && (this.action === 'rename' || this.action === 'clean' && !!this.token);
  }
  get canCheck(): boolean { return !this.busy && this.requestCheckAvailable; }
  get canRetrySame(): boolean {
    return this.canCheck && this.action === 'create' && this.canReplayDuringCheck(this.requestId);
  }
  private fresh(start: number): boolean { const age = Date.now() - start; return start > 0 && age >= 0 && age < RECEIPT_RETENTION; }
  private async finishRecord(id: string, state: string): Promise<void> {
    const owner = this.owner!, journal = owner.repo().mutations, record = this.exactRecord(id);
    if (record && journal && record.state !== state) await journal.finish(record, state);
    if (journal && this.owner === owner && this.current(this.revision)) owner.unresolvedWrites = journal.blocked();
  }
  private async completed(rev: number, result: Record<string,Object>): Promise<WorktreeChange | undefined> {
    if (!this.current(rev)) return undefined;
    const change = this.legacy ? undefined : this.change(result), view = this.view!;
    await this.finishRecord(this.legacy ? this.legacyId : this.requestId, 'done');
    if (!this.current(rev)) return undefined;
    if (this.action === 'rename') await view.clearIfUnchanged(this.renameSnapshot).catch(() => undefined);
    else if (this.action === 'clean') await view.clearIfUnchanged(this.cleanupSnapshot).catch(() => undefined);
    else if (!this.legacy) await view.write('').catch(() => undefined);
    if (!this.current(rev)) return undefined;
    this.unknown = false; this.ready = false; this.retrySupported = false; this.releaseReady = false; this.error = change ? '' : 'done';
    return change;
  }
  private async refused(rev: number, code: string): Promise<void> {
    await this.finishRecord(this.legacy ? this.legacyId : this.requestId, 'failed');
    if (!this.current(rev)) return;
    if (!this.legacy) await this.view!.write('').catch(() => undefined);
    if (!this.current(rev)) return;
    this.unknown = false; this.ready = true; this.retrySupported = false; this.legacy = false; this.releaseReady = false;
    this.error = code || 'failed';
  }
  private async status(id: string): Promise<Record<string,Object>> {
    const rev = this.revision, owner = this.owner!;
    try {
      const op = await this.read(rev, () => owner.repo().call('/api/v1/ops/' + encodeURIComponent(id)));
      if (!this.current(rev)) throw new Error('request_updated');
      const path = Wire.text(Wire.object(op, 'target'), 'path');
      if (Wire.text(op,'id') !== id || Wire.text(op,'kind') !== 'worktree_create' ||
        (path !== this.endpoint('create') && path !== '/api/v1/machines/' + this.tree!.machine + '/worktree/create')) throw new Error('request_updated');
      return op;
    } catch (error) {
      if (error instanceof HttpFailure && error.status === 404 && ['no such operation','no_such_operation'].includes(error.code))
        return Wire.payload(['state'],['missing']);
      throw error as Error;
    }
  }
  private receiptBody(result: Record<string,Object>): Record<string,Object> {
    const encoded = Wire.text(result,'body');
    if (!encoded) return Wire.object(result,'body');
    if (encoded.length > 256 * 1024) return {};
    try {
      const bytes = new util.Base64Helper().decodeSync(encoded);
      return Wire.parse(util.TextDecoder.create('utf-8', { fatal: true }).decodeToString(bytes));
    } catch (_) { return {}; }
  }
  private sameSource(row: Record<string,Object>): boolean {
    const tree = this.tree!;
    return Wire.text(row,'machine_id') === tree.machine && Wire.text(row,'source') === tree.source &&
      Wire.text(row,'source_id') === tree.sourceId && checkoutComparable(Wire.text(row,'cwd')) === checkoutComparable(tree.cwd) &&
      Wire.bool(row,'online') && (!Wire.has(row,'available') || Wire.bool(row,'available'));
  }
  private async workspaces(rev: number, requireComplete: boolean = false, proveAbsence:boolean=false): Promise<Record<string,Object>[]> {
    const owner = this.owner!;
    const result = await this.read(rev, () => owner.repo().call('/api/v1/workspaces'));
    if (!this.current(rev)) throw new Error('request_updated');
    if (requireComplete && (Wire.bool(result,'has_more',Wire.has(result,'has_more')) || Wire.bool(result,'partial',Wire.has(result,'partial')) ||
      !!Wire.text(result,'next_cursor') || Wire.has(result,'errors') && !Array.isArray(result['errors']))) throw new Error('result_unknown');
    if (!Array.isArray(result['workspaces']) || Wire.rows(result,'errors').some((err: Record<string,Object>) =>
      !Wire.text(err,'machine_id') || Wire.text(err,'machine_id') === this.tree!.machine)) throw new Error('result_unknown');
    const rows=Wire.rows(result,'workspaces');
    if(proveAbsence && (Wire.rows(result,'errors').length>0 || Wire.has(result,'next_cursor') && typeof result['next_cursor']!=='string' ||
      Wire.bool(result,'truncated',Wire.has(result,'truncated')) || Wire.has(result,'complete') && result['complete']!==true ||
      Wire.has(result,'total') && Wire.number(result,'total',-1)!==rows.length ||
      rows.some((row:Record<string,Object>)=>!Wire.text(row,'machine_id') || !Wire.text(row,'source') || !Wire.text(row,'source_id'))))
      throw new Error('result_unknown');
    return rows;
  }
  private async liveRenameName(rev: number): Promise<string> {
    const tree = this.tree!, rows = await this.workspaces(rev,true);
    this.requireCurrent(rev);
    if (!tree.group || !tree.machine || !tree.source || !tree.sourceId || !tree.cwd.trim()) throw new Error('result_unknown');
    const matches = rows.filter((row: Record<string,Object>) => Wire.text(row,'machine_id') === tree.machine &&
      Wire.text(row,'source') === tree.source && Wire.text(row,'source_id') === tree.sourceId);
    if (matches.length !== 1 || !this.sameSource(matches[0]) ||
      Wire.has(matches[0],'manager_id') && Wire.text(matches[0],'manager_id') !== tree.manager) throw new Error('result_unknown');
    const name = Wire.text(matches[0],'title');
    if (!name.trim()) throw new Error('result_unknown');
    return name;
  }
  private retirableRename(record:MutationRecord):boolean {
    const created=Date.parse(record.createdAt), tree=this.tree;
    if (!tree || !tree.permits('rename') || !OP_UUID.test(record.id) || record.path!==this.endpoint('rename') ||
      record.method!=='POST' || record.state!=='unknown' || record.confirmedPending!==false || record.requiresReceipt!==false ||
      !Number.isFinite(created) || created<=0 || Date.now()-created<NODE_RENAME_TIMEOUT) return false;
    const journal=this.owner?.repo().mutations;
    return !!journal && journal.record(record.id)===record && journal.blocked().filter((r:MutationRecord)=>
      r.method==='POST' && r.path===record.path).length===1;
  }
  private async inspectRemovedRename(rev:number):Promise<RemovedRenameReview | undefined> {
    const owner=this.owner!,tree=this.tree!,record=owner.repo().mutations?.record(this.priorRenameId);
    if(!record || !this.retirableRename(record) || !tree.group || !tree.machine || !tree.source || !tree.sourceId ||
      !tree.cwd.trim() || !this.view!.storageKey || await this.view!.read()!=='') return undefined;
    this.requireCurrent(rev);
    const targets=await owner.savedRenameTargets(tree.group,tree.manager,tree.machine,tree.source,record.id);
    this.requireCurrent(rev);
    if(targets.length!==1 || targets[0]===this.target)return undefined;
    const target=targets[0], decoded=JSON.parse(target) as Object;
    if(!Array.isArray(decoded) || decoded.length!==8 || decoded.some((part:Object)=>typeof part!=='string'))return undefined;
    const fields=decoded as string[],oldId=fields[5],oldPath=fields[6];
    const fullPath=(value:string):boolean=>value===value.trim() && /^(?:\/[^/]|[a-z]:[\\/]|[\\/]{2}[^\\/])/i.test(value);
    if(target!==JSON.stringify([this.ownerScope,tree.group,tree.manager,tree.machine,tree.source,oldId,oldPath,'rename']) ||
      !oldId.trim() || oldId===tree.sourceId || !fullPath(oldPath) || !fullPath(tree.cwd) || checkoutComparable(oldPath)===checkoutComparable(tree.cwd))return undefined;
    const view=owner.captureView('worktree-request:'+target), snapshot=await view.read(), saved=Wire.parse(snapshot);
    const body=Wire.text(saved,'payload'), payload=Wire.parse(body), at=Wire.number(saved,'at'), name=Wire.text(payload,'name');
    if(!view.storageKey || Wire.text(saved,'id')!==record.id || Wire.text(saved,'target')!==target || Wire.text(saved,'action')!=='rename' ||
      Wire.text(saved,'path')!==record.path || Wire.text(payload,'kind')!==tree.source || Wire.text(payload,'workspace_id')!==oldId ||
      !name.trim() || name.trim()!==name || name.length>120 || record.payload && record.payload!==body || Object.keys(payload).length!==3 ||
      Wire.has(saved,'receipts') && saved['receipts']!==false ||
      !at || at<=0 || Date.now()-at<NODE_RENAME_TIMEOUT)return undefined;
    this.requireCurrent(rev);
    const rows=await this.workspaces(rev,true,true);
    this.requireCurrent(rev);
    const sourceRows=rows.filter((row:Record<string,Object>)=>Wire.text(row,'machine_id')===tree.machine);
    if(sourceRows.some((row:Record<string,Object>)=>Wire.text(row,'source')===tree.source &&
      (!Wire.text(row,'cwd').trim() || Wire.text(row,'source_id')===oldId) ||
      checkoutComparable(Wire.text(row,'cwd'))===checkoutComparable(oldPath)))return undefined;
    const active=sourceRows.filter((row:Record<string,Object>)=>Wire.text(row,'source')===tree.source && Wire.text(row,'source_id')===tree.sourceId);
    if(active.length!==1 || !this.sameSource(active[0]) || Wire.has(active[0],'manager_id') && Wire.text(active[0],'manager_id')!==tree.manager ||
      sourceRows.some((row:Record<string,Object>)=>row!==active[0] && checkoutComparable(Wire.text(row,'cwd'))===checkoutComparable(tree.cwd)))return undefined;
    const liveName=Wire.text(active[0],'title');
    if(!liveName.trim() || await view.read()!==snapshot || await this.view!.read()!=='' || !this.current(rev) || !this.retirableRename(record))return undefined;
    return {record:record,key:view.storageKey,snapshot:snapshot,directory:oldPath,currentName:liveName};
  }
  private async reviewRemovedRename(rev:number,confirm:boolean):Promise<void> {
    const previous=this.removedRenameReview;
    this.removedRenameReview=undefined;this.removedRenameDirectory='';this.error='pending_action';
    const review=await this.inspectRemovedRename(rev);
    this.requireCurrent(rev);
    if(!review)return;
    if(!confirm) {
      this.removedRenameReview=review;this.removedRenameDirectory=review.directory;this.error='tree_rename_removed_explanation';return;
    }
    if(!previous || previous.record!==review.record || previous.snapshot!==review.snapshot ||
      previous.key!==review.key || previous.currentName!==review.currentName){this.error='tree_rename_check_again';return;}
    const owner=this.owner!,journal=owner.repo().mutations!,formKey=this.view!.storageKey!;
    const accepted=await journal.acknowledgeRemovedRename(review.record,this.ownerScope,review.key,review.snapshot,formKey,
      ()=>this.current(rev) && this.retirableRename(review.record));
    this.requireCurrent(rev);
    if(!accepted)return;
    owner.unresolvedWrites=journal.blocked();
    if(journal.blocked().some((other:MutationRecord)=>other.method==='POST' && other.path===review.record.path))return;
    this.priorRenameId='';this.title=review.currentName;this.name=review.currentName;this.locationTitle=review.currentName;
    this.reviewedRenameBase=review.currentName;this.unknown=false;this.ready=true;this.error='';
  }
  private canKeepRename(record: MutationRecord | undefined): boolean {
    if (!record || record.id !== this.requestId || record.method !== 'POST' || record.path !== this.endpoint('rename') ||
      record.confirmedPending !== false || record.requiresReceipt !== false || !['unknown','checked'].includes(record.state)) return false;
    const created = Date.parse(record.createdAt);
    return Number.isFinite(created) && created > 0 && this.requestTime > 0 &&
      Date.now() - Math.max(created,this.requestTime) >= NODE_RENAME_TIMEOUT;
  }
  private async recoverRename(rev: number, confirm: boolean): Promise<WorktreeChange | undefined> {
    const previous = this.renameCurrent, reviewed = this.renameReviewedRecord;
    const snapshot = this.renameSnapshot, view = this.view!, id = this.requestId;
    this.renameCurrent = ''; this.renameReviewedRecord = undefined;
    this.error = 'result_unknown';
    const current = await this.liveRenameName(rev);
    this.requireCurrent(rev);
    const owner = this.owner!, journal = owner.repo().mutations, record = journal?.record(id);
    const saved = await view.read();
    this.requireCurrent(rev);
    // A successful journal check survives an interrupted UI update or a failed snapshot removal.
    const alreadyCleared = saved === '' && record?.state === 'checked' && this.canKeepRename(record);
    if (!snapshot || saved !== snapshot && !alreadyCleared) return undefined;
    if (confirm && (previous !== current || reviewed !== record)) { this.error = 'tree_rename_check_again'; return undefined; }
    const wanted = Wire.text(Wire.parse(this.requestBody),'name');
    if (current === wanted) return this.completed(rev,Wire.payload(['ok'],[true]));
    if (!journal || !this.canKeepRename(record) ||
      journal.blocked().some((item: MutationRecord) => item !== record && item.method === 'POST' && item.path === this.requestPath)) return undefined;
    if (!confirm) { this.renameCurrent = current; this.renameReviewedRecord = record; this.error = 'tree_rename_keep_explanation'; return undefined; }
    // A negative readback cannot prove non-delivery. The explicit choice only marks this
    // local record checked; a late reply or another person's title must never cause a POST.
    await journal.acknowledge(id, () => this.current(rev) && journal.record(id) === record && this.canKeepRename(record));
    if (!this.current(rev) || journal.record(id) !== record || record?.state !== 'checked') return undefined;
    owner.unresolvedWrites = journal.blocked();
    await view.clearIfUnchanged(snapshot);
    const remaining = await view.read();
    if (!this.current(rev)) return undefined;
    if (remaining) { this.error = remaining === snapshot ? 'storage_unavailable' : 'request_updated'; return undefined; }
    if (journal.blocked().some((item: MutationRecord) => item.method === 'POST' && item.path === this.requestPath)) {
      this.error = 'pending_action'; return undefined;
    }
    this.title = current; this.name = current; this.reviewedRenameBase = current;
    this.renameOriginal = ''; this.renameSnapshot = ''; this.requestBody = ''; this.requestId = '';
    this.unknown = false; this.ready = true; this.error = '';
    return undefined;
  }
  private async checkMissingLegacy(rev: number, confirm: boolean): Promise<void> {
    const record = this.exactRecord(this.legacyId), at = record ? Date.parse(record.createdAt) : 0;
    const target = JSON.stringify([this.branch.trim(),this.path.trim()]);
    const admitted = this.releaseReady && this.releaseTarget === target;
    this.releaseReady = false;
    if (!record || !this.fresh(at) || Date.now() - at <= NODE_CREATE_TIMEOUT || !validWorktreeBranch(this.branch) ||
      !this.path.trim() || this.automatic || !await this.verifyCreateVersion() || !this.current(rev)) {
      if (this.current(rev)) this.error = 'tree_verify_original_checkout'; return;
    }
    const rows = await this.workspaces(rev);
    if (!this.current(rev)) return;
    const sourceOnline = rows.some((row: Record<string,Object>) => this.sameSource(row));
    const found = rows.some((row: Record<string,Object>) => Wire.text(row,'machine_id') === this.tree!.machine &&
      (checkoutComparable(Wire.text(row,'cwd')) === checkoutComparable(this.path) || Wire.text(row,'branch') === this.branch.trim()));
    if (!sourceOnline || found || this.exactRecord(this.legacyId) !== record) { this.error = 'result_unknown'; return; }
    if (!confirm || !admitted) { this.releaseReady = true; this.releaseTarget = target; this.error = 'tree_verify_original_checkout'; return; }
    await this.refused(rev,'');
    if (this.current(rev)) { this.error = ''; this.legacyId = ''; this.requestId = ''; this.requestBody = ''; }
  }
  private async canRetryMissingCreate(rev: number, id: string): Promise<boolean> {
    if (!await this.verifyCreateVersion() || !this.current(rev)) return false;
    if (this.originalReceiptRoute) return true;
    // An older phone could save a full request without recognizing the Hub's build.
    // Recheck the exact manual target before offering or executing a retry with that same ID.
    const record = this.exactRecord(id), body = Wire.parse(this.requestBody);
    const branch = Wire.text(body, 'branch'), path = Wire.text(body, 'path');
    const recorded = this.requestTime, created = record ? Date.parse(record.createdAt) : 0;
    if (!record || record.state !== 'unknown' || record.confirmedPending !== false || record.requiresReceipt !== false ||
      !this.fresh(recorded) || !this.fresh(created) || Date.now() - Math.max(created, recorded) <= NODE_CREATE_TIMEOUT ||
      this.automatic || Wire.bool(body, 'auto_name') || !validWorktreeBranch(branch) || !path ||
      this.branch.trim() !== branch || this.path.trim() !== path) return false;
    const rows = await this.workspaces(rev, true);
    this.requireCurrent(rev);
    const original = rows.filter((row: Record<string,Object>) => Wire.text(row, 'machine_id') === this.tree!.machine &&
      Wire.text(row, 'source') === this.tree!.source && Wire.text(row, 'source_id') === this.tree!.sourceId);
    const targetExists = rows.some((row: Record<string,Object>) => Wire.text(row, 'machine_id') === this.tree!.machine &&
      (checkoutComparable(Wire.text(row, 'cwd')) === checkoutComparable(path) || Wire.text(row, 'branch') === branch));
    const saved = await this.view!.read();
    this.requireCurrent(rev);
    return saved === this.snapshot() && original.length === 1 && this.sameSource(original[0]) &&
      (!Wire.has(original[0], 'manager_id') || Wire.text(original[0], 'manager_id') === this.tree!.manager) &&
      !targetExists && this.exactRecord(id) === record && record.state === 'unknown' &&
      record.confirmedPending === false && record.requiresReceipt === false && this.fresh(this.requestTime);
  }
  private async recoverCreate(rev: number, replay: boolean): Promise<WorktreeChange | undefined> {
    const id = this.legacy ? this.legacyId : this.requestId, canReplay = this.canReplayDuringCheck(id);
    this.retrySupported = false;
    const op = await this.status(id);
    if (!this.current(rev)) return undefined;
    const state = Wire.text(op,'state'), result = Wire.object(op,'result'), answer = this.receiptBody(result);
    if (state !== 'missing') this.releaseReady = false;
    if (state === 'done' && (this.legacy || this.acknowledged(answer))) return this.completed(rev, answer);
    if (state === 'failed') { await this.refused(rev, Wire.text(answer,'code') || Wire.text(answer,'error') || 'failed'); return undefined; }
    if (state === 'missing' && this.legacy) { await this.checkMissingLegacy(rev,replay); return undefined; }
    if (!['pending','unknown','missing','done'].includes(state)) throw new Error('result_unknown');
    const supported = state !== 'missing' || await this.canRetryMissingCreate(rev, id);
    if (!this.current(rev)) return undefined;
    this.retrySupported = !this.legacy && supported;
    if (state === 'pending' || state === 'unknown') await this.finishRecord(id,state);
    if (!this.current(rev)) return undefined;
    if (this.legacy || !replay || !canReplay || !this.retrySupported || !this.exactRecord(id)) {
      this.error = state === 'pending' ? 'pending' : 'result_unknown'; return undefined;
    }
    const owner = this.owner!, path = this.requestPath, bytes = this.requestBody;
    try {
      const value = Wire.parse(await owner.repo().transport.request(path,'POST',bytes,id));
      if (!this.current(rev)) return undefined;
      if (this.acknowledged(value)) return this.completed(rev, value);
      const pending = Wire.text(value,'state') === 'pending'; await this.finishRecord(id, pending ? 'pending' : 'unknown');
      if (this.current(rev)) this.error = pending ? 'pending' : 'result_unknown';
    } catch (error) {
      if (!this.current(rev)) return undefined;
      if (error instanceof HttpFailure && error.code === 'op_id_reused') { this.retrySupported = false; this.error = 'request_updated'; return undefined; }
      if (error instanceof HttpFailure && !error.resultUnknown) {
        const latest = await this.status(id);
        if (!this.current(rev)) return undefined;
        const lastState = Wire.text(latest,'state'), cached = this.receiptBody(Wire.object(latest,'result'));
        if (lastState === 'failed' || lastState === 'unknown' && Wire.text(cached,'delivered') === 'none')
          await this.refused(rev, Wire.text(cached,'code') || this.failure(error as Error));
        else if (lastState === 'done' && this.acknowledged(cached)) return await this.completed(rev,cached);
        else this.error = lastState === 'pending' ? 'pending' : 'result_unknown';
        return undefined;
      }
      await this.finishRecord(id, error instanceof HttpFailure && error.resultPending ? 'pending' : 'unknown');
      if (this.current(rev)) this.error = error instanceof HttpFailure && error.resultPending ? 'pending' : 'result_unknown';
    }
    return undefined;
  }
  private canReplayDuringCheck(id: string): boolean {
    if (this.legacy || !this.retrySupported || !this.requestBody || !this.fresh(this.requestTime) || !this.exactRecord(id)) return false;
    const body = Wire.parse(this.requestBody);
    return this.automatic === Wire.bool(body,'auto_name') && (this.automatic ||
      this.branch.trim() === Wire.text(body,'branch') && this.path.trim() === Wire.text(body,'path'));
  }
  private ownsCleanupJournal(): boolean {
    const journal = this.owner?.repo().mutations;
    if (!journal) return false;
    const record = journal.record(this.requestId);
    return (!record || record.method === 'POST' && record.path === this.requestPath &&
      (!record.payload || record.payload === this.requestBody) && ['pending','unknown','failed'].includes(record.state)) &&
      !journal.blocked().some((item: MutationRecord) => item.method === 'POST' && item.path === this.requestPath && item !== record);
  }
  private async ownsCleanupSnapshot(rev: number, allowCleared: boolean = false): Promise<boolean> {
    if (!this.current(rev) || !this.cleanupSnapshot || this.tree!.source !== 'herdr' || !this.tree!.permits('clean')) return false;
    const saved = await this.view!.read();
    return this.current(rev) && this.ownsCleanupJournal() && (saved === this.cleanupSnapshot ||
      allowCleared && saved === '' && this.owner!.repo().mutations?.record(this.requestId)?.state === 'failed');
  }
  private async reopenRevokedCleanup(rev: number): Promise<void> {
    if (!await this.ownsCleanupSnapshot(rev, true)) { if (this.current(rev)) this.error = 'request_updated'; return; }
    const journal = this.owner!.repo().mutations!, id = this.requestId, record = journal.record(id);
    const view = this.view!, snapshot = this.cleanupSnapshot, retired = this.token;
    if (record && record.state !== 'failed') {
      await journal.finish(record,'failed',() => this.current(rev) && journal.record(id) === record && this.ownsCleanupJournal());
    }
    if (!this.current(rev) || journal.record(id) !== record || record && record.state !== 'failed' || !this.ownsCleanupJournal()) return;
    this.owner!.unresolvedWrites = journal.blocked();
    await view.clearIfUnchanged(snapshot);
    const saved = await view.read();
    if (!this.current(rev)) return;
    if (saved || !this.ownsCleanupJournal()) { this.error = saved === snapshot ? 'storage_unavailable' : 'request_updated'; return; }
    await this.open(this.root!, this.tree!, 'clean', retired);
  }
  async reconcile(replay: boolean = false): Promise<WorktreeChange | undefined> {
    if (!this.canCheck) return undefined;
    const rev = this.revision, lane = JSON.stringify([this.ownerScope,this.endpoint(this.action === 'clean' ? 'delete' : this.action)]);
    if (submittingRoutes.has(lane)) return undefined;
    submittingRoutes.add(lane); this.busy = true;
    try {
      if (this.action === 'create') return await this.recoverCreate(rev,replay);
      if (this.action==='rename' && this.priorRenameId) { await this.reviewRemovedRename(rev,replay);return undefined; }
      if (this.action === 'rename') return await this.recoverRename(rev,replay);
      const body = this.body(); body['preview_token'] = this.token; body['reconcile'] = true;
      const surrender = replay && this.cleanupRepreviewAvailable;
      this.cleanupRepreviewAvailable = false;
      if (surrender) {
        if (!await this.ownsCleanupSnapshot(rev)) { if (this.current(rev)) this.error = 'request_updated'; return undefined; }
        body['revoke_preview'] = true;
      }
      const result = await this.query('delete', body);
      if (!this.current(rev)) return undefined;
      if (this.removed(result)) return await this.completed(rev, result);
      this.error = 'result_unknown';
      if (Wire.text(result, 'workspace_id') === this.tree!.sourceId && result['removed'] === false) {
        const state = Wire.text(result, 'state');
        if (state === 'preview_revoked' && Wire.text(result, 'revoked_preview_token') === this.token) await this.reopenRevokedCleanup(rev);
        if (state === 'present') {
          const available = result['can_revoke_preview'] === true && await this.ownsCleanupSnapshot(rev);
          if (this.current(rev)) { this.cleanupRepreviewAvailable = available; this.error = 'tree_cleanup_present'; }
        }
        if (state === 'removing') this.error = 'tree_cleanup_removing';
      }
      return undefined;
    } catch (error) {
      if (this.current(rev)) this.error = this.failure(error as Error);
      return undefined;
    } finally { submittingRoutes.delete(lane); if (this.revision === rev) this.busy = false; }
  }
}
