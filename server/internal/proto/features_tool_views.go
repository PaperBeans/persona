package proto

// FeatureToolViews advertises out-of-band declarative views attached to transcript items and folds (Item.ToolViews,
// Fold.ToolViews) from connector tools.call answers (connector_result.views).
const FeatureToolViews = "tool_views"

func init() { Features = append(Features, FeatureToolViews) }
