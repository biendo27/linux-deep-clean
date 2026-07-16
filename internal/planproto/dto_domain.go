package planproto

import (
	"fmt"

	"github.com/biendo27/linux-deep-clean/internal/domain"
)

func toProviderGuaranteeWire(guarantee domain.ProviderGuarantee) providerGuaranteeWire {
	covered := make([]managerObjectRefWire, len(guarantee.CoveredObjects))
	for index, object := range guarantee.CoveredObjects {
		covered[index] = toManagerObjectRefWire(object)
	}
	return providerGuaranteeWire{Kind: string(guarantee.Kind), CoveredObjects: covered}
}

func fromProviderGuaranteeWire(wire providerGuaranteeWire) (domain.ProviderGuarantee, error) {
	var covered []domain.ManagerObjectRef
	if len(wire.CoveredObjects) > 0 {
		covered = make([]domain.ManagerObjectRef, len(wire.CoveredObjects))
	}
	for index, value := range wire.CoveredObjects {
		object, err := fromManagerObjectRefWire(value)
		if err != nil {
			return domain.ProviderGuarantee{}, fmt.Errorf("guarantee covered object %d: %w", index, err)
		}
		covered[index] = object
	}
	guarantee := domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeKind(wire.Kind), CoveredObjects: covered}
	if err := guarantee.Validate(); err != nil {
		return domain.ProviderGuarantee{}, err
	}
	return guarantee, nil
}

func toManagerObjectRefWire(reference domain.ManagerObjectRef) managerObjectRefWire {
	return managerObjectRefWire{ID: reference.ID.String(), Scope: string(reference.Scope)}
}

func fromManagerObjectRefWire(wire managerObjectRefWire) (domain.ManagerObjectRef, error) {
	id, err := domain.NewManagerObjectID(wire.ID)
	if err != nil {
		return domain.ManagerObjectRef{}, fmt.Errorf("manager object ID: %w", err)
	}
	reference, err := domain.NewManagerObjectRef(id, domain.ManagerScope(wire.Scope))
	if err != nil {
		return domain.ManagerObjectRef{}, err
	}
	return reference, nil
}

func toTransactionGraphWire(graph domain.TransactionGraph) transactionGraphWire {
	nodes := make([]transactionNodeWire, len(graph.Nodes))
	for index, node := range graph.Nodes {
		nodes[index] = transactionNodeWire{
			ID:                node.ID.String(),
			Version:           node.Version,
			Scope:             string(node.Scope),
			Protected:         node.Protected,
			Essential:         node.Essential,
			MaintainerScripts: node.MaintainerScripts,
			RestartRequired:   node.RestartRequired,
			NetworkRequired:   node.NetworkRequired,
		}
	}
	edges := make([]transactionEdgeWire, len(graph.Edges))
	for index, edge := range graph.Edges {
		edges[index] = transactionEdgeWire{
			From:   toManagerObjectRefWire(edge.From),
			To:     toManagerObjectRefWire(edge.To),
			Reason: string(edge.Reason),
		}
	}
	return transactionGraphWire{
		Provider:               graph.Provider.String(),
		Nodes:                  nodes,
		Edges:                  edges,
		ProviderEvidenceDigest: graph.ProviderEvidenceDigest.Bytes(),
		Guarantee:              toProviderGuaranteeWire(graph.Guarantee),
	}
}

