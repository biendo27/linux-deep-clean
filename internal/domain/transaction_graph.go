package domain

import (
	"fmt"
	"sort"
)

// ProviderGuaranteeKind is the closed strength vocabulary carried by provider
// evidence. It describes what must be re-probed, not an execution permission.
type ProviderGuaranteeKind string

const (
	ProviderGuaranteeReadOnlyInventory          ProviderGuaranteeKind = "read_only_inventory"
	ProviderGuaranteeBoundedEffectOnly          ProviderGuaranteeKind = "bounded_effect_only"
	ProviderGuaranteeExactTargetReprobeRequired ProviderGuaranteeKind = "exact_target_reprobe_required"
	ProviderGuaranteeExactGraphReprobeRequired  ProviderGuaranteeKind = "exact_graph_reprobe_required"
)

// ManagerObjectRef is a graph-local manager object identity. Object names are
// not globally unique: user and system scopes can contain the same name, so
// dependency edges and guarantee coverage always retain both fields.
type ManagerObjectRef struct {
	ID    ManagerObjectID
	Scope ManagerScope
}

// NewManagerObjectRef validates a graph-local manager object identity.
func NewManagerObjectRef(id ManagerObjectID, scope ManagerScope) (ManagerObjectRef, error) {
	reference := ManagerObjectRef{ID: id, Scope: scope}
	if err := reference.Validate(); err != nil {
		return ManagerObjectRef{}, err
	}
	return reference, nil
}

func (reference ManagerObjectRef) Validate() error {
	if err := reference.ID.Validate(); err != nil {
		return err
	}
	return reference.Scope.Validate()
}

// ProviderGuarantee binds a claim to exact manager object references whose
// evidence supports it. Exact-graph claims must cover every graph node.
type ProviderGuarantee struct {
	Kind           ProviderGuaranteeKind
	CoveredObjects []ManagerObjectRef
}

// TransactionNode is a typed package-manager object included in a preview.
type TransactionNode struct {
	ID                ManagerObjectID
	Version           string
	Scope             ManagerScope
	Protected         bool
	Essential         bool
	MaintainerScripts bool
	RestartRequired   bool
	NetworkRequired   bool
}

func (node TransactionNode) reference() ManagerObjectRef {
	return ManagerObjectRef{ID: node.ID, Scope: node.Scope}
}

// TransactionEdgeReason preserves why two transaction nodes relate.
type TransactionEdgeReason string

const (
	TransactionEdgeDependency  TransactionEdgeReason = "dependency"
	TransactionEdgeReplacement TransactionEdgeReason = "replacement"
	TransactionEdgeConflict    TransactionEdgeReason = "conflict"
	TransactionEdgeRemoval     TransactionEdgeReason = "removal"
)

// TransactionEdge is scoped at both endpoints, so a graph can represent the
// same package name in user and system installations without ambiguity.
type TransactionEdge struct {
	From   ManagerObjectRef
	To     ManagerObjectRef
	Reason TransactionEdgeReason
}

// TransactionGraph is the only dependency-free graph shared by provider
// preview and future helper re-simulation. Package dependency cycles are
// preserved; only duplicate/dangling graph facts are rejected here.
type TransactionGraph struct {
	Provider               ProviderID
	Nodes                  []TransactionNode
	Edges                  []TransactionEdge
	ProviderEvidenceDigest EvidenceDigest
	Guarantee              ProviderGuarantee
}

func (guarantee ProviderGuarantee) Validate() error {
	switch guarantee.Kind {
	case ProviderGuaranteeReadOnlyInventory:
		if len(guarantee.CoveredObjects) != 0 {
			return fmt.Errorf("read-only inventory guarantee must not claim covered objects")
		}
	case ProviderGuaranteeBoundedEffectOnly, ProviderGuaranteeExactGraphReprobeRequired:
		if len(guarantee.CoveredObjects) == 0 {
			return fmt.Errorf("provider guarantee %q requires covered objects", guarantee.Kind)
		}
	case ProviderGuaranteeExactTargetReprobeRequired:
		if len(guarantee.CoveredObjects) != 1 {
			return fmt.Errorf("exact-target provider guarantee must cover exactly one object")
		}
	default:
		return fmt.Errorf("unknown provider guarantee %q", guarantee.Kind)
	}
	for index, object := range guarantee.CoveredObjects {
		if err := object.Validate(); err != nil {
			return fmt.Errorf("provider guarantee object %d: %w", index, err)
		}
		if index > 0 && compareManagerObjectRef(guarantee.CoveredObjects[index-1], object) >= 0 {
			return fmt.Errorf("provider guarantee covered objects must be strictly sorted")
		}
	}
	return nil
}

func (node TransactionNode) Validate() error {
	if err := node.reference().Validate(); err != nil {
		return err
	}
	if node.Version == "" {
		return fmt.Errorf("transaction node %q requires an exact version", node.ID)
	}
	return nil
}

