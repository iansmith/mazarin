package mancini

import (
	"sort"
	"strings"

	"mazzy/mazarin/attr"
)

// registryEntry holds an interactor and its insertion sequence number.
type registryEntry struct {
	interactor Interactor
	seq        uint64
}

// registry maps constraint-system name → (Interactor, sequence number).
// Sequence number is the count of insertions before this one, giving
// deterministic ordering that matches construction order.
var registry = make(map[string]registryEntry)
var registrySeq uint64

// RegisterInteractor adds an interactor to the global registry keyed by its
// constraint-system name. Called from impl.Interactor.Init().
func RegisterInteractor(name string, i Interactor) {
	if name == "" {
		return
	}
	registry[name] = registryEntry{
		interactor: i,
		seq:        registrySeq,
	}
	registrySeq++
}

// SwapSequence swaps the registration sequence numbers of two named
// interactors, changing their order in [FindChildren] results.
func SwapSequence(a, b string) {
	ea, okA := registry[a]
	eb, okB := registry[b]
	if okA && okB {
		ea.seq, eb.seq = eb.seq, ea.seq
		registry[a] = ea
		registry[b] = eb
	}
}

// LookupInteractor returns the interactor registered under the given
// constraint-system name, or nil if not found.
func LookupInteractor(name string) Interactor {
	e, ok := registry[name]
	if !ok {
		return nil
	}
	return e.interactor
}

// FindChildren discovers children of the named parent via the constraint
// network. It calls attr.Find with the wildcard Parent pattern, reads each
// matching Parent value, filters by parentName, extracts the child's
// constraint-system name from the URI, looks it up in the registry, and
// returns them sorted by registration sequence number.
func FindChildren(parentName string) []Interactor {
	// Find all URIs matching attr:///shepherd/{sid}/str/*/layout/Parent
	uris := attr.Find(ChildPattern())
	if len(uris) == 0 {
		return nil
	}

	type childEntry struct {
		interactor Interactor
		seq        uint64
	}
	var children []childEntry

	for _, uri := range uris {
		// Read the Parent value stored at this URI.
		parentVal, ok := attr.DerefStr(uri)
		if !ok {
			continue
		}
		// Only keep children whose Parent value matches parentName.
		if parentVal != parentName {
			continue
		}

		// Extract the child's constraint-system name from segment 3 of the URI.
		// URI format: attr:///shepherd/{sid}/str/{name}/layout/Parent
		childName := uriSegment(uri, 3)
		if childName == "" {
			continue
		}

		// Look up in the registry.
		entry, ok := registry[childName]
		if !ok {
			continue
		}
		children = append(children, childEntry{
			interactor: entry.interactor,
			seq:        entry.seq,
		})
	}

	if len(children) == 0 {
		return nil
	}

	// Sort by sequence number for deterministic construction-order results.
	sort.Slice(children, func(i, j int) bool {
		return children[i].seq < children[j].seq
	})

	result := make([]Interactor, len(children))
	for i, c := range children {
		result[i] = c.interactor
	}
	return result
}

// uriSegment extracts the nth path segment from an attr URI.
// URI format: "attr:///segment0/segment1/segment2/..."
// Returns "" if the segment index is out of range.
func uriSegment(uri string, n int) string {
	// Strip "attr:///" prefix.
	const prefix = "attr:///"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	rest := uri[len(prefix):]

	seg := 0
	start := 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == '/' {
			if seg == n {
				return rest[start:i]
			}
			seg++
			start = i + 1
		}
	}
	return ""
}
