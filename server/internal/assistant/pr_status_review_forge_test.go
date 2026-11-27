package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPRReviewWakeForgeEvidence(t *testing.T) {
	type reviewRow struct {
		State       string `json:"state"`
		CommitID    string `json:"commit_id,omitempty"`
		SubmittedAt string `json:"submitted_at,omitempty"`
		User        struct {
			Login string `json:"login,omitempty"`
		} `json:"user"`
	}
	makeReview := func(login, state, commit, at string) reviewRow {
		r := reviewRow{State: state, CommitID: commit, SubmittedAt: at}
		r.User.Login = login
		return r
	}
	marshalReviews := func(t *testing.T, rows []reviewRow) string {
		t.Helper()
		b, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	const (
		head = "abc123"
		t1   = "2026-11-17T01:02:03Z"
		t2   = "2026-11-17T02:03:04Z"
		t3   = "2026-11-17T03:04:05Z"
	)
	cases := []struct {
		name          string
		reviews       func(*testing.T) string
		secondPage    string
		wantPageError bool
		wantAt        string
		wantReview    string
	}{
		{
			name: "current head active request",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1)})
			},
			wantAt: t1, wantReview: "changes_requested",
		},
		{
			name: "latest active request across reviewers",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{
					makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1),
					makeReview("reviewer-b", "CHANGES_REQUESTED", head, t3),
					makeReview("reviewer-c", "CHANGES_REQUESTED", head, t2),
				})
			},
			wantAt: t3, wantReview: "changes_requested",
		},
		{
			name: "later approval clears request",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{
					makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1),
					makeReview("reviewer-a", "APPROVED", head, t2),
				})
			},
			wantReview: "approved",
		},
		{
			name: "later dismissal clears request",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{
					makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1),
					makeReview("reviewer-a", "DISMISSED", head, t2),
				})
			},
			wantReview: "pending",
		},
		{
			name: "old head request has no wake time",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("reviewer-a", "CHANGES_REQUESTED", "oldhead", t1)})
			},
			wantReview: "changes_requested",
		},
		{
			name: "missing commit has no wake time",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("reviewer-a", "CHANGES_REQUESTED", "", t1)})
			},
			wantReview: "changes_requested",
		},
		{
			name: "missing submission time has no wake time",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("reviewer-a", "CHANGES_REQUESTED", head, "")})
			},
			wantReview: "changes_requested",
		},
		{
			name: "missing login has no wake time",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("", "CHANGES_REQUESTED", head, t1)})
			},
			wantReview: "changes_requested",
		},
		{
			name: "blank login has no wake time",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{makeReview("  \t", "CHANGES_REQUESTED", head, t1)})
			},
			wantReview: "changes_requested",
		},
		{
			name: "commented does not clear active request",
			reviews: func(t *testing.T) string {
				return marshalReviews(t, []reviewRow{
					makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1),
					makeReview("reviewer-a", "COMMENTED", head, t2),
				})
			},
			wantAt: t1, wantReview: "changes_requested",
		},
		{
			name: "later approval clears a request on the first page",
			reviews: func(t *testing.T) string {
				rows := make([]reviewRow, 100)
				for i := range rows {
					rows[i] = makeReview("reviewer-a", "COMMENTED", head, t1)
				}
				rows[0] = makeReview("reviewer-a", "CHANGES_REQUESTED", head, t1)
				return marshalReviews(t, rows)
			},
			secondPage: `[{"user":{"login":"reviewer-a"},"state":"APPROVED","commit_id":"abc123","submitted_at":"2026-11-17T03:04:05Z"}]`,
			wantReview: "approved",
		},
		{
			name: "incomplete list provides no verdict",
			reviews: func(t *testing.T) string {
				rows := make([]reviewRow, 100)
				rows[0] = makeReview("reviewer-a", "APPROVED", head, t1)
				return marshalReviews(t, rows)
			},
			wantPageError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviews := tc.reviews(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body string
				switch r.URL.RequestURI() {
				case "/repos/example/app/pulls/12":
					body = `{"state":"open","head":{"sha":"` + head + `"}}`
				case "/repos/example/app/pulls/12/reviews?per_page=100":
					body = reviews
				case "/repos/example/app/pulls/12/reviews?per_page=100&page=2":
					if tc.wantPageError {
						http.Error(w, "reviews unavailable", http.StatusServiceUnavailable)
						return
					}
					body = tc.secondPage
				case "/repos/example/app/commits/abc123/status":
					body = `{"state":"success","total_count":0}`
				case "/repos/example/app/commits/abc123/check-runs?per_page=100&filter=latest":
					body = `{"total_count":0,"check_runs":[]}`
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.Error(w, "unexpected", http.StatusNotFound)
					return
				}
				_, _ = fmt.Fprint(w, body)
			}))
			defer srv.Close()

			got, err := (githubForge{base: srv.URL, client: srv.Client()}).PRStatus(context.Background(), "example/app", 12)
			if tc.wantPageError {
				if err == nil || got.Review != "" || got.ChangesRequestedAt != nil {
					t.Fatalf("incomplete reviews gave status %+v and error %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("PRStatus: %v", err)
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal status: %v", err)
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatalf("decode status JSON: %v", err)
			}
			var reviewLabel string
			if err := json.Unmarshal(payload["review"], &reviewLabel); err != nil {
				t.Fatalf("decode existing review label: %v", err)
			}
			if reviewLabel != tc.wantReview {
				t.Errorf("review label = %q, want %q", reviewLabel, tc.wantReview)
			}
			value, present := payload["changes_requested_at"]
			if tc.wantAt == "" {
				if present {
					t.Errorf("changes_requested_at unexpectedly present: %s", value)
				}
				return
			}
			if !present {
				t.Fatalf("changes_requested_at missing from JSON: %s", raw)
			}
			var gotAt string
			if err := json.Unmarshal(value, &gotAt); err != nil {
				t.Fatalf("decode changes_requested_at: %v", err)
			}
			if gotAt != tc.wantAt {
				t.Errorf("changes_requested_at = %q, want %q", gotAt, tc.wantAt)
			}
			if strings.TrimSpace(gotAt) != gotAt {
				t.Errorf("changes_requested_at is not normalized: %q", gotAt)
			}
		})
	}
}