func (reason TransactionEdgeReason) Validate() error {
	switch reason {
	case TransactionEdgeDependency, TransactionEdgeReplacement, TransactionEdgeConflict, TransactionEdgeRemoval:
		return nil
	default:
		return fmt.Errorf("unknown transaction edge reason %q", reason)
	}
}

func NewTransactionGraph(graph TransactionGraph) (TransactionGraph, error) {
	cloned := graph.Clone()
	sort.Slice(cloned.Nodes, func(left, right int) bool {
		return compareManagerObjectRef(cloned.Nodes[left].reference(), cloned.Nodes[right].reference()) < 0
	})
	sort.Slice(cloned.Edges, func(left, right int) bool {
		return compareTransactionEdge(cloned.Edges[left], cloned.Edges[right]) < 0
	})
	sort.Slice(cloned.Guarantee.CoveredObjects, func(left, right int) bool {
		return compareManagerObjectRef(cloned.Guarantee.CoveredObjects[left], cloned.Guarantee.CoveredObjects[right]) < 0
	})
	if err := cloned.Validate(); err != nil {
		return TransactionGraph{}, err
	}
	return cloned, nil
}

func (graph TransactionGraph) Validate() error {
	if err := graph.Provider.Validate(); err != nil {
		return err
	}
	if len(graph.Nodes) == 0 {
		return fmt.Errorf("transaction graph requires at least one node")
	}
	if err := graph.ProviderEvidenceDigest.Validate(); err != nil {
		return err
	}
	for index, node := range graph.Nodes {
		if err := node.Validate(); err != nil {
			return fmt.Errorf("transaction node %d: %w", index, err)
		}
		if index > 0 && compareManagerObjectRef(graph.Nodes[index-1].reference(), node.reference()) >= 0 {
			return fmt.Errorf("transaction graph nodes must be strictly sorted by ID and scope")
		}
	}
	for index, edge := range graph.Edges {
		if err := edge.From.Validate(); err != nil {
			return fmt.Errorf("transaction edge %d source: %w", index, err)
		}
		if err := edge.To.Validate(); err != nil {
			return fmt.Errorf("transaction edge %d destination: %w", index, err)
		}
		if edge.From == edge.To {
			return fmt.Errorf("transaction edge %d is self-referential", index)
		}
		if err := edge.Reason.Validate(); err != nil {
			return fmt.Errorf("transaction edge %d: %w", index, err)
		}
		if !graph.hasNode(edge.From) || !graph.hasNode(edge.To) {
			return fmt.Errorf("transaction edge %d has a dangling node", index)
		}
		if index > 0 && compareTransactionEdge(graph.Edges[index-1], edge) >= 0 {
			return fmt.Errorf("transaction graph edges must be strictly sorted and unique")
		}
	}
	if err := graph.Guarantee.Validate(); err != nil {
		return err
	}
	for _, object := range graph.Guarantee.CoveredObjects {
		if !graph.hasNode(object) {
			return fmt.Errorf("provider guarantee covers object %q/%q absent from transaction graph", object.Scope, object.ID)
		}
	}
	if graph.Guarantee.Kind == ProviderGuaranteeExactGraphReprobeRequired && !sameNodeSet(graph.Nodes, graph.Guarantee.CoveredObjects) {
		return fmt.Errorf("exact graph guarantee is broader than its covered evidence")
	}
	return nil
}

func (graph TransactionGraph) Clone() TransactionGraph {
	cloned := graph
	cloned.Nodes = append([]TransactionNode(nil), graph.Nodes...)
	cloned.Edges = append([]TransactionEdge(nil), graph.Edges...)
	cloned.Guarantee.CoveredObjects = append([]ManagerObjectRef(nil), graph.Guarantee.CoveredObjects...)
	return cloned
}

func (graph TransactionGraph) hasNode(reference ManagerObjectRef) bool {
	index := sort.Search(len(graph.Nodes), func(index int) bool {
		return compareManagerObjectRef(graph.Nodes[index].reference(), reference) >= 0
	})
	return index < len(graph.Nodes) && graph.Nodes[index].reference() == reference
}

func compareTransactionEdge(left, right TransactionEdge) int {
	if comparison := compareManagerObjectRef(left.From, right.From); comparison != 0 {
		return comparison
	}
	if comparison := compareManagerObjectRef(left.To, right.To); comparison != 0 {
		return comparison
	}
	if left.Reason < right.Reason {
		return -1
	}
	if left.Reason > right.Reason {
		return 1
	}
	return 0
}

func sameNodeSet(nodes []TransactionNode, covered []ManagerObjectRef) bool {
	if len(nodes) != len(covered) {
		return false
	}
	for index := range nodes {
		if nodes[index].reference() != covered[index] {
			return false
		}
	}
	return true
}

func compareManagerObjectRef(left, right ManagerObjectRef) int {
	if left.ID < right.ID {
		return -1
	}
	if left.ID > right.ID {
		return 1
	}
	if left.Scope < right.Scope {
		return -1
	}
	if left.Scope > right.Scope {
		return 1
	}
	return 0
}