func fromTransactionGraphWire(wire transactionGraphWire) (domain.TransactionGraph, error) {
	provider, err := domain.NewProviderID(wire.Provider)
	if err != nil {
		return domain.TransactionGraph{}, fmt.Errorf("graph provider: %w", err)
	}
	digest, err := domain.NewEvidenceDigest(wire.ProviderEvidenceDigest)
	if err != nil {
		return domain.TransactionGraph{}, fmt.Errorf("graph provider evidence digest: %w", err)
	}
	guarantee, err := fromProviderGuaranteeWire(wire.Guarantee)
	if err != nil {
		return domain.TransactionGraph{}, fmt.Errorf("graph guarantee: %w", err)
	}
	nodes := make([]domain.TransactionNode, len(wire.Nodes))
	for index, node := range wire.Nodes {
		id, err := domain.NewManagerObjectID(node.ID)
		if err != nil {
			return domain.TransactionGraph{}, fmt.Errorf("graph node %d ID: %w", index, err)
		}
		nodes[index] = domain.TransactionNode{
			ID:                id,
			Version:           node.Version,
			Scope:             domain.ManagerScope(node.Scope),
			Protected:         node.Protected,
			Essential:         node.Essential,
			MaintainerScripts: node.MaintainerScripts,
			RestartRequired:   node.RestartRequired,
			NetworkRequired:   node.NetworkRequired,
		}
	}
	edges := make([]domain.TransactionEdge, len(wire.Edges))
	for index, edge := range wire.Edges {
		from, err := fromManagerObjectRefWire(edge.From)
		if err != nil {
			return domain.TransactionGraph{}, fmt.Errorf("graph edge %d source: %w", index, err)
		}
		to, err := fromManagerObjectRefWire(edge.To)
		if err != nil {
			return domain.TransactionGraph{}, fmt.Errorf("graph edge %d target: %w", index, err)
		}
		edges[index] = domain.TransactionEdge{From: from, To: to, Reason: domain.TransactionEdgeReason(edge.Reason)}
	}
	graph := domain.TransactionGraph{
		Provider:               provider,
		Nodes:                  nodes,
		Edges:                  edges,
		ProviderEvidenceDigest: digest,
		Guarantee:              guarantee,
	}
	if err := graph.Validate(); err != nil {
		return domain.TransactionGraph{}, err
	}
	return graph, nil
}

func toEvidenceWire(evidence domain.Evidence) evidenceWire {
	wire := evidenceWire{Kind: string(evidence.Kind)}
	if value := evidence.Filesystem; value != nil {
		wire.Filesystem = &filesystemEvidenceWire{Target: toTargetWire(value.Target), Snapshot: toFilesystemSnapshotWire(value.Snapshot)}
	}
	if value := evidence.PackageTransaction; value != nil {
		wire.PackageTransaction = &packageTransactionEvidenceWire{Graph: toTransactionGraphWire(value.Graph)}
	}
	if value := evidence.ManagerObject; value != nil {
		wire.ManagerObject = &managerObjectEvidenceWire{Target: toTargetWire(value.Target), Version: value.Version, Present: value.Present}
	}
	if value := evidence.Capability; value != nil {
		wire.Capability = &capabilityEvidenceWire{Capability: value.Capability.String(), State: string(value.State), Version: value.Version}
	}
	if value := evidence.ProjectMarker; value != nil {
		wire.ProjectMarker = &projectMarkerEvidenceWire{Root: value.Root.String(), Marker: toPathWire(value.Marker), Ecosystem: value.Ecosystem}
	}
	if value := evidence.InstallerMetadata; value != nil {
		wire.InstallerMetadata = &installerMetadataEvidenceWire{Target: toTargetWire(value.Target), Format: string(value.Format), Digest: value.Digest.Bytes()}
	}
	if value := evidence.ProfileManager; value != nil {
		wire.ProfileManager = &profileManagerEvidenceWire{Provider: value.Provider.String(), Manager: string(value.Manager), Version: value.Version}
	}
	return wire
}

