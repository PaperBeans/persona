import { Wire } from './Wire';

export class TaskStartupQuestion {
  readonly line:string;
  readonly choices:string[];
  readonly kind:string;
  readonly screen:string;
  readonly secret:boolean;
  readonly defaultChoice:string;
  readonly until:number;
  readonly attempt:string;

  constructor(args:Record<string,Object>, attempt:string) {
    this.line=Wire.text(args,'line'); this.kind=Wire.text(args,'kind');
    this.screen=Wire.text(args,'screen'); this.secret=Wire.text(args,'secret')==='true' || this.kind==='secret';
    this.defaultChoice=Wire.text(args,'default'); this.attempt=attempt;
    const time=Wire.text(args,'kept_until');
    this.until=/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(time) ? Date.parse(time) : NaN;
    this.choices=Array.from(new Set<string>(Wire.text(args,'choices').split(',').map((value:string)=>value.trim()).filter((value:string)=>
      !!value && (!this.secret || value==='cancel') && (this.kind!=='ssh_host_key' || value==='no' || value==='cancel'))));
  }
  open(now:number=Date.now()):boolean { return !!this.line && !!this.choices.length && (!Number.isFinite(this.until) || this.until>now); }

  label(choice:string):string {
    if (choice==='enter') return this.defaultChoice==='y' ? 'task_start_enter_yes' : this.defaultChoice==='n' ? 'task_start_enter_no' : 'task_start_enter';
    if (choice==='cancel' || choice==='yes' || choice==='no') return 'task_start_'+choice;
    const names:Record<string,string>={'update:y':'update','update:n':'not_now','zsh_newuser:q':'not_now','compinit:y':'continue',
      'compinit:n':'stop_compinit','zsh_newuser:0':'empty_zshrc','p10k_wizard:q':'quit_wizard','rvm_trust:n':'no_trust','rvm_trust:c':'cancel_key'};
    const action=names[this.kind+':'+choice] || (choice==='y' ? 'yes_key' : choice==='n' ? 'no_key' : 'press');
    return 'task_start_'+action;
  }
}
