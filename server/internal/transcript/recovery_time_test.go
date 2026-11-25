package transcript

import (
	"testing"
	"time"
)

func TestProviderRecoveryAt(t *testing.T) {
	at := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		text string
		at   time.Time
		want time.Time
		ok   bool
	}{
		{
			name: "gateway available again dated utc",
			text: "API Error: 503 · no upstream account available; an account becomes available again at 2026-11-23 12:30 UTC",
			at:   at,
			want: time.Date(2026, 11, 23, 12, 30, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "gateway available again dated utc with seconds",
			text: "API Error: 503 · available again at 2026-11-23 12:30:45 UTC",
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 12, 30, 45, 0, time.UTC),
			ok:   true,
		},
		{
			name: "gateway available again undated utc anchored",
			text: "API Error: 503 · no account available; available again at 12:30 UTC (in 2h30m)",
			at:   at,
			want: time.Date(2026, 11, 23, 12, 30, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "gateway available again undated utc rolls to next day",
			text: "API Error: 503 · available again at 08:15 UTC",
			at:   at,
			want: time.Date(2026, 11, 24, 8, 15, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "retry after seconds",
			text: "API Error: 503 Service Unavailable (Retry-After: 900)",
			at:   at,
			want: at.Add(15 * time.Minute),
			ok:   true,
		},
		{
			name: "retry after equals seconds",
			text: "API Error: 429 rate_limit_exceeded · Retry-After=1200",
			at:   at,
			want: at.Add(20 * time.Minute),
			ok:   true,
		},
		{
			name: "retry after http date",
			text: "API Error: 503 Service Unavailable; Retry-After: Mon, 23 Nov 2026 11:45:00 GMT",
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 11, 45, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "retry after http date with trailing prose",
			text: "API Error: 503 overloaded · Retry-After: Mon, 23 Nov 2026 11:45:00 GMT, please wait",
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 11, 45, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "keyword rfc3339",
			text: "API Error: 503 upstream pool exhausted; resets at 2026-11-23T13:15:00+02:00",
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 11, 15, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "json resets_at rfc3339",
			text: `API Error: 503 {"error":{"type":"overloaded_error","resets_at":"2026-11-23T12:00:00Z"}}`,
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "json retry_at epoch seconds",
			text: `API Error: 503 {"error":{"retry_at":1795435200}}`,
			at:   time.Time{},
			want: time.Unix(1795435200, 0).UTC(),
			ok:   true,
		},
		{
			name: "json available_at epoch millis",
			text: `API Error: 503 {"error":{"available_at":1795435200000}}`,
			at:   time.Time{},
			want: time.Unix(1795435200, 0).UTC(),
			ok:   true,
		},
		{
			name: "json retry_after seconds",
			text: `API Error: 503 {"error":{"message":"no account available","retry_after":600}}`,
			at:   at,
			want: at.Add(10 * time.Minute),
			ok:   true,
		},
		{
			name: "json retry_after exponent",
			text: `API Error: 503 {"error":{"retry_after":1.8e3,"type":"overloaded_error"}}`,
			at:   at,
			want: at.Add(30 * time.Minute),
			ok:   true,
		},
		{
			name: "json retry after millis with a signed exponent",
			text: `API Error: 429 {"retryAfterMs":4.5E+5}`,
			at:   at,
			want: at.Add(450 * time.Second),
			ok:   true,
		},
		{
			name: "json recovery timestamp as exponent",
			text: `API Error: 503 {"retry_at":1.7954352e9}`,
			want: time.Unix(1795435200, 0).UTC(),
			ok:   true,
		},
		{
			name: "truncated json recovery timestamp exponent",
			text: `API Error: 503 {"retry_at":1795435200e}`,
		},
		{
			name: "truncated json retry exponent",
			text: `API Error: 503 {"retry_after":6e}`,
			at:   at,
		},
		{
			name: "json retry after with a malformed numeric suffix",
			text: `API Error: 503 {"retry_after":120abc}`,
			at:   at,
		},
		{
			name: "non-json leading zero in retry after",
			text: `API Error: 503 {"retry_after":0600}`,
			at:   at,
		},
		{
			name: "whole json exponent retains the relative bound",
			text: `API Error: 503 {"retry_after":1e8}`,
			at:   at,
		},
		{
			name: "json retry_after http date without anchor",
			text: `API Error: 503 {"error":{"retry_after":"Mon, 23 Nov 2026 11:30:00 GMT"}}`,
			at:   time.Time{},
			want: time.Date(2026, 11, 23, 11, 30, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "json retry_after_ms",
			text: `API Error: 429 {"error":{"retry_after_ms":450000}}`,
			at:   at,
			want: at.Add(450 * time.Second),
			ok:   true,
		},
		{
			name: "try again in duration",
			text: "API Error: 503 Service Unavailable. Please try again in 15m.",
			at:   at,
			want: at.Add(15 * time.Minute),
			ok:   true,
		},
		{
			name: "try again in compound duration",
			text: "API Error: 429 rate_limit_exceeded: Rate limit reached. Please try again in 1h30m.",
			at:   at,
			want: at.Add(90 * time.Minute),
			ok:   true,
		},
		{
			name: "try again in duration separated by words",
			text: "API Error: 503 Try again in 1 hour and 30 minutes.",
			at:   at,
			want: at.Add(90 * time.Minute),
			ok:   true,
		},
		{
			name: "retry in three parts with commas and conjunction",
			text: "API Error: 503 Retry in 1 hr, 30 min, and 0.5 seconds.",
			at:   at,
			want: at.Add(90*time.Minute + 500*time.Millisecond),
			ok:   true,
		},
		{
			name: "compound duration retains the relative bound",
			text: "API Error: 503 Retry after 167 hours and 61 minutes.",
			at:   at,
		},
		{
			name: "retry in minutes",
			text: "API Error: 503 no eligible upstream, retry in 20 minutes",
			at:   at,
			want: at.Add(20 * time.Minute),
			ok:   true,
		},
		{
			name: "explicit timezone reset",
			text: "API Error: 503 account pool exhausted · resets 4:20pm (UTC)",
			at:   at,
			want: time.Date(2026, 11, 23, 16, 20, 0, 0, time.UTC),
			ok:   true,
		},
		{
			name: "missing metadata",
			text: "API Error: 503 no eligible upstream",
			at:   at,
			ok:   false,
		},
		{
			name: "empty text",
			text: "",
			at:   at,
			ok:   false,
		},
		{
			name: "ambiguous clock without zone",
			text: "API Error: 503 Service Unavailable. Try again at 3:05 PM.",
			at:   at,
			ok:   false,
		},
		{
			name: "available again without utc",
			text: "API Error: 503 available again at 18:02",
			at:   at,
			ok:   false,
		},
		{
			name: "malformed clock",
			text: "API Error: 503 available again at 25:02 UTC",
			at:   at,
			ok:   false,
		},
		{
			name: "malformed calendar date",
			text: "API Error: 503 available again at 2026-02-30 18:02 UTC",
			at:   at,
			ok:   false,
		},
		{
			name: "malformed retry after word",
			text: "API Error: 503 Retry-After: soon",
			at:   at,
			ok:   false,
		},
		{
			name: "zero retry after",
			text: "API Error: 503 Retry-After: 0",
			at:   at,
			ok:   false,
		},
		{
			name: "negative retry after",
			text: "API Error: 503 Retry-After: -120",
			at:   at,
			ok:   false,
		},
		{
			name: "alphanumeric malformed retry after",
			text: "API Error: 503 Retry-After: 120abc",
			at:   at,
			ok:   false,
		},
		{
			name: "excessive retry after seconds",
			text: "API Error: 503 Retry-After: 999999999",
			at:   at,
			ok:   false,
		},
		{
			name: "retry after seconds without anchor",
			text: "API Error: 503 Retry-After: 900",
			at:   time.Time{},
			ok:   false,
		},
		{
			name: "undated available again without anchor",
			text: "API Error: 503 available again at 12:30 UTC",
			at:   time.Time{},
			ok:   false,
		},
		{
			name: "json malformed resets_at",
			text: `API Error: 503 {"error":{"resets_at":"soon"}}`,
			at:   at,
			ok:   false,
		},
		{
			name: "json small epoch resets_at rejected",
			text: `API Error: 503 {"error":{"resets_at":60}}`,
			at:   at,
			ok:   false,
		},
		{
			name: "json zero retry_after",
			text: `API Error: 503 {"error":{"retry_after":0}}`,
			at:   at,
			ok:   false,
		},
		{
			name: "json negative retry_after",
			text: `API Error: 503 {"error":{"retry_after":-30}}`,
			at:   at,
			ok:   false,
		},
		{
			name: "try again zero duration",
			text: "API Error: 503 Please try again in 0s.",
			at:   at,
			ok:   false,
		},
		{
			name: "try again non-unit phrase",
			text: "API Error: 503 try again in 5 more minutes",
			at:   at,
			ok:   false,
		},
		{
			name: "try again without anchor",
			text: "API Error: 503 Please try again in 15m.",
			at:   time.Time{},
			ok:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ProviderRecoveryAt(tc.text, tc.at)
			if ok != tc.ok || !got.Equal(tc.want) {
				t.Fatalf("ProviderRecoveryAt(%q, %v) = (%v, %v), want (%v, %v)", tc.text, tc.at, got, ok, tc.want, tc.ok)
			}
			if ok && got.Location() != time.UTC {
				t.Fatalf("ProviderRecoveryAt(%q) location = %v, want UTC", tc.text, got.Location())
			}
		})
	}
}