func fromEvidenceWire(wire evidenceWire, limits DecodeLimits) (domain.Evidence, error) {
	if countTrue(
		wire.Filesystem != nil,
		wire.PackageTransaction != nil,
		wire.ManagerObject != nil,
		wire.Capability != nil,
		wire.ProjectMarker != nil,
		wire.InstallerMetadata != nil,
		wire.ProfileManager != nil,
	) != 1 {
		return domain.Evidence{}, fmt.Errorf("evidence must contain exactly one typed variant")
	}
	evidence := domain.Evidence{Kind: domain.EvidenceKind(wire.Kind)}
	switch evidence.Kind {
	case domain.EvidenceFilesystemIdentity:
		if wire.Filesystem == nil {
			return domain.Evidence{}, fmt.Errorf("filesystem evidence has no filesystem variant")
		}
		target, err := fromTargetWire(wire.Filesystem.Target, limits)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("filesystem evidence target: %w", err)
		}
		evidence.Filesystem = &domain.FilesystemEvidence{Target: target, Snapshot: fromFilesystemSnapshotWire(wire.Filesystem.Snapshot)}
	case domain.EvidencePackageTransaction:
		if wire.PackageTransaction == nil {
			return domain.Evidence{}, fmt.Errorf("package transaction evidence has no package variant")
		}
		graph, err := fromTransactionGraphWire(wire.PackageTransaction.Graph)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("package transaction evidence graph: %w", err)
		}
		evidence.PackageTransaction = &domain.PackageTransactionEvidence{Graph: graph}
	case domain.EvidenceManagerObject:
		if wire.ManagerObject == nil {
			return domain.Evidence{}, fmt.Errorf("manager-object evidence has no manager variant")
		}
		target, err := fromTargetWire(wire.ManagerObject.Target, limits)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("manager-object evidence target: %w", err)
		}
		evidence.ManagerObject = &domain.ManagerObjectEvidence{Target: target, Version: wire.ManagerObject.Version, Present: wire.ManagerObject.Present}
	case domain.EvidenceCapability:
		if wire.Capability == nil {
			return domain.Evidence{}, fmt.Errorf("capability evidence has no capability variant")
		}
		capability, err := domain.NewCapabilityID(wire.Capability.Capability)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("capability evidence ID: %w", err)
		}
		evidence.Capability = &domain.CapabilityEvidence{Capability: capability, State: domain.CapabilityState(wire.Capability.State), Version: wire.Capability.Version}
	case domain.EvidenceProjectMarker:
		if wire.ProjectMarker == nil {
			return domain.Evidence{}, fmt.Errorf("project marker evidence has no project-marker variant")
		}
		root, err := domain.NewTrustedRootID(wire.ProjectMarker.Root)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("project marker root: %w", err)
		}
		marker, err := fromPathWire(wire.ProjectMarker.Marker, limits)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("project marker path: %w", err)
		}
		evidence.ProjectMarker = &domain.ProjectMarkerEvidence{Root: root, Marker: marker, Ecosystem: wire.ProjectMarker.Ecosystem}
	case domain.EvidenceInstallerMetadata:
		if wire.InstallerMetadata == nil {
			return domain.Evidence{}, fmt.Errorf("installer metadata evidence has no installer variant")
		}
		target, err := fromTargetWire(wire.InstallerMetadata.Target, limits)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("installer metadata target: %w", err)
		}
		digest, err := domain.NewEvidenceDigest(wire.InstallerMetadata.Digest)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("installer metadata digest: %w", err)
		}
		evidence.InstallerMetadata = &domain.InstallerMetadataEvidence{Target: target, Format: domain.InstallerFormat(wire.InstallerMetadata.Format), Digest: digest}
	case domain.EvidenceProfileManager:
		if wire.ProfileManager == nil {
			return domain.Evidence{}, fmt.Errorf("profile manager evidence has no profile-manager variant")
		}
		provider, err := domain.NewProviderID(wire.ProfileManager.Provider)
		if err != nil {
			return domain.Evidence{}, fmt.Errorf("profile manager provider: %w", err)
		}
		evidence.ProfileManager = &domain.ProfileManagerEvidence{Provider: provider, Manager: domain.ProfileManagerKind(wire.ProfileManager.Manager), Version: wire.ProfileManager.Version}
	default:
		return domain.Evidence{}, fmt.Errorf("unknown evidence kind %q", wire.Kind)
	}
	if err := evidence.Validate(); err != nil {
		return domain.Evidence{}, err
	}
	return evidence, nil
}

func toPreconditionWire(precondition domain.Precondition) preconditionWire {
	wire := preconditionWire{Kind: string(precondition.Kind)}
	if value := precondition.Filesystem; value != nil {
		wire.Filesystem = &filesystemPreconditionWire{Target: toTargetWire(value.Target), Required: uint32(value.Required), Snapshot: toFilesystemSnapshotWire(value.Snapshot)}
	}
	if value := precondition.PackageTransaction; value != nil {
		wire.PackageTransaction = &packageTransactionPreconditionWire{Graph: toTransactionGraphWire(value.Graph), ManagerStateDigest: value.ManagerStateDigest.Bytes()}
	}
	if value := precondition.ManagerObject; value != nil {
		wire.ManagerObject = &managerObjectPreconditionWire{Target: toTargetWire(value.Target), ExpectedVersion: value.ExpectedVersion, ExpectedPresent: value.ExpectedPresent}
	}
	if value := precondition.Capability; value != nil {
		wire.Capability = &capabilityPreconditionWire{Capability: value.Capability.String(), State: string(value.State), Version: value.Version}
	}
	if value := precondition.ProjectMarker; value != nil {
		wire.ProjectMarker = &projectMarkerPreconditionWire{Root: value.Root.String(), Marker: toPathWire(value.Marker), Ecosystem: value.Ecosystem}
	}
	if value := precondition.InstallerMetadata; value != nil {
		wire.InstallerMetadata = &installerMetadataPreconditionWire{Target: toTargetWire(value.Target), Format: string(value.Format), Digest: value.Digest.Bytes()}
	}
	if value := precondition.ProfileManager; value != nil {
		wire.ProfileManager = &profileManagerPreconditionWire{Provider: value.Provider.String(), Manager: string(value.Manager), Version: value.Version}
	}
	return wire
}

