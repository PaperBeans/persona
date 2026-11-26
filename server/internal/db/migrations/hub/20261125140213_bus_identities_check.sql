-- bus_identities.role holds only the bus roles the hub writes (#15397, docs/plans/plan-typed-core.md step P9):
-- bus.RoleManager ('manager', server/internal/bus/bus.go), the only role so far; a new role adds itself here.
-- NOT VALID: new and updated rows are checked, existing rows are not scanned, so the deploy cannot fail on old data; a
-- later migration validates it once the hub's rows have been checked. TestStorageChecksMatchTheCode keeps this list and
-- the constant together.
ALTER TABLE bus_identities ADD CONSTRAINT bus_identities_role_check CHECK (role IN ('manager')) NOT VALID;
