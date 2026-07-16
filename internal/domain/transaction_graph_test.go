package domain

import "testing"

func TestTransactionGraphRejectsDuplicateDanglingAndBroadenedGuarantees(t *testing.T) {
	provider := testProvider(t)
	first := testManagerObjectID(t, "first-package")
	second := testManagerObjectID(t, "second-package")
	base := TransactionGraph{
		Provider: provider,
		Nodes: []TransactionNode{
			{ID: first, Version: "1.0.0", Scope: ManagerScopeSystem},
			{ID: second, Version: "2.0.0", Scope: ManagerScopeSystem},
		},
		Edges:                  []TransactionEdge{{From: ManagerObjectRef{ID: first, Scope: ManagerScopeSystem}, To: ManagerObjectRef{ID: second, Scope: ManagerScopeSystem}, Reason: TransactionEdgeDependency}},
		ProviderEvidenceDigest: testEvidenceDigest(t, 3),
		Guarantee: ProviderGuarantee{
			Kind:           ProviderGuaranteeExactGraphReprobeRequired,
			CoveredObjects: []ManagerObjectRef{{ID: first, Scope: ManagerScopeSystem}, {ID: second, Scope: ManagerScopeSystem}},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("TransactionGraph.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*TransactionGraph)
	}{
		{
			name: "duplicate node",
			mutate: func(graph *TransactionGraph) {
				graph.Nodes = append(graph.Nodes, graph.Nodes[0])
			},
		},
		{
			name: "duplicate edge",
			mutate: func(graph *TransactionGraph) {
				graph.Edges = append(graph.Edges, graph.Edges[0])
			},
		},
		{
			name: "dangling edge",
			mutate: func(graph *TransactionGraph) {
				graph.Edges[0].To = testManagerObjectRef(t, "missing-package", ManagerScopeSystem)
			},
		},
		{
			name: "broadened exact graph guarantee",
			mutate: func(graph *TransactionGraph) {
				graph.Guarantee.CoveredObjects = graph.Guarantee.CoveredObjects[:1]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base.Clone()
			tt.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("TransactionGraph.Validate() error = nil, want error")
			}
		})
	}
}

func TestTransactionGraphCanonicalizesDeclaredUnorderedSets(t *testing.T) {
	provider := testProvider(t)
	first := testManagerObjectID(t, "first-package")
	second := testManagerObjectID(t, "second-package")
	graph := TransactionGraph{
		Provider: provider,
		Nodes: []TransactionNode{
			{ID: second, Version: "2.0.0", Scope: ManagerScopeSystem},
			{ID: first, Version: "1.0.0", Scope: ManagerScopeSystem},
		},
		ProviderEvidenceDigest: testEvidenceDigest(t, 4),
		Guarantee: ProviderGuarantee{
			Kind:           ProviderGuaranteeExactGraphReprobeRequired,
			CoveredObjects: []ManagerObjectRef{{ID: second, Scope: ManagerScopeSystem}, {ID: first, Scope: ManagerScopeSystem}},
		},
	}
	canonical, err := NewTransactionGraph(graph)
	if err != nil {
		t.Fatalf("NewTransactionGraph() error = %v", err)
	}
	if got := canonical.Nodes[0].ID; got != first {
		t.Fatalf("canonical first node = %q, want %q", got, first)
	}
	if got := canonical.Guarantee.CoveredObjects[0]; got.ID != first || got.Scope != ManagerScopeSystem {
		t.Fatalf("canonical first guarantee object = %#v, want %q/%q", got, first, ManagerScopeSystem)
	}
}
