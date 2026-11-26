import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.'))
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const {
  agentPendingCards, interactionCardAgent, interactionPermissionTool, interactionPermissionSubject, interactionQuestionHeader, interactionSessionAllowance, interactionAllowanceScope,
  decodeInteractionDecision, interactionCanDefer, interactionCanSubmit, interactionElicitationDefaults, interactionElicitationMessage,
  interactionElicitationMode, interactionElicitationSupported, interactionElicitationUrl, interactionHistoryResolution,
  interactionPermissionFieldPlaceholder, interactionPlanTitle, interactionQuestionOptions, interactionResolution, interactionScreenOptions,
  validateInteractionElicitation
} = await import('../entry/src/main/ets/models/InteractionDecision.ets');

function decision(interaction, extra = {}) { return decodeInteractionDecision({ interaction, ...extra }); }

test('Agent cards show permissions first, followed by the newest questions and plans without changing Hub order', () => {
  const card = (id, kind, created_at) => decision({ id, kind, created_at });
  const cards = [card('plan', 'plan', '2026-01-01T10:00:00.000100Z'), card('choice', 'question', '2026-01-01T10:00:00.000900Z'),
    card('first', 'permission'), card('second', 'permission'), card('answered', 'question')];
  cards[4].status = 'resolved';
  assert.deepEqual(agentPendingCards(cards).map(card => card.id), ['second', 'first', 'choice', 'plan']);
  assert.equal(cards[0].id, 'plan');
  assert.deepEqual(agentPendingCards([], cards[0]), [cards[0]]);
});

test('question answers keep exact labels, custom text takes precedence, and cancel is gated', () => {
  const request = decision({ id: 'question-1', kind: 'question', can_cancel: true, questions: [
    { question: 'Which?', multi_select: true, options: [{ label: 'Alpha', description: 'First' }, { label: 'Beta' }] }
  ] });
  assert.equal(request.fields[0].kind, 'multi_choice');
  assert.equal(interactionQuestionOptions(request, 'Which?')[0].detail, 'First');
  assert.deepEqual(interactionResolution(request, 'answer', { 'Which?': 'Alpha, Beta' }), { answers: { 'Which?': 'Alpha, Beta' } });
  assert.deepEqual(interactionResolution(request, 'answer', { 'Which?': 'custom answer' }), { answers: { 'Which?': 'custom answer' } });
  assert.deepEqual(interactionResolution(request, 'cancel', {}), { cancel: true });
  request.raw = JSON.stringify({ interaction: { id: 'question-1', kind: 'question', questions: [{ question: 'Which?', options: [] }] } });
  assert.throws(() => interactionResolution(request, 'cancel', {}), /unsupported/);
});

test('handing a pending card to the terminal sends only dismiss and is unavailable for assistant ask_user', () => {
  for (const kind of ['plan','permission','question','screen','elicitation']) {
    const card=decision({id:'terminal',kind,tool_name:'other',questions:[{question:'Which?'}]});
    assert.deepEqual(interactionResolution(card,'dismiss',{mode:'bypassPermissions',feedback:'private draft'}),{dismiss:true});
    card.status='resolved';
    assert.throws(()=>interactionResolution(card,'dismiss',{}),/not_pending/);
  }
  const ask=decision({id:'ask',kind:'question',tool_name:'ask_user',questions:[{question:'Which?'}]});
  assert.throws(()=>interactionResolution(ask,'dismiss',{}),/unsupported/);
});

test('Later is eligible only for an open ask_user question in an editable Persona conversation', () => {
  const request = decision({ id: 'ask-later', kind: 'question', tool_name: 'ask_user', questions: [{question: 'Which?'}] });
  assert.equal(interactionCanDefer(request, 'conversation', false, false), true);
  assert.equal(interactionCanDefer(request, 'conversation', true, false), false);
  assert.equal(interactionCanDefer(request, 'conversation', false, true), false);
  assert.equal(interactionCanDefer(request, 'session', false, false), false);
  assert.equal(interactionCanDefer(decision({ id: 'permission-later', kind: 'permission', tool_name: 'ask_user' }), 'conversation', false, false), false);
  request.status = 'pending';
  assert.equal(interactionCanDefer(request, 'conversation', false, false), false);
});

