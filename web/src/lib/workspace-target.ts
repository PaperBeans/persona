/** A runtime-owned destination. Paths and display labels are not workspace identities. */
export interface WorkspaceTarget {
  machineId: string;
  workspaceId: string;
  host: string;
  cwd: string;
  label?: string;
  /** The opaque credentialOwner() generation of the catalog that supplied this target. */
  owner?: string;
  /** The resolved machine manager, including the default manager. */
  manager?: string;
}
