// Package wireids is the contract's ID types: MachineID, SessionID, InteractionID, TodoID, ConversationID, HubID,
// DeviceID, ExtensionID, ClientMsgID, InboxItemID, ConnectorID and SubscriptionID, and AgentRef, the one shape for
// "points at an Agent". They are generated from schema/wire/ids.schema.json (ids_gen.go, never edited by hand) in
// every client's language too, so an ID of one kind cannot be passed where another is expected
// (docs/plans/plan-typed-core.md 2.3, #15397). Types only: core's opaque internal/agentref types stay as they are, and
// an adapter converts between the two exactly once.
//
//persona:layer contract
package wireids