test('plan and permission bodies match proto.Resolution and never send denied secret values', () => {
  const plan = decision({ id: 'plan-1', kind: 'plan', plan: 'Proceed?' });
  assert.deepEqual(interactionResolution(plan, 'approve', {}), { approve: true });
  assert.deepEqual(interactionResolution(plan, 'deny', { feedback: 'Use another path' }), { approve: false, feedback: 'Use another path' });

  const permission = decision({ id: 'permission-1', kind: 'permission', fields: [
    { key: 'token', label: 'Token', placeholder: 'Paste token', secret: true },
    { key: 'note', label: 'Note', optional: true }
  ] });
  assert.equal(interactionPermissionFieldPlaceholder(permission, 'token'), 'Paste token');
  assert.deepEqual(interactionResolution(permission, 'approve', { token: 'one-time', note: 'safe' }), {
    allow: true, values: { token: 'one-time', note: 'safe' }
  });
  assert.deepEqual(interactionResolution(permission, 'deny', { token: 'one-time' }), { allow: false });
  assert.throws(() => interactionResolution(permission, 'approve', {}), /required/);
});

test('screen form displays labels, resolves stable keys, handles multi-select, and rejects a changed revision', () => {
  const request = decision({ id: 'screen-1', kind: 'screen', dialog: {
    question: 'Continue?', rev: 'rev-7', multi: false, options: [{ key: 'continue_key', label: 'Continue' }, { key: 'cancel_key', label: 'Cancel' }]
  } });
  assert.equal(request.options[0], 'Continue');
  assert.equal(interactionScreenOptions(request)[0].key, 'continue_key');
  assert.deepEqual(interactionResolution(request, 'continue_key', {}, 'rev-7'), { option: 'continue_key' });
  assert.throws(() => interactionResolution(request, 'continue_key', {}, 'rev-6'), /request_updated/);

  const multi = decision({ id: 'screen-multi', kind: 'screen', dialog: {
    rev: 'rev-8', multi: true, submit: 'Save choices', options: [{ key: 'x', label: 'X' }, { key: 'y', label: 'Y' }]
  } });
  assert.deepEqual(interactionResolution(multi, 'submit', { options: ['y'], text: 'Other' }, 'rev-8'), {
    options: ['y'], text: 'Other'
  });
});

test('elicitation form validates typed schema values and excludes secret defaults', () => {
  const request = decision({ id: 'form-1', kind: 'elicitation', elicitation: {
    mode: 'form', message: 'Fill in the request', requested_schema: {
      type: 'object', required: ['email', 'count', 'notify'], properties: {
        email: { type: 'string', format: 'email' }, count: { type: 'integer', minimum: 1 },
        notify: { type: 'boolean' }, color: { type: 'string', enum: ['red', 'blue'], enumNames: ['Red', 'Blue'] },
        tags: { type: 'array', items: { type: 'string', enum: ['one', 'two'] }, maxItems: 2 },
        secret: { type: 'string', format: 'password', default: 'do-not-fill' }
      }
    }
  } });
  assert.equal(request.prompt, 'Fill in the request');
  assert.equal(interactionElicitationMessage(request), 'Fill in the request');
  assert.equal(interactionElicitationSupported(request), true);
  assert.equal(interactionElicitationMode(request), 'form');
  assert.equal(interactionElicitationDefaults(request).secret, undefined);
  assert.equal(request.fields.find((field) => field.key === 'secret').kind, 'secret');

  const invalid = validateInteractionElicitation(request, { email: 'bad', count: '', notify: false, color: 'red', tags: ['one'] });
  assert.equal(invalid.valid, false);
  assert.equal(invalid.errors.email, 'invalid');
  assert.equal(invalid.errors.count, 'invalid', 'blank numeric input is not silently coerced to zero');
  const valid = validateInteractionElicitation(request, { email: 'a@example.test', count: '2', notify: false, color: 'red', tags: ['one', 'two'] });
  assert.equal(valid.valid, true);
  assert.deepEqual(valid.content, { email: 'a@example.test', count: 2, notify: false, color: 'red', tags: ['one', 'two'] });
  assert.deepEqual(interactionResolution(request, 'accept', {
    email: 'a@example.test', count: '2', notify: false, color: 'red', tags: ['one'], secret: 'one-time'
  }), { action: 'accept', content: { email: 'a@example.test', count: 2, notify: false, color: 'red', tags: ['one'], secret: 'one-time' } });
});

test('elicitation URLs require an exact safe HTTP(S) open and preserve separate cancel/decline actions', () => {
  const request = decision({ id: 'url-1', kind: 'elicitation', elicitation: { mode: 'url', message: 'Open this page', url: 'https://example.test/path?x=1' } });
  const url = interactionElicitationUrl(request);
  assert.equal(url, 'https://example.test/path?x=1');
  assert.throws(() => interactionResolution(request, 'accept', {}), /required/);
  assert.deepEqual(interactionResolution(request, 'accept', { opened_url: url }), { action: 'accept' });
  assert.deepEqual(interactionResolution(request, 'decline', {}), { action: 'decline' });
  assert.deepEqual(interactionResolution(request, 'cancel', {}), { action: 'cancel' });
  assert.equal(interactionElicitationUrl(decision({ id: 'bad-url', kind: 'elicitation', elicitation: { mode: 'url', url: 'https://user@example.test/' } })), '');
  assert.equal(interactionElicitationUrl(decision({ id: 'other-mode', kind: 'elicitation', elicitation: { mode: 'form', url: 'https://example.test/' } })), '');
});

