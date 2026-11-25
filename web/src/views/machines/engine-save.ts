// Tracks the latest engine save request per machine so an earlier in-flight PUT settling out of order cannot overwrite
// a newer save in the store (e.g. after switching machines A -> B -> A and saving again on A).
const latestByMachine = new Map<string, number>();
let nextId = 0;

export function beginEngineSave(machineId: string): number {
  const id = ++nextId;
  latestByMachine.set(machineId, id);
  return id;
}

export function isLatestEngineSave(machineId: string, id: number): boolean {
  return latestByMachine.get(machineId) === id;
}

export function finishEngineSave(machineId: string, id: number): void {
  if (latestByMachine.get(machineId) === id) latestByMachine.delete(machineId);
}