func fromPreconditionWire(wire preconditionWire, limits DecodeLimits) (domain.Precondition, error) {
	if countTrue(
		wire.Filesystem != nil,
		wire.PackageTransaction != nil,
		wire.ManagerObject != nil,
		wire.Capability != nil,
		wire.ProjectMarker != nil,
		wire.InstallerMetadata != nil,
		wire.ProfileManager != nil,
	) != 1 {
		return domain.Precondition{}, fmt.Errorf("precondition must contain exactly one typed variant")
	}
	precondition := domain.Precondition{Kind: domain.PreconditionKind(wire.Kind)}
	switch precondition.Kind {
	case domain.PreconditionFilesystemIdentity:
		if wire.Filesystem == nil {
			return domain.Precondition{}, fmt.Errorf("filesystem precondition has no filesystem variant")
		}
		target, err := fromTargetWire(wire.Filesystem.Target, limits)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("filesystem precondition target: %w", err)
		}
		precondition.Filesystem = &domain.FilesystemPrecondition{Target: target, Required: domain.FilesystemFieldMask(wire.Filesystem.Required), Snapshot: fromFilesystemSnapshotWire(wire.Filesystem.Snapshot)}
	case domain.PreconditionPackageTransaction:
		if wire.PackageTransaction == nil {
			return domain.Precondition{}, fmt.Errorf("package transaction precondition has no package variant")
		}
		graph, err := fromTransactionGraphWire(wire.PackageTransaction.Graph)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("package transaction precondition graph: %w", err)
		}
		digest, err := domain.NewEvidenceDigest(wire.PackageTransaction.ManagerStateDigest)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("manager state digest: %w", err)
		}
		precondition.PackageTransaction = &domain.PackageTransactionPrecondition{Graph: graph, ManagerStateDigest: digest}
	case domain.PreconditionManagerObjectState:
		if wire.ManagerObject == nil {
			return domain.Precondition{}, fmt.Errorf("manager-object precondition has no manager variant")
		}
		target, err := fromTargetWire(wire.ManagerObject.Target, limits)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("manager-object precondition target: %w", err)
		}
		precondition.ManagerObject = &domain.ManagerObjectPrecondition{Target: target, ExpectedVersion: wire.ManagerObject.ExpectedVersion, ExpectedPresent: wire.ManagerObject.ExpectedPresent}
	case domain.PreconditionCapability:
		if wire.Capability == nil {
			return domain.Precondition{}, fmt.Errorf("capability precondition has no capability variant")
		}
		capability, err := domain.NewCapabilityID(wire.Capability.Capability)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("capability precondition ID: %w", err)
		}
		precondition.Capability = &domain.CapabilityPrecondition{Capability: capability, State: domain.CapabilityState(wire.Capability.State), Version: wire.Capability.Version}
	case domain.PreconditionProjectMarker:
		if wire.ProjectMarker == nil {
			return domain.Precondition{}, fmt.Errorf("project marker precondition has no marker variant")
		}
		root, err := domain.NewTrustedRootID(wire.ProjectMarker.Root)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("project marker root: %w", err)
		}
		marker, err := fromPathWire(wire.ProjectMarker.Marker, limits)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("project marker path: %w", err)
		}
		precondition.ProjectMarker = &domain.ProjectMarkerPrecondition{Root: root, Marker: marker, Ecosystem: wire.ProjectMarker.Ecosystem}
	case domain.PreconditionInstallerMetadata:
		if wire.InstallerMetadata == nil {
			return domain.Precondition{}, fmt.Errorf("installer metadata precondition has no installer variant")
		}
		target, err := fromTargetWire(wire.InstallerMetadata.Target, limits)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("installer metadata precondition target: %w", err)
		}
		digest, err := domain.NewEvidenceDigest(wire.InstallerMetadata.Digest)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("installer metadata digest: %w", err)
		}
		precondition.InstallerMetadata = &domain.InstallerMetadataPrecondition{Target: target, Format: domain.InstallerFormat(wire.InstallerMetadata.Format), Digest: digest}
	case domain.PreconditionProfileManager:
		if wire.ProfileManager == nil {
			return domain.Precondition{}, fmt.Errorf("profile manager precondition has no profile-manager variant")
		}
		provider, err := domain.NewProviderID(wire.ProfileManager.Provider)
		if err != nil {
			return domain.Precondition{}, fmt.Errorf("profile manager provider: %w", err)
		}
		precondition.ProfileManager = &domain.ProfileManagerPrecondition{Provider: provider, Manager: domain.ProfileManagerKind(wire.ProfileManager.Manager), Version: wire.ProfileManager.Version}
	default:
		return domain.Precondition{}, fmt.Errorf("unknown precondition kind %q", wire.Kind)
	}
	if err := precondition.Validate(); err != nil {
		return domain.Precondition{}, err
	}
	return precondition, nil
}

