package externalbindingadapt

import (
	"errors"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/externalbinding"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/repositorywire"
)

// Provider normalizes a provider selector.
func Provider(raw assistantwire.ExternalProvider) (assistantwire.ExternalProvider, error) {
	p, err := unpack(externalbinding.ParseProvider(string(raw)))
	return assistantwire.ExternalProvider(p.Wire()), err
}

// Path normalizes a reference and lists it and its parents in lookup order.
func Path(in assistantwire.ExternalRef) ([]assistantwire.ExternalRef, error) {
	entries, err := path(in)
	if err != nil {
		return nil, err
	}
	refs := make([]assistantwire.ExternalRef, 0, len(entries))
	for _, entry := range entries {
		refs = append(refs, entry.ref)
	}
	return refs, nil
}

func refFromWire(in assistantwire.ExternalRef) (externalbinding.ExternalRef, error) {
	var zero externalbinding.ExternalRef
	provider, err := unpack(externalbinding.ParseProvider(string(in.Provider)))
	if err != nil {
		return zero, err
	}
	if provider.IsGitHub() {
		identity, _, err := githubParts(in)
		if err != nil {
			return zero, err
		}
		in.Channel = assistantwire.ExternalChannelID(identity.InstanceURL)
		if identity.InstanceURL == publicGitHub {
			in.Channel = ""
		}
		if in.Container != (assistantwire.ExternalRefNode{}) {
			in.Container.ID = assistantwire.ExternalRefID(identity.Path)
		}
	}
	channel, err := unpack(externalbinding.ParseChannel(string(in.Channel)))
	if err != nil {
		return zero, err
	}
	container, err := segmentFromWire(in.Container)
	if err != nil {
		return zero, err
	}
	item, err := segmentFromWire(in.Item)
	if err != nil {
		return zero, err
	}
	return unpack(externalbinding.NewExternalRef(provider, channel, container, item))
}

func segmentFromWire(in assistantwire.ExternalRefNode) (opt.Option[externalbinding.Segment], error) {
	none := opt.None[externalbinding.Segment]()
	if in == (assistantwire.ExternalRefNode{}) {
		return none, nil
	}
	segment, err := unpack(externalbinding.ParseSegment(string(in.Kind), string(in.ID)))
	if err != nil {
		return none, err
	}
	return opt.Some(segment), nil
}

type pathEntry struct {
	ref   assistantwire.ExternalRef
	scope externalbinding.Scope
}

type pathReader struct{ entries []pathEntry }

func (p *pathReader) VisitRef(r externalbinding.ExternalRef, scope externalbinding.Scope) {
	p.entries = append(p.entries, pathEntry{ref: refWire(r), scope: scope})
}

func path(in assistantwire.ExternalRef) ([]pathEntry, error) {
	ref, err := refFromWire(in)
	if err != nil {
		return nil, err
	}
	var reader pathReader
	externalbinding.Path(ref).Accept(&reader)
	return reader.entries, nil
}

type refReader struct{ ref assistantwire.ExternalRef }

func (r *refReader) VisitExternalRef(p externalbinding.Provider, c externalbinding.Channel, container, item opt.Option[externalbinding.Segment]) {
	r.ref = assistantwire.ExternalRef{
		Provider: assistantwire.ExternalProvider(p.Wire()), Channel: assistantwire.ExternalChannelID(c.Wire()),
		Container: opt.Fold(container, nodeWire, emptyNode), Item: opt.Fold(item, nodeWire, emptyNode),
	}
}

func refWire(ref externalbinding.ExternalRef) assistantwire.ExternalRef {
	var r refReader
	ref.Accept(&r)
	return r.ref
}

type nodeReader struct{ node assistantwire.ExternalRefNode }

func (r *nodeReader) VisitSegment(kind externalbinding.Kind, id externalbinding.ExternalID) {
	r.node = assistantwire.ExternalRefNode{Kind: assistantwire.ExternalRefKind(kind.Wire()), ID: assistantwire.ExternalRefID(id.Wire())}
}

func nodeWire(segment externalbinding.Segment) assistantwire.ExternalRefNode {
	var r nodeReader
	segment.Accept(&r)
	return r.node
}

func emptyNode() assistantwire.ExternalRefNode { return assistantwire.ExternalRefNode{} }

const publicGitHub = "https://github.com"

// RepositoryLinkRef converts a saved GitHub link using the same identity as a configured reference.
func RepositoryLinkRef(identity repositorywire.Identity, link repositorywire.TaskLink) (assistantwire.ExternalRef, error) {
	if identity.Provider != string(assistantwire.ExternalProviderGitHub) || identity.InstanceURL == "" || identity.Path == "" ||
		link.Kind == "" || link.Number == "" {
		return assistantwire.ExternalRef{}, errors.New("invalid GitHub repository link")
	}
	ref, err := refFromWire(assistantwire.ExternalRef{
		Provider: assistantwire.ExternalProviderGitHub, Channel: assistantwire.ExternalChannelID(identity.InstanceURL),
		Container: assistantwire.ExternalRefNode{Kind: assistantwire.ExternalKindRepository, ID: assistantwire.ExternalRefID(identity.Path)},
		Item:      assistantwire.ExternalRefNode{Kind: assistantwire.ExternalRefKind(link.Kind), ID: assistantwire.ExternalRefID(link.Number)},
	})
	if err != nil {
		return assistantwire.ExternalRef{}, err
	}
	return refWire(ref), nil
}

// RepositoryLinkIdentity gives a normalized GitHub item reference the repository store's identity.
func RepositoryLinkIdentity(ref assistantwire.ExternalRef) (repositorywire.Identity, repositorywire.TaskLink, error) {
	if ref.Provider != assistantwire.ExternalProviderGitHub || ref.Container.Kind != assistantwire.ExternalKindRepository ||
		ref.Item == (assistantwire.ExternalRefNode{}) {
		return repositorywire.Identity{}, repositorywire.TaskLink{}, errors.New("invalid GitHub item reference")
	}
	return githubParts(ref)
}

func githubParts(ref assistantwire.ExternalRef) (repositorywire.Identity, repositorywire.TaskLink, error) {
	var noIdentity repositorywire.Identity
	var noLink repositorywire.TaskLink
	origin := strings.TrimSpace(string(ref.Channel))
	if origin == "" {
		origin = publicGitHub
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(origin, "#") || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return noIdentity, noLink, errors.New("invalid GitHub instance origin")
	}
	repo := strings.TrimSpace(string(ref.Container.ID))
	if ref.Container == (assistantwire.ExternalRefNode{}) {
		// The repository parser owns origin normalization for both repository links and channel defaults.
		repo = "owner/repository"
	}
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, "?#%") {
		return noIdentity, noLink, errors.New("invalid GitHub repository path")
	}
	prefix := "/"
	if strings.EqualFold(u.Hostname(), "api.github.com") {
		prefix = "/repos/"
	}
	identity, err := repositorywire.Parse(strings.TrimSuffix(origin, "/")+prefix+repo, string(assistantwire.ExternalProviderGitHub))
	if err != nil {
		return noIdentity, noLink, errors.New("invalid GitHub repository identity")
	}
	if ref.Item == (assistantwire.ExternalRefNode{}) {
		return identity, noLink, nil
	}
	link, err := repositorywire.NormalizeLink(identity, repositorywire.LinkInput{
		Kind: strings.ToLower(strings.TrimSpace(string(ref.Item.Kind))), Number: strings.TrimSpace(string(ref.Item.ID)),
	})
	if err != nil {
		return noIdentity, noLink, errors.New("invalid GitHub issue or pull request")
	}
	return identity, link, nil
}
