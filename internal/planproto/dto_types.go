package planproto

// The v1 wire structs deliberately mirror closed domain values instead of
// serializing domain structs directly. Their fields are explicit, stable, and
// carry both CBOR and JSON names. No DTO includes an extension map.

type planWire struct {
	SchemaVersion uint16                 `cbor:"schema_version" json:"schema_version"`
	Command       string                 `cbor:"command" json:"command"`
	Caller        callerWire             `cbor:"caller" json:"caller"`
	Capabilities  capabilitySnapshotWire `cbor:"capabilities" json:"capabilities"`
	ConfigDigest  []byte                 `cbor:"config_digest" json:"config_digest"`
	Actions       []actionWire           `cbor:"actions" json:"actions"`
	Totals        sizeFactsWire          `cbor:"totals" json:"totals"`
	Creation      creationWire           `cbor:"creation" json:"creation"`
	Digest        []byte                 `cbor:"digest" json:"digest"`
}

type planBodyWire struct {
	SchemaVersion uint16                 `cbor:"schema_version" json:"schema_version"`
	Command       string                 `cbor:"command" json:"command"`
	Caller        callerWire             `cbor:"caller" json:"caller"`
	Capabilities  capabilitySnapshotWire `cbor:"capabilities" json:"capabilities"`
	ConfigDigest  []byte                 `cbor:"config_digest" json:"config_digest"`
	Actions       []actionWire           `cbor:"actions" json:"actions"`
	Totals        sizeFactsWire          `cbor:"totals" json:"totals"`
	Creation      creationWire           `cbor:"creation" json:"creation"`
}

type resultWire struct {
	SchemaVersion uint16             `cbor:"schema_version" json:"schema_version"`
	PlanDigest    []byte             `cbor:"plan_digest" json:"plan_digest"`
	RunID         string             `cbor:"run_id" json:"run_id"`
	Actions       []actionResultWire `cbor:"actions" json:"actions"`
	Summary       exitSummaryWire    `cbor:"summary" json:"summary"`
}

type callerWire struct {
	RunID                string   `cbor:"run_id" json:"run_id"`
	UID                  uint32   `cbor:"uid" json:"uid"`
	Interactive          bool     `cbor:"interactive" json:"interactive"`
	SelectedCandidateIDs []string `cbor:"selected_candidate_ids" json:"selected_candidate_ids"`
}

type creationWire struct {
	CreatedAtUnixNano int64  `cbor:"created_at_unix_nano" json:"created_at_unix_nano"`
	ToolVersion       string `cbor:"tool_version" json:"tool_version"`
	HostProfile       string `cbor:"host_profile" json:"host_profile"`
}

type capabilitySnapshotWire struct {
	Facts []capabilityFactWire `cbor:"facts" json:"facts"`
}

type capabilityFactWire struct {
	ID      string `cbor:"id" json:"id"`
	State   string `cbor:"state" json:"state"`
	Version string `cbor:"version" json:"version"`
}

type targetWire struct {
	Kind          string                   `cbor:"kind" json:"kind"`
	Filesystem    *filesystemTargetWire    `cbor:"filesystem" json:"filesystem"`
	ManagerObject *managerObjectTargetWire `cbor:"manager_object" json:"manager_object"`
}

type filesystemTargetWire struct {
	Root string   `cbor:"root" json:"root"`
	Path pathWire `cbor:"path" json:"path"`
}

type managerObjectTargetWire struct {
	Provider string `cbor:"provider" json:"provider"`
	Object   string `cbor:"object" json:"object"`
	Scope    string `cbor:"scope" json:"scope"`
}

type pathWire struct {
	Display    string              `cbor:"display" json:"display"`
	Components []pathComponentWire `cbor:"components" json:"components"`
}

type pathComponentWire struct {
	UTF8   *string `cbor:"utf8,omitempty" json:"utf8,omitempty"`
	Base64 []byte  `cbor:"base64,omitempty" json:"base64,omitempty"`
}

type byteQuantityWire struct {
	Available bool   `cbor:"available" json:"available"`
	Bytes     uint64 `cbor:"bytes" json:"bytes"`
}