func toActionWire(action domain.Action) actionWire {
	evidence := make([]evidenceWire, len(action.Evidence))
	for index, value := range action.Evidence {
		evidence[index] = toEvidenceWire(value)
	}
	dependencies := make([]string, len(action.Dependencies))
	for index, value := range action.Dependencies {
		dependencies[index] = value.String()
	}
	return actionWire{
		ID:                    action.ID.String(),
		Kind:                  string(action.Kind),
		Target:                toTargetWire(action.Target),
		Evidence:              canonicalEvidenceWires(evidence),
		Precondition:          toPreconditionWire(action.Precondition),
		Dependencies:          dependencies,
		Risk:                  string(action.Risk),
		Reversibility:         string(action.Reversibility),
		EstimatedEffect:       toSizeFactsWire(action.EstimatedEffect),
		RequiredCapability:    action.RequiredCapability.String(),
		ProviderGuarantee:     toProviderGuaranteeWire(action.ProviderGuarantee),
		ExpectedPostcondition: string(action.ExpectedPostcondition),
	}
}

func fromActionWire(wire actionWire, limits DecodeLimits) (domain.Action, error) {
	id, err := domain.NewActionID(wire.ID)
	if err != nil {
		return domain.Action{}, fmt.Errorf("action ID: %w", err)
	}
	target, err := fromTargetWire(wire.Target, limits)
	if err != nil {
		return domain.Action{}, fmt.Errorf("action target: %w", err)
	}
	evidence := make([]domain.Evidence, len(wire.Evidence))
	for index, value := range wire.Evidence {
		item, err := fromEvidenceWire(value, limits)
		if err != nil {
			return domain.Action{}, fmt.Errorf("action evidence %d: %w", index, err)
		}
		evidence[index] = item
	}
	precondition, err := fromPreconditionWire(wire.Precondition, limits)
	if err != nil {
		return domain.Action{}, fmt.Errorf("action precondition: %w", err)
	}
	dependencies := make([]domain.ActionID, len(wire.Dependencies))
	for index, value := range wire.Dependencies {
		dependency, err := domain.NewActionID(value)
		if err != nil {
			return domain.Action{}, fmt.Errorf("action dependency %d: %w", index, err)
		}
		dependencies[index] = dependency
	}
	requiredCapability, err := domain.NewCapabilityID(wire.RequiredCapability)
	if err != nil {
		return domain.Action{}, fmt.Errorf("action required capability: %w", err)
	}
	guarantee, err := fromProviderGuaranteeWire(wire.ProviderGuarantee)
	if err != nil {
		return domain.Action{}, fmt.Errorf("action provider guarantee: %w", err)
	}
	return domain.NewAction(domain.Action{
		ID:                    id,
		Kind:                  domain.ActionKind(wire.Kind),
		Target:                target,
		Evidence:              evidence,
		Precondition:          precondition,
		Dependencies:          dependencies,
		Risk:                  domain.Risk(wire.Risk),
		Reversibility:         domain.Reversibility(wire.Reversibility),
		EstimatedEffect:       fromSizeFactsWire(wire.EstimatedEffect),
		RequiredCapability:    requiredCapability,
		ProviderGuarantee:     guarantee,
		ExpectedPostcondition: domain.Postcondition(wire.ExpectedPostcondition),
	})
}

func countTrue(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
