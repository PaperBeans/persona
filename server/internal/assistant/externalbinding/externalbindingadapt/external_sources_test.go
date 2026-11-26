package externalbindingadapt_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/repositorywire"
)

type ref = assistantwire.ExternalRef
type node = assistantwire.ExternalRefNode

func TestCanonicalExternalRefPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ref
		want []ref
	}{
		{"Lark aliases, case-sensitive ids", ref{
			Provider: " Feishu ", Container: node{Kind: "CHAT", ID: " oc_Chat "}, Item: node{Kind: "thread", ID: "omt_One"},
		}, []ref{
			{Provider: "lark", Container: node{Kind: "chat", ID: "oc_Chat"}, Item: node{Kind: "thread", ID: "omt_One"}},
			{Provider: "lark", Container: node{Kind: "chat", ID: "oc_Chat"}},
			{Provider: "lark"},
		}},
		{"Public GitHub host aliases", ref{
			Provider: "GitHub", Channel: "http://WWW.GITHUB.COM:80/", Container: node{Kind: "Repository", ID: "Owner/Repo.GIT"},
			Item: node{Kind: "Pull_Request", ID: "123"},
		}, []ref{
			{Provider: "github", Container: node{Kind: "repository", ID: "owner/repo"}, Item: node{Kind: "pull_request", ID: "123"}},
			{Provider: "github", Container: node{Kind: "repository", ID: "owner/repo"}},
			{Provider: "github"},
		}},
		{"Account-wide email thread", ref{
			Provider: "Email", Channel: "account-01", Item: node{Kind: "thread", ID: "thread/AbC"},
		}, []ref{
			{Provider: "mail", Channel: "account-01", Item: node{Kind: "thread", ID: "thread/AbC"}},
			{Provider: "mail", Channel: "account-01"},
		}},
		{"Mail label", ref{
			Provider: "mail", Channel: "account-02", Container: node{Kind: "label", ID: "Work/Inbox"},
			Item: node{Kind: "thread", ID: "thread/AbC"},
		}, []ref{
			{Provider: "mail", Channel: "account-02", Container: node{Kind: "label", ID: "Work/Inbox"}, Item: node{Kind: "thread", ID: "thread/AbC"}},
			{Provider: "mail", Channel: "account-02", Container: node{Kind: "label", ID: "Work/Inbox"}},
			{Provider: "mail", Channel: "account-02"},
		}},
		{"Generic source", ref{
			Provider: "Vendor.RSS", Container: node{Kind: "source", ID: "feed-12"}, Item: node{Kind: "item", ID: "entry-7"},
		}, []ref{
			{Provider: "vendor.rss", Container: node{Kind: "source", ID: "feed-12"}, Item: node{Kind: "item", ID: "entry-7"}},
			{Provider: "vendor.rss", Container: node{Kind: "source", ID: "feed-12"}},
			{Provider: "vendor.rss"},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := externalbindingadapt.Path(tc.in)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("path = %+v, %v; want %+v", got, err, tc.want)
			}
			for i, scope := range got {
				again, err := externalbindingadapt.Path(scope)
				if err != nil || !slices.Equal(again, got[i:]) {
					t.Fatalf("canonical path is not idempotent: %+v, %v", again, err)
				}
			}
		})
	}
}

func TestExternalRefRejectsInvalidPaths(t *testing.T) {
	for _, invalid := range []ref{
		{Provider: ".unknown"},
		{Provider: "mail"},
		{Provider: "lark", Container: node{Kind: "mailbox", ID: "x"}},
		{Provider: "lark", Item: node{Kind: "thread", ID: "x"}},
		{Provider: "vendor", Container: node{Kind: "source"}},
		{Provider: "vendor", Container: node{Kind: "source", ID: "line\nbreak"}},
		{Provider: "vendor", Container: node{Kind: "source", ID: assistantwire.ExternalRefID(strings.Repeat("x", 513))}},
		{Provider: "github", Channel: "https://user@example.com"},
		{Provider: "github", Channel: "https://example.com/extra"},
		{Provider: "github", Channel: "https://example.com?"},
		{Provider: "github", Container: node{Kind: "repository", ID: "owner/%2Frepo"}},
		{Provider: "github", Container: node{Kind: "repository", ID: "owner/repo#fragment"}},
		{Provider: "github", Container: node{Kind: "repository", ID: "owner/repo"}, Item: node{Kind: "issue", ID: "01"}},
	} {
		if path, err := externalbindingadapt.Path(invalid); err == nil {
			t.Errorf("accepted %+v as %+v", invalid, path)
		}
	}
}

func TestGitHubExternalRefMatchesRepositoryLinkIdentity(t *testing.T) {
	for _, remote := range []string{"https://github.com/Mixed/Case", "http://git.example.com:8443/Mixed/Case"} {
		identity, err := repositorywire.Parse(remote, "github")
		if err != nil {
			t.Fatal(err)
		}
		link, err := repositorywire.NormalizeLink(identity, repositorywire.LinkInput{Kind: "issue", Number: "123"})
		if err != nil {
			t.Fatal(err)
		}
		projected, err := externalbindingadapt.RepositoryLinkRef(identity, link)
		if err != nil {
			t.Fatal(err)
		}
		fromInput, err := externalbindingadapt.Path(ref{
			Provider: "github", Channel: assistantwire.ExternalChannelID(identity.InstanceURL),
			Container: node{Kind: "repository", ID: "Mixed/Case.git"}, Item: node{Kind: "issue", ID: "123"},
		})
		if err != nil || projected != fromInput[0] {
			t.Fatalf("projected %+v, input %+v (%v)", projected, fromInput, err)
		}
	}
}

