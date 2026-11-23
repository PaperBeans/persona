// Gemini API key provider (#13762): agy signed in with a key and, usually, a self-hosted Gemini-compatible gateway
// instead of a Google account. The key only ever travels in the environment of the agy child process.
import{readFileSync}from"node:fs";import{join}from"node:path";import{discoverModels}from"./models.js";

const API_KEY_METHOD="gemini-api-key",SETTINGS='{"modelProvider":"gemini"}',DEFAULT_BASE="https://generativelanguage.googleapis.com",MAX_PAGES=5;

function settingsProvider(agyDir){
  try{
    const value=JSON.parse(readFileSync(join(agyDir,"settings.json"),"utf8"))?.modelProvider;
    return typeof value==="string"?value.trim().toLowerCase():"";
  }catch{return""}
}

// True when agy runs on a Gemini API key. PERSONA_AGY_MODEL_PROVIDER (set by the node from a Persona-managed agy
// launcher) wins over agy's own settings.json `modelProvider`.
function geminiKeyMode({env=process.env,agyDir}={}){
  const forced=String(env.PERSONA_AGY_MODEL_PROVIDER||"").trim().toLowerCase();
  return(forced||settingsProvider(agyDir))==="gemini";
}

// Wraps a log function so the API key never reaches a log line, whatever agy prints.
function redactLog(log,env=process.env){
  return msg=>{
    const key=String(env.GEMINI_API_KEY||"").trim();
    log(key&&typeof msg==="string"?msg.split(key).join("***"):msg);
  };
}

function modelsUrl(env,pageToken){
  const base=String(env.GOOGLE_GEMINI_BASE_URL||"").trim().replace(/\/+$/,"")||DEFAULT_BASE;
  const url=new URL(/\/v1(?:beta|alpha)?$/.test(base)?`${base}/models`:`${base}/v1beta/models`);
  url.searchParams.set("pageSize","1000");
  if(pageToken)url.searchParams.set("pageToken",pageToken);
  return url;
}

// The model names the provider serves (GET {base}/v1beta/models). Errors never carry the URL or the key.
async function gatewayModelNames({env=process.env,timeout=25e3,signal,fetchImpl=globalThis.fetch}={}){
  const key=String(env.GEMINI_API_KEY||"").trim();
  if(!key)throw new Error("agy uses a Gemini API key provider but GEMINI_API_KEY is not set for the Persona companion");
  const names=new Set;let pageToken="";
  for(let page=0;page<MAX_PAGES;page++){
    const timer=AbortSignal.timeout(timeout),res=await fetchImpl(modelsUrl(env,pageToken),{
      headers:{"x-goog-api-key":key},signal:signal?AbortSignal.any([signal,timer]):timer
    }).catch(err=>{throw new Error(`Gemini provider model list failed: ${err?.cause?.code||err?.name||"network error"}`)});
    if(!res.ok)throw new Error(`Gemini provider model list answered HTTP ${res.status}`);
    const body=await res.json().catch(()=>({}));
    for(const model of Array.isArray(body.models)?body.models:[]){
      const id=String(model?.baseModelId||model?.name||"").replace(/^models\//,""),methods=model?.supportedGenerationMethods;
      if(id&&!(Array.isArray(methods)&&!methods.some(m=>m==="generateContent"||m==="streamGenerateContent")))names.add(id);
    }
    pageToken=typeof body.nextPageToken==="string"?body.nextPageToken:"";
    if(!pageToken)break;
  }
  if(!names.size)throw new Error("Gemini provider returned no models");
  return names;
}

// agy only runs the model ids it knows (session/set_model refuses the rest), and in this mode `agy models` prints its whole
// built-in catalog. So the list is agy's ids that the provider serves: an id is served when the provider has a model of that
// name, or of that name without agy's reasoning-effort suffix (agy `gemini-4-neon-high` is the provider's `gemini-4-neon`).
const EFFORT_SUFFIX=/-(?:low|medium|high|xhigh|max)$/;
const served=(names,id)=>names.has(id)||names.has(id.replace(EFFORT_SUFFIX,""));

async function discoverProviderModels(bin,{env=process.env,timeout,signal,fetchImpl,discover=discoverModels}={}){
  const names=await gatewayModelNames({env,timeout,signal,fetchImpl});
  const key=String(env.GEMINI_API_KEY||"").trim();
  try{
    return await discover(bin,{env,timeout,signal,args:["--settings",SETTINGS],keep:id=>served(names,id)});
  }catch(err){
    if(key&&String(err?.message).includes(key))err.message=err.message.split(key).join("***");
    throw err;
  }
}

export{API_KEY_METHOD,discoverProviderModels,gatewayModelNames,served,geminiKeyMode,redactLog};
