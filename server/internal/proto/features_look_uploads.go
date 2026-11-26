package proto

// FeatureLookUploadsStates announces per-state pictures, animated uploads, theme color and editing for uploaded avatars (#15449).
const FeatureLookUploadsStates = "look_uploads_states"

func init() { Features = append(Features, FeatureLookUploadsStates) }