type sizeEffectWire struct {
	Apparent  byteQuantityWire `cbor:"apparent" json:"apparent"`
	Allocated byteQuantityWire `cbor:"allocated" json:"allocated"`
}

type uint64FactWire struct {
	Known bool   `cbor:"known" json:"known"`
	Value uint64 `cbor:"value" json:"value"`
}

type uint32FactWire struct {
	Known bool   `cbor:"known" json:"known"`
	Value uint32 `cbor:"value" json:"value"`
}

type int64FactWire struct {
	Known bool  `cbor:"known" json:"known"`
	Value int64 `cbor:"value" json:"value"`
}

type sizeFactsWire struct {
	Apparent  byteQuantityWire `cbor:"apparent" json:"apparent"`
	Allocated byteQuantityWire `cbor:"allocated" json:"allocated"`
	Estimated sizeEffectWire   `cbor:"estimated" json:"estimated"`
	Verified  sizeEffectWire   `cbor:"verified" json:"verified"`
	LinkCount uint64FactWire   `cbor:"link_count" json:"link_count"`
	Aggregate bool             `cbor:"aggregate" json:"aggregate"`
}

type filesystemSnapshotWire struct {
	DeviceMajor uint32FactWire `cbor:"device_major" json:"device_major"`
	DeviceMinor uint32FactWire `cbor:"device_minor" json:"device_minor"`
	Inode       uint64FactWire `cbor:"inode" json:"inode"`
	Type        string         `cbor:"type" json:"type"`
	UID         uint32FactWire `cbor:"uid" json:"uid"`
	GID         uint32FactWire `cbor:"gid" json:"gid"`
	Mode        uint32FactWire `cbor:"mode" json:"mode"`
	LinkCount   uint64FactWire `cbor:"link_count" json:"link_count"`
	Size        uint64FactWire `cbor:"size" json:"size"`
	ModifiedAt  int64FactWire  `cbor:"modified_at" json:"modified_at"`
	ChangedAt   int64FactWire  `cbor:"changed_at" json:"changed_at"`
	MountID     uint64FactWire `cbor:"mount_id" json:"mount_id"`
}

type providerGuaranteeWire struct {
	Kind           string                 `cbor:"kind" json:"kind"`
	CoveredObjects []managerObjectRefWire `cbor:"covered_objects" json:"covered_objects"`
}

// managerObjectRefWire preserves a graph-local identity. A manager object ID
// alone is ambiguous because the same object can exist in more than one
// manager scope.
type managerObjectRefWire struct {
	ID    string `cbor:"id" json:"id"`
	Scope string `cbor:"scope" json:"scope"`
}

type transactionNodeWire struct {
	ID                string `cbor:"id" json:"id"`
	Version           string `cbor:"version" json:"version"`
	Scope             string `cbor:"scope" json:"scope"`
	Protected         bool   `cbor:"protected" json:"protected"`
	Essential         bool   `cbor:"essential" json:"essential"`
	MaintainerScripts bool   `cbor:"maintainer_scripts" json:"maintainer_scripts"`
	RestartRequired   bool   `cbor:"restart_required" json:"restart_required"`
	NetworkRequired   bool   `cbor:"network_required" json:"network_required"`
}

type transactionEdgeWire struct {
	From   managerObjectRefWire `cbor:"from" json:"from"`
	To     managerObjectRefWire `cbor:"to" json:"to"`
	Reason string               `cbor:"reason" json:"reason"`
}

type transactionGraphWire struct {
	Provider               string                `cbor:"provider" json:"provider"`
	Nodes                  []transactionNodeWire `cbor:"nodes" json:"nodes"`
	Edges                  []transactionEdgeWire `cbor:"edges" json:"edges"`
	ProviderEvidenceDigest []byte                `cbor:"provider_evidence_digest" json:"provider_evidence_digest"`
	Guarantee              providerGuaranteeWire `cbor:"guarantee" json:"guarantee"`
}

