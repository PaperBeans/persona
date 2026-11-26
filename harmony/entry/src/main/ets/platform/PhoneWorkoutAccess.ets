import type { healthStore } from '@kit.HealthServiceKit';

export class WorkoutAccessResult {
  state:string='';
  code:number=0;
  constructor(state:string,code:number=0) {this.state=state;this.code=code;}
}

export async function workoutAccess():Promise<WorkoutAccessResult> {
  if (!canIUse('SystemCapability.Health.HealthStore')) return new WorkoutAccessResult('unavailable');
  try {
    // Resolve the optional kit after checking its native system capability.
    const kit=await import('@kit.HealthServiceKit');
    const workout=kit.healthStore.exerciseSequenceHelper.DATA_TYPE;
    const request:healthStore.AuthorizationRequest={readDataTypes:[workout],writeDataTypes:[]};
    const result=await kit.healthStore.getAuthorizations(request);
    const granted=result.readDataTypes.some((type:healthStore.DataType)=>type.id===workout.id);
    return new WorkoutAccessResult(granted ? 'granted' : 'not_granted');
  } catch (error) {
    const native=error as Object as Record<string,Object>,code=native['code'];
    return new WorkoutAccessResult('unknown',typeof code==='number' && Number.isFinite(code) ? code : 0);
  }
}
