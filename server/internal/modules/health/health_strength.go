package health

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func strengthJSON(v any) (string, error) { b, err := json.Marshal(v); return string(b), err }
func strengthMatch(v string) string      { return strings.ToLower(strings.Join(strings.Fields(v), " ")) }
func strengthBest(sets []assistantwire.StrengthSet) map[string]float64 {
	weight, e1rm := 0.0, 0.0
	for _, s := range sets {
		if !s.Warmup {
			weight = math.Max(weight, s.Weight)
			e1rm = math.Max(e1rm, s.Weight*(1+float64(s.Reps)/30))
		}
	}
	return map[string]float64{"weight": weight, "e1rm": e1rm}
}
func (s *Service) strengthAdded(ctx context.Context) (bool, error) {
	items, err := s.Items(ctx)
	for _, item := range items {
		if item == "strength" {
			return true, err
		}
	}
	return false, err
}
