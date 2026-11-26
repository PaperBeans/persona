import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {registerHooks,stripTypeScriptTypes} from 'node:module';

registerHooks({
  resolve(spec,ctx,next){
    if(spec==='@kit.HealthServiceKit')return {url:'test-health:scope',shortCircuit:true};
    return next(spec,ctx);
  },
  load(url,ctx,next){
    if(url==='test-health:scope')return {format:'module',shortCircuit:true,source:`export const healthStore={exerciseSequenceHelper:{DATA_TYPE:{id:101}},getAuthorizations:(request)=>globalThis.__readWorkoutScope(request)};`};
    if(url.endsWith('/PhoneWorkoutAccess.ets'))return {format:'module',shortCircuit:true,source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8'))};
    return next(url,ctx);
  }
});
const {workoutAccess}=await import('../entry/src/main/ets/platform/PhoneWorkoutAccess.ets');
test('workout access checks only the read authorization, distinguishing device, scope and service refusal',async()=>{
 let offered=false,calls=0,response={readDataTypes:[],writeDataTypes:[]};
 globalThis.canIUse=cap=>cap==='SystemCapability.Health.HealthStore'&&offered;
 globalThis.__readWorkoutScope=request=>{calls++;assert.deepEqual(request,{readDataTypes:[{id:101}],writeDataTypes:[]});if(response instanceof Error)throw response;return response;};
 assert.equal((await workoutAccess()).state,'unavailable');assert.equal(calls,0);
 offered=true;assert.equal((await workoutAccess()).state,'not_granted');
 response={readDataTypes:[{id:99},{id:101}],writeDataTypes:[]};assert.equal((await workoutAccess()).state,'granted');
 response=Object.assign(Error('private platform detail'),{code:201});const failed=await workoutAccess();
 assert.deepEqual({...failed},{state:'unknown',code:201});assert.equal(calls,3);
});