func TestPolicyResolutionAndFieldProvenance(t *testing.T) {
	var (
		channel   = ref{Provider: "lark"}
		container = ref{Provider: "lark", Container: node{Kind: "chat", ID: "group"}}
		item      = ref{Provider: "lark", Container: container.Container, Item: node{Kind: "thread", ID: "topic"}}
		digest    = assistantwire.ExternalForwardMode("digest")
		off       = assistantwire.ExternalForwardMode("off")
		owner     = assistantwire.ExternalActionRole("owner")
		automatic = assistantwire.ExternalNewItemPolicy("auto_todo")
		interval  = 120
	)
	stored := []assistantwire.ExternalScopedPolicy{
		{ExternalRef: item, Policy: assistantwire.ExternalPolicy{ActionRole: &owner, PreFilter: &assistantwire.ExternalPreFilter{}}},
		{ExternalRef: channel, Policy: assistantwire.ExternalPolicy{Mode: &digest, DigestIntervalMinutes: &interval}},
		{ExternalRef: container, Policy: assistantwire.ExternalPolicy{NewItemPolicy: &automatic, PreFilter: &assistantwire.ExternalPreFilter{Keywords: []string{"Owner"}}}},
	}
	resolved, err := externalbindingadapt.Resolve(item, stored, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := assistantwire.ExternalEffectivePolicy{
		ExternalRef: item,
		Policy: assistantwire.ExternalPolicy{
			Mode: &digest, DigestIntervalMinutes: &interval, ActionRole: &owner, NewItemPolicy: &automatic, PreFilter: &assistantwire.ExternalPreFilter{},
		},
		Provenance: assistantwire.ExternalPolicyProvenance{
			Mode:                  assistantwire.ExternalPolicyOrigin{Source: "channel", ExternalRef: &channel},
			DigestIntervalMinutes: &assistantwire.ExternalPolicyOrigin{Source: "channel", ExternalRef: &channel},
			ActionRole:            assistantwire.ExternalPolicyOrigin{Source: "item", ExternalRef: &item},
			NewItemPolicy:         assistantwire.ExternalPolicyOrigin{Source: "container", ExternalRef: &container},
			PreFilter:             assistantwire.ExternalPolicyOrigin{Source: "item", ExternalRef: &item},
		},
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Errorf("effective policy = %+v, want %+v", resolved, want)
	}

	binding := assistantwire.ExternalBinding{ID: "binding", ExternalBindingInput: assistantwire.ExternalBindingInput{
		ExternalRef: container, TargetKind: "todo", TargetID: "todo_1", Policy: assistantwire.ExternalPolicy{Mode: &off, ActionRole: &owner},
	}}
	resolved, err = externalbindingadapt.Resolve(item, stored, &binding)
	if err != nil {
		t.Fatal(err)
	}
	if *resolved.Policy.Mode != "off" || resolved.Policy.DigestIntervalMinutes != nil || resolved.Provenance.DigestIntervalMinutes != nil ||
		!reflect.DeepEqual(resolved.Provenance.Mode, assistantwire.ExternalPolicyOrigin{Source: "binding", ExternalRef: &container, BindingID: binding.ID}) ||
		resolved.Provenance.ActionRole.Source != "item" {
		t.Errorf("binding policy = %+v", resolved)
	}
	binding.ExternalRef = ref{Provider: "mail", Channel: "account"}
	if _, err := externalbindingadapt.Resolve(item, stored, &binding); err == nil {
		t.Fatal("an unrelated binding was used to resolve the item")
	}
}

func TestExternalPolicyDefaultsAndValidation(t *testing.T) {
	resolved, err := externalbindingadapt.Resolve(ref{Provider: "vendor"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := resolved.Policy
	if *policy.Mode != "off" || *policy.ActionRole != "fyi" || *policy.NewItemPolicy != "ask_owner" || policy.DigestIntervalMinutes != nil ||
		!reflect.DeepEqual(policy.PreFilter, &assistantwire.ExternalPreFilter{}) ||
		resolved.Provenance.Mode.Source != "default" || resolved.Provenance.ActionRole.Source != "default" ||
		resolved.Provenance.NewItemPolicy.Source != "default" || resolved.Provenance.PreFilter.Source != "default" {
		t.Fatalf("unsafe default policy: %+v", resolved)
	}

	off := assistantwire.ExternalForwardMode("off")
	unknown := assistantwire.ExternalForwardMode("instant")
	interval, zero := 15, 0
	for _, invalid := range []assistantwire.ExternalPolicy{
		{Mode: &unknown},
		{DigestIntervalMinutes: &zero},
		{Mode: &off, DigestIntervalMinutes: &interval},
		{PreFilter: &assistantwire.ExternalPreFilter{Keywords: []string{" "}}},
		{PreFilter: &assistantwire.ExternalPreFilter{Senders: []string{"\tinvalid\nvalue"}}},
	} {
		if _, err := externalbindingadapt.Policy(invalid); err == nil {
			t.Errorf("accepted invalid policy %+v", invalid)
		}
	}
	input := assistantwire.ExternalPolicy{PreFilter: &assistantwire.ExternalPreFilter{
		Keywords: []string{" Zed ", "rÉLEASE", "RÉLEASE"}, Mentions: []string{"@B", "@A", "@B"}, Senders: []string{"Sender"},
	}}
	got, err := externalbindingadapt.Policy(input)
	want := &assistantwire.ExternalPreFilter{Keywords: []string{"rélease", "zed"}, Mentions: []string{"@A", "@B"}, Senders: []string{"Sender"}}
	if err != nil || !reflect.DeepEqual(got.PreFilter, want) || input.PreFilter.Keywords[0] != " Zed " {
		t.Fatalf("filter = %+v, %v", got.PreFilter, err)
	}
}