type evidenceWire struct {
	Kind               string                          `cbor:"kind" json:"kind"`
	Filesystem         *filesystemEvidenceWire         `cbor:"filesystem" json:"filesystem"`
	PackageTransaction *packageTransactionEvidenceWire `cbor:"package_transaction" json:"package_transaction"`
	ManagerObject      *managerObjectEvidenceWire      `cbor:"manager_object" json:"manager_object"`
	Capability         *capabilityEvidenceWire         `cbor:"capability" json:"capability"`
	ProjectMarker      *projectMarkerEvidenceWire      `cbor:"project_marker" json:"project_marker"`
	InstallerMetadata  *installerMetadataEvidenceWire  `cbor:"installer_metadata" json:"installer_metadata"`
	ProfileManager     *profileManagerEvidenceWire     `cbor:"profile_manager" json:"profile_manager"`
}

type filesystemEvidenceWire struct {
	Target   targetWire             `cbor:"target" json:"target"`
	Snapshot filesystemSnapshotWire `cbor:"snapshot" json:"snapshot"`
}

type packageTransactionEvidenceWire struct {
	Graph transactionGraphWire `cbor:"graph" json:"graph"`
}

type managerObjectEvidenceWire struct {
	Target  targetWire `cbor:"target" json:"target"`
	Version string     `cbor:"version" json:"version"`
	Present bool       `cbor:"present" json:"present"`
}

type capabilityEvidenceWire struct {
	Capability string `cbor:"capability" json:"capability"`
	State      string `cbor:"state" json:"state"`
	Version    string `cbor:"version" json:"version"`
}

type projectMarkerEvidenceWire struct {
	Root      string   `cbor:"root" json:"root"`
	Marker    pathWire `cbor:"marker" json:"marker"`
	Ecosystem string   `cbor:"ecosystem" json:"ecosystem"`
}

type installerMetadataEvidenceWire struct {
	Target targetWire `cbor:"target" json:"target"`
	Format string     `cbor:"format" json:"format"`
	Digest []byte     `cbor:"digest" json:"digest"`
}

type profileManagerEvidenceWire struct {
	Provider string `cbor:"provider" json:"provider"`
	Manager  string `cbor:"manager" json:"manager"`
	Version  string `cbor:"version" json:"version"`
}

type preconditionWire struct {
	Kind               string                              `cbor:"kind" json:"kind"`
	Filesystem         *filesystemPreconditionWire         `cbor:"filesystem" json:"filesystem"`
	PackageTransaction *packageTransactionPreconditionWire `cbor:"package_transaction" json:"package_transaction"`
	ManagerObject      *managerObjectPreconditionWire      `cbor:"manager_object" json:"manager_object"`
	Capability         *capabilityPreconditionWire         `cbor:"capability" json:"capability"`
	ProjectMarker      *projectMarkerPreconditionWire      `cbor:"project_marker" json:"project_marker"`
	InstallerMetadata  *installerMetadataPreconditionWire  `cbor:"installer_metadata" json:"installer_metadata"`
	ProfileManager     *profileManagerPreconditionWire     `cbor:"profile_manager" json:"profile_manager"`
}

type filesystemPreconditionWire struct {
	Target   targetWire             `cbor:"target" json:"target"`
	Required uint32                 `cbor:"required" json:"required"`
	Snapshot filesystemSnapshotWire `cbor:"snapshot" json:"snapshot"`
}

type packageTransactionPreconditionWire struct {
	Graph              transactionGraphWire `cbor:"graph" json:"graph"`
	ManagerStateDigest []byte               `cbor:"manager_state_digest" json:"manager_state_digest"`
}

type managerObjectPreconditionWire struct {
	Target          targetWire `cbor:"target" json:"target"`
	ExpectedVersion string     `cbor:"expected_version" json:"expected_version"`
	ExpectedPresent bool       `cbor:"expected_present" json:"expected_present"`
}

type capabilityPreconditionWire struct {
	Capability string `cbor:"capability" json:"capability"`
	State      string `cbor:"state" json:"state"`
	Version    string `cbor:"version" json:"version"`
}

type projectMarkerPreconditionWire struct {
	Root      string   `cbor:"root" json:"root"`
	Marker    pathWire `cbor:"marker" json:"marker"`
	Ecosystem string   `cbor:"ecosystem" json:"ecosystem"`
}