test('only escalation releases a held permission, plan or parsed terminal choice; a delegated question remains answerable', () => {
  const inputs=[
    [{kind:'permission',tool_name:'Read',tool_input:{file_path:'README.md'}},'allow',{}, {allow:true}],
    [{kind:'plan',plan:'Review the documentation'},'approve',{}, {approve:true}],
    [{kind:'screen',dialog:{question:'Which branch?',options:[{key:'1',label:'main'},{key:'2',label:'docs'}],rev:'r1'}},'1',{}, {option:'1'}]
  ];
  for(const [data,choice,values,expected] of inputs){
    const held=decision({id:'from-supervisor',...data,delegating:true});
    assert.equal(interactionCanSubmit(held),false,data.kind);
    assert.throws(()=>interactionResolution(held,choice,values),/not_pending/);
    assert.throws(()=>interactionResolution(held,'dismiss',{}),/not_pending/);
    for(const flags of [{},{delegating:false,delegate:{reason:'Please decide'}}]){
      const released=decision({id:'from-supervisor',...data,...flags});
      assert.deepEqual(interactionResolution(released,choice,values),expected);
    }
  }
  const question=decision({id:'q',kind:'question',delegating:true,questions:[{question:'Which color?',options:[{label:'Blue'}]}]});
  assert.deepEqual(interactionResolution(question,'answer',{'Which color?':'Blue'}),{answers:{'Which color?':'Blue'}});
});

test('history entries expose safe outcomes and cannot be submitted', () => {
  const resolved = decision({ id: 'history-1', kind: 'permission', fields: [{ key: 'token', label: 'Token', secret: true }] }, {
    status: 'resolved', resolution: { allow: true, values: { token: 'secret-value' } }
  });
  assert.equal(interactionCanSubmit(resolved), false);
  assert.equal(interactionCanSubmit(decision({ id: 'open-history', kind: 'plan' }), true), false);
  assert.deepEqual(interactionHistoryResolution(resolved), { allow: true });
  assert.equal(JSON.stringify(interactionHistoryResolution(resolved)).includes('secret-value'), false);
});

test('plan controls send exactly the mode selected on this request and preserve a recorded follow-up mode', () => {
  const original=decision({id:'plan',kind:'plan',plan:'Review'});
  assert.deepEqual(interactionResolution(original,'approve',{mode:'acceptEdits'}),{approve:true,mode:'acceptEdits'});
  assert.deepEqual(interactionResolution(original,'approve',{mode:'bypassPermissions'}),{approve:true,mode:'bypassPermissions'});
  assert.deepEqual(interactionResolution(original,'approve',{}),{approve:true});
  assert.deepEqual(interactionResolution(original,'deny',{mode:'bypassPermissions',feedback:'Revise it'}),{approve:false,feedback:'Revise it'});
  assert.throws(()=>interactionResolution(original,'approve',{mode:'unexpected'}),/request_updated/);
  const pinned=decision({id:'pinned',kind:'plan',plan_then:'default',plan:'Review'});
  assert.throws(()=>interactionResolution(pinned,'approve',{mode:'bypassPermissions'}),/request_updated/);
  assert.deepEqual(interactionResolution(pinned,'approve',{mode:'default'}),{approve:true,mode:'default'});
});

test('plan reading uses the first meaningful Markdown line for the sheet title', () => {
  assert.equal(interactionPlanTitle('\n ### **支持 HEIC** ##\n1. 检查图像','计划'), '支持 HEIC');
  assert.equal(interactionPlanTitle('##\n* ** #\n  1. Preserve EXIF  \n- Continue','Plan'), '1. Preserve EXIF');
  assert.equal(interactionPlanTitle('\n###   **\n','計画'), '計画');
});

