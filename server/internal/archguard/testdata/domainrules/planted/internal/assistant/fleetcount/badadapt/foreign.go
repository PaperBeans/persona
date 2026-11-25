package badadapt

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sort"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
)

// IntoHeader hands the reason out through another package's named map: the caller reads the header back.
func IntoHeader(w fleetcount.Why, h http.Header) { // adapter-exported-result
	h.Set("why", WhyWord(w))
}

// IntoValues does the same through url.Values.
func IntoValues(w fleetcount.Why, v url.Values) { // adapter-exported-result
	v.Set("why", WhyWord(w))
}

// IntoSlice hands it out through another package's named slice.
func IntoSlice(w fleetcount.Why, s sort.StringSlice) { // adapter-exported-result
	s[0] = WhyWord(w)
}

// IntoRequest hands it out through a struct of another package: its header is a map the caller holds too.
func IntoRequest(w fleetcount.Why, r *http.Request) { // adapter-exported-result
	r.Header.Set("why", WhyWord(w))
}

// WithWhy hands it out through an interface of another package whose method takes any value.
func WithWhy(ctx context.Context, w fleetcount.Why) { // adapter-exported-result
	ctx.Value(WhyWord(w))
}

// WriteWhy writes the reason to a sink, the one kind of another package's type the rule leaves to review.
func WriteWhy(w fleetcount.Why, out io.Writer) error {
	_, err := io.WriteString(out, WhyWord(w))
	return err
}