type installerMetadataPreconditionWire struct {
	Target targetWire `cbor:"target" json:"target"`
	Format string     `cbor:"format" json:"format"`
	Digest []byte     `cbor:"digest" json:"digest"`
}

type profileManagerPreconditionWire struct {
	Provider string `cbor:"provider" json:"provider"`
	Manager  string `cbor:"manager" json:"manager"`
	Version  string `cbor:"version" json:"version"`
}

type actionWire struct {
	ID                    string                `cbor:"id" json:"id"`
	Kind                  string                `cbor:"kind" json:"kind"`
	Target                targetWire            `cbor:"target" json:"target"`
	Evidence              []evidenceWire        `cbor:"evidence" json:"evidence"`
	Precondition          preconditionWire      `cbor:"precondition" json:"precondition"`
	Dependencies          []string              `cbor:"dependencies" json:"dependencies"`
	Risk                  string                `cbor:"risk" json:"risk"`
	Reversibility         string                `cbor:"reversibility" json:"reversibility"`
	EstimatedEffect       sizeFactsWire         `cbor:"estimated_effect" json:"estimated_effect"`
	RequiredCapability    string                `cbor:"required_capability" json:"required_capability"`
	ProviderGuarantee     providerGuaranteeWire `cbor:"provider_guarantee" json:"provider_guarantee"`
	ExpectedPostcondition string                `cbor:"expected_postcondition" json:"expected_postcondition"`
}

type actionResultWire struct {
	ActionID            string                   `cbor:"action_id" json:"action_id"`
	Kind                string                   `cbor:"kind" json:"kind"`
	Outcome             string                   `cbor:"outcome" json:"outcome"`
	Attempted           bool                     `cbor:"attempted" json:"attempted"`
	Reconciliation      string                   `cbor:"reconciliation" json:"reconciliation"`
	ReconciliationProbe *reconciliationProbeWire `cbor:"reconciliation_probe" json:"reconciliation_probe"`
	Recovery            string                   `cbor:"recovery" json:"recovery"`
	RecoveryHandle      *recoveryHandleWire      `cbor:"recovery_handle" json:"recovery_handle"`
	VerifiedEffect      sizeFactsWire            `cbor:"verified_effect" json:"verified_effect"`
}

type reconciliationProbeWire struct {
	Kind        string                `cbor:"kind" json:"kind"`
	Target      *targetWire           `cbor:"target" json:"target"`
	Graph       *transactionGraphWire `cbor:"graph" json:"graph"`
	StateRecord *stateRecordProbeWire `cbor:"state_record" json:"state_record"`
}

type stateRecordProbeWire struct {
	RunID    string `cbor:"run_id" json:"run_id"`
	ActionID string `cbor:"action_id" json:"action_id"`
}

type recoveryHandleWire struct {
	Kind         string   `cbor:"kind" json:"kind"`
	Root         string   `cbor:"root" json:"root"`
	Token        string   `cbor:"token" json:"token"`
	OriginalPath pathWire `cbor:"original_path" json:"original_path"`
	RecordedAtNS int64    `cbor:"recorded_at_unix_nano" json:"recorded_at_unix_nano"`
}

type exitSummaryWire struct {
	Total                  uint32 `cbor:"total" json:"total"`
	SuccessCount           uint32 `cbor:"success_count" json:"success_count"`
	SkippedCount           uint32 `cbor:"skipped_count" json:"skipped_count"`
	DriftedCount           uint32 `cbor:"drifted_count" json:"drifted_count"`
	DeniedCount            uint32 `cbor:"denied_count" json:"denied_count"`
	UnsupportedCount       uint32 `cbor:"unsupported_count" json:"unsupported_count"`
	FailedCount            uint32 `cbor:"failed_count" json:"failed_count"`
	InterruptedCount       uint32 `cbor:"interrupted_count" json:"interrupted_count"`
	IndeterminateCount     uint32 `cbor:"indeterminate_count" json:"indeterminate_count"`
	Partial                bool   `cbor:"partial" json:"partial"`
	ReconciliationRequired bool   `cbor:"reconciliation_required" json:"reconciliation_required"`
}
