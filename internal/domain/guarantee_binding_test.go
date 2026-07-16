package domain

import "testing"

func TestManagerGuaranteesRequireMatchingTransactionEvidence(t *testing.T) {
	for _, guarantee := range []ProviderGuarantee{
		{
			Kind:           ProviderGuaranteeBoundedEffectOnly,
			CoveredObjects: []ManagerObjectRef{testManagerObjectRef(t, "example-package", ManagerScopeSystem)},
		},
		{
			Kind:           ProviderGuaranteeExactTargetReprobeRequired,
			CoveredObjects: []ManagerObjectRef{testManagerObjectRef(t, "example-package", ManagerScopeSystem)},
		},
	} {
		action := testManagerAction(t, "manager-guarantee-needs-transaction-evidence")
		action.Evidence = action.Evidence[:1]
		action.ProviderGuarantee = guarantee
		if err := action.Validate(); err == nil {
			t.Fatalf("Action.Validate() accepted %q without matching transaction evidence", guarantee.Kind)
		}
	}
}

func TestExactTargetGuaranteeCoversOnlyOneObject(t *testing.T) {
	graph := testTransactionGraph(t)
	if compareManagerObjectRef(graph.Nodes[0].reference(), graph.Nodes[1].reference()) >= 0 {
		t.Fatal("test fixture must supply sorted manager-object references")
	}
	guarantee := ProviderGuarantee{
		Kind: ProviderGuaranteeExactTargetReprobeRequired,
		CoveredObjects: []ManagerObjectRef{
			graph.Nodes[0].reference(),
			graph.Nodes[1].reference(),
		},
	}

	if err := guarantee.Validate(); err == nil {
		t.Fatal("ProviderGuarantee.Validate() accepted an exact-target guarantee covering two objects")
	}
}
