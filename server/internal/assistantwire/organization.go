package assistantwire

const (
	OrganizationSnapshotPath  = "/api/v1/assistant/organization/snapshot"
	OrganizationSnapshotRoute = "GET " + OrganizationSnapshotPath
)

func init() { Routes = append(Routes, OrganizationSnapshotRoute) }