test('the inline approval opens its native plan sheet from a stable card and closes to the pending request', () => {
  const form=readFileSync(new URL('../entry/src/main/ets/components/InteractionDecisionForm.ets',import.meta.url),'utf8');
  assert.match(form,/if \(this\.admission\(\)\) this\.fullPlan=true;[\s\S]*?id\('interaction.task.expand_plan'\)/);
  assert.match(form,/id\('interaction.card'\)\s*\.bindSheet\(\$\$this\.fullPlan,this\.planReader\(\),\{height:SheetSize\.LARGE/);
  assert.match(form,/this\.fullPlan=false\)\.id\('interaction.task.reader.close'\)/);
  assert.match(form,/aboutToDisappear\(\): void \{ this\.fullPlan=false;/);
});

test('agent permission cards show their tool input and send session approval only where the hub honors it', () => {
  const permission=(data={})=>decision({id:'photo-check',kind:'permission',session_id:'photo',tool_name:'Bash',
    tool_input:{command:'python3 -m unittest discover',description:'Run checks'},...data});
  const current=permission();
  assert.equal(interactionPermissionSubject(current),'python3 -m unittest discover');
  assert.equal(interactionPermissionTool(permission({tool_title:'Test runner'})),'Test runner');
  assert.equal(interactionPermissionSubject(permission({summary:'Run project tests'})),'Run project tests');
  assert.equal(interactionPermissionSubject(permission({tool_name:'Read',tool_input:{file_path:'README.md',description:'Read it'}})),'README.md');
  assert.equal(interactionPermissionSubject(permission({tool_name:'Other',tool_input:{z:2,a:'p'}})),'a: p\nz: 2');
  assert.equal(interactionPermissionSubject(permission({tool_name:'Other',fields:[{key:'password',secret:true}],tool_input:{password:'private',file:'doc'}})),'file: doc');
  assert.equal(interactionPermissionSubject(permission({tool_name:'secret_run'})),'');
  assert.equal(interactionSessionAllowance(current),true);
  assert.equal(interactionAllowanceScope(current),'decision_scope_commands');
  assert.deepEqual(interactionResolution(current,'allow_session',{}),{allow:true,always:true});
  assert.deepEqual(interactionResolution(current,'approve',{always:true}),{allow:true});
  assert.deepEqual(interactionResolution(current,'deny',{always:true}),{allow:false});
  for (const data of [{always_honored:false},{session_id:''},{agent:'codex'}, {agent:'paseo'}, {tool_name:'secret_handoff',always_honored:true}]) {
    const request=permission(data);
    assert.equal(interactionSessionAllowance(request),false);
    assert.throws(()=>interactionResolution(request,'allow_session',{}),/unsupported/);
  }
  assert.equal(interactionSessionAllowance(permission({agent:'codex',always_label:'This command'})),true);
  assert.equal(interactionSessionAllowance(permission({agent:'codex',always_honored:true})),true);
  assert.equal(interactionAllowanceScope(permission({always_label:'Only this workspace'})),'Only this workspace');
  assert.equal(interactionAllowanceScope(permission({always_keys:['tool:Read(/tmp/**)']})),'decision_scope_reads');
  const gated=permission({fields:[{key:'token',secret:true}]});
  assert.throws(()=>interactionResolution(gated,'allow_session',{}),/required/);
  assert.deepEqual(interactionResolution(gated,'allow_session',{token:'user-entered'}),{allow:true,always:true,values:{token:'user-entered'}});
});

test('agent cards use the actual question header and one-tap sends share the same admission checks as every reply',()=>{
  const first=decision({id:'q',kind:'question',agent:'codex',questions:[{header:'Format',question:'Which format?',options:[{label:'JPEG',description:'Stable export'}]}]});
  const field=first.fields[0],choice=interactionQuestionOptions(first,field.key)[0];
  assert.equal(interactionQuestionHeader(first,field.key),'Format');
  assert.equal(interactionCardAgent(first,{}),'Codex');
  assert.equal(interactionCardAgent(decision({kind:'question',tool_name:'ask_user'}),{}),'Persona');
  const src=readFileSync(new URL('../entry/src/main/ets/components/InteractionDecisionForm.ets',import.meta.url),'utf8');
  const names=['sendPermission','chooseAgentAnswer'];
  const methods=names.map(name=>src.match(new RegExp('^  private '+name+'\\([^]*?^  }','m'))[0]);
  const Form=new Function('interactionSessionAllowance','return '+stripTypeScriptTypes('class Form {'+methods.join('\n')+'}'))(interactionSessionAllowance);
  let admitted=false;const submitted=[],form=Object.assign(new Form(),{request:first,questionPage:0,
    canRespond:()=>admitted,pickQuestion(f,c){this.pick=c.label;},submit(action){submitted.push(action);}});
  form.chooseAgentAnswer(field,choice);assert.deepEqual(submitted,[]);
  admitted=true;form.chooseAgentAnswer(field,choice);assert.equal(form.pick,'JPEG');assert.deepEqual(submitted,['answer']);
  form.chooseAgentAnswer({...field,kind:'multi_choice'},choice);assert.deepEqual(submitted,['answer']);
  form.request=decision({id:'a',kind:'permission',always_honored:false});
  form.sendPermission('allow_session');assert.deepEqual(submitted,['answer']);
  form.sendPermission('deny');assert.deepEqual(submitted,['answer','deny']);
  admitted=false;form.sendPermission('allow');assert.deepEqual(submitted,['answer','deny']);
});