const feedbackTestPullPath = "/repos/example/app/pulls/12"

type feedbackTestForge struct {
	mu            sync.Mutex
	head          string
	headAfterList string
	reviews       []githubPRReview
	inline        map[int64][]githubPRInlineComment
	requests      map[string]int
	failRequest   string
}

func newFeedbackTestForge(t *testing.T) (*feedbackTestForge, githubForge) {
	t.Helper()
	f := &feedbackTestForge{head: "head-one", requests: make(map[string]int)}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, githubForge{base: server.URL, client: server.Client()}
}

func (f *feedbackTestForge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests[r.URL.RequestURI()]++
	if r.URL.RequestURI() == f.failRequest {
		http.Error(w, "temporary forge failure", http.StatusServiceUnavailable)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	var body any
	switch path := r.URL.Path; {
	case path == feedbackTestPullPath:
		body = map[string]any{"state": "open", "head": map[string]string{"sha": f.head}, "user": map[string]string{"login": "pr-author"}}
	case path == feedbackTestPullPath+"/reviews":
		body = feedbackTestPage(f.reviews, page)
		if f.headAfterList != "" {
			f.head, f.headAfterList = f.headAfterList, ""
		}
	case strings.HasPrefix(path, feedbackTestPullPath+"/reviews/") && strings.HasSuffix(path, "/comments"):
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, feedbackTestPullPath+"/reviews/"), "/comments"), 10, 64)
		body = feedbackTestPage(f.inline[id], page)
	case strings.HasSuffix(path, "/status"):
		body = map[string]int{"total_count": 0}
	case strings.HasSuffix(path, "/check-runs"):
		body = map[string]int{"total_count": 0}
	case path == "/user":
		body = map[string]string{"login": "hub-poster"}
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func feedbackTestPage[T any](rows []T, page int) []T {
	start := min((max(page, 1)-1)*100, len(rows))
	return append([]T{}, rows[start:min(start+100, len(rows))]...)
}

func feedbackTestReview(id int64, at time.Time, body string) githubPRReview {
	r := githubPRReview{ID: id, State: "COMMENTED", SubmittedAt: at, CommitID: "head-one", Body: body, AuthorAssociation: "COLLABORATOR"}
	r.User.Login, r.User.Type = "reviewer", "User"
	return r
}

func (f *feedbackTestForge) calls(uri string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[uri]
}
