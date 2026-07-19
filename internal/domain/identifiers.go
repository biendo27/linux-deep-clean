package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// SchemaVersionV1 is the only plan/result schema understood by the Phase 2
	// domain contract. Later schema changes require an explicit reader and
	// compatibility decision.
	SchemaVersionV1 uint16 = 1

	planDigestLength = sha256.Size
)

var (
	planDigestDomain          = []byte("ldclean.plan.digest.v1\x00")
	actionBindingDigestDomain = []byte("ldclean.action-binding.digest.v1\x00")
	trashLayoutBindingDomain  = []byte("ldclean.trash-layout-binding.v1\x00")
)

// ProviderID identifies a compiled provider. It is not a filesystem path or
// executable name.
type ProviderID string

// TrustedRootID identifies a compiled trusted-root registry entry. It never
// contains a caller-supplied absolute path.
type TrustedRootID string

// CandidateID identifies one provider observation.
type CandidateID string

// ActionID identifies one immutable planned action.
type ActionID string

// RunID identifies one local plan/result run.
type RunID string

// CapabilityID identifies a compiled capability fact.
type CapabilityID string

// ManagerObjectID identifies a manager-owned object. It remains a typed data
// value; it is never treated as an executable argument or filesystem path.
type ManagerObjectID string

// Command is the closed product command vocabulary represented in a plan.
type Command string

const (
	CommandMenu        Command = "menu"
	CommandClean       Command = "clean"
	CommandUninstall   Command = "uninstall"
	CommandOptimize    Command = "optimize"
	CommandAnalyze     Command = "analyze"
	CommandStatus      Command = "status"
	CommandHistory     Command = "history"
	CommandPurge       Command = "purge"
	CommandInstaller   Command = "installer"
	CommandFingerprint Command = "fingerprint"
	CommandCompletion  Command = "completion"
	CommandUpdate      Command = "update"
	CommandRemove      Command = "remove"
)

// PlanDigest binds canonical plan-body bytes. It is audit evidence, not a MAC
// and not an authorization token.
type PlanDigest struct {
	value [planDigestLength]byte
}

// ActionBindingDigest binds a canonical action representation to a plan
// digest. It is audit/correlation evidence only: it does not prove that the
// action is a member of a verified full plan and never grants authority.
type ActionBindingDigest struct {
	value [planDigestLength]byte
}

// TrashLayoutBinding identifies the complete authority-selected Trash layout
// evidence used to bind durable metadata reconciliation. It is correlation
// data only: it never contains a path or descriptor and grants no authority.
type TrashLayoutBinding struct {
	value [planDigestLength]byte
}

// ConfigDigest identifies the configuration facts used while constructing a
// plan. It is intentionally distinct from the plan digest.
type ConfigDigest struct {
	value [planDigestLength]byte
}

// EvidenceDigest binds provider evidence used by a transaction graph.
type EvidenceDigest struct {
	value [planDigestLength]byte
}

func NewProviderID(value string) (ProviderID, error) {
	if err := validateStableID("provider ID", value); err != nil {
		return "", err
	}
	return ProviderID(value), nil
}

func NewTrustedRootID(value string) (TrustedRootID, error) {
	if err := validateStableID("trusted root ID", value); err != nil {
		return "", err
	}
	return TrustedRootID(value), nil
}

func NewCandidateID(value string) (CandidateID, error) {
	if err := validateStableID("candidate ID", value); err != nil {
		return "", err
	}
	return CandidateID(value), nil
}

func NewActionID(value string) (ActionID, error) {
	if err := validateStableID("action ID", value); err != nil {
		return "", err
	}
	return ActionID(value), nil
}

func NewRunID(value string) (RunID, error) {
	if err := validateStableID("run ID", value); err != nil {
		return "", err
	}
	return RunID(value), nil
}

func NewCapabilityID(value string) (CapabilityID, error) {
	if err := validateStableID("capability ID", value); err != nil {
		return "", err
	}
	return CapabilityID(value), nil
}

func NewManagerObjectID(value string) (ManagerObjectID, error) {
	if value == "" || len(value) > 256 {
		return "", fmt.Errorf("manager object ID must contain 1 through 256 bytes")
	}
	if value[0] == '/' || value[0] == '-' {
		return "", fmt.Errorf("manager object ID must not begin with %q", value[0])
	}
	if strings.Contains(value, "..") || strings.Contains(value, "//") {
		return "", fmt.Errorf("manager object ID contains a path-like traversal or empty segment")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return "", fmt.Errorf("manager object ID contains a non-printable byte")
		}
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("._:+@/=-", rune(character)):
		default:
			return "", fmt.Errorf("manager object ID contains unsupported byte %q", character)
		}
	}
	return ManagerObjectID(value), nil
}

func (id ProviderID) String() string      { return string(id) }
func (id TrustedRootID) String() string   { return string(id) }
func (id CandidateID) String() string     { return string(id) }
func (id ActionID) String() string        { return string(id) }
func (id RunID) String() string           { return string(id) }
func (id CapabilityID) String() string    { return string(id) }
func (id ManagerObjectID) String() string { return string(id) }

func (id ProviderID) Validate() error      { return validateStableID("provider ID", string(id)) }
func (id TrustedRootID) Validate() error   { return validateStableID("trusted root ID", string(id)) }
func (id CandidateID) Validate() error     { return validateStableID("candidate ID", string(id)) }
func (id ActionID) Validate() error        { return validateStableID("action ID", string(id)) }
func (id RunID) Validate() error           { return validateStableID("run ID", string(id)) }
func (id CapabilityID) Validate() error    { return validateStableID("capability ID", string(id)) }
func (id ManagerObjectID) Validate() error { _, err := NewManagerObjectID(string(id)); return err }

func (command Command) Validate() error {
	switch command {
	case CommandMenu, CommandClean, CommandUninstall, CommandOptimize, CommandAnalyze,
		CommandStatus, CommandHistory, CommandPurge, CommandInstaller, CommandFingerprint,
		CommandCompletion, CommandUpdate, CommandRemove:
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

// NewPlanDigest constructs a nonzero digest from exactly one SHA-256 value.
func NewPlanDigest(value []byte) (PlanDigest, error) {
	parsed, err := newDigest(value)
	if err != nil {
		return PlanDigest{}, err
	}
	return PlanDigest{value: parsed}, nil
}

// NewActionBindingDigest constructs a nonzero action-binding digest from
// exactly one SHA-256 value.
func NewActionBindingDigest(value []byte) (ActionBindingDigest, error) {
	parsed, err := newDigest(value)
	if err != nil {
		return ActionBindingDigest{}, err
	}
	return ActionBindingDigest{value: parsed}, nil
}

// NewTrashLayoutBinding constructs a nonzero Trash-layout binding from
// exactly one SHA-256 value.
func NewTrashLayoutBinding(value []byte) (TrashLayoutBinding, error) {
	parsed, err := newDigest(value)
	if err != nil {
		return TrashLayoutBinding{}, err
	}
	return TrashLayoutBinding{value: parsed}, nil
}

func NewConfigDigest(value []byte) (ConfigDigest, error) {
	parsed, err := newDigest(value)
	if err != nil {
		return ConfigDigest{}, err
	}
	return ConfigDigest{value: parsed}, nil
}

func NewEvidenceDigest(value []byte) (EvidenceDigest, error) {
	parsed, err := newDigest(value)
	if err != nil {
		return EvidenceDigest{}, err
	}
	return EvidenceDigest{value: parsed}, nil
}

// ComputePlanDigest hashes only canonical plan-body bytes with the fixed v1
// domain separator. Callers must validate canonical bytes before comparing it.
func ComputePlanDigest(canonicalBody []byte) PlanDigest {
	hash := sha256.New()
	_, _ = hash.Write(planDigestDomain)
	_, _ = hash.Write(canonicalBody)
	var value [planDigestLength]byte
	copy(value[:], hash.Sum(nil))
	return PlanDigest{value: value}
}

// ComputeActionBindingDigest hashes canonical action-binding bytes with the
// fixed v1 domain separator. The canonical bytes must include the plan digest
// and action representation; this function does not establish plan membership.
func ComputeActionBindingDigest(canonicalBinding []byte) ActionBindingDigest {
	hash := sha256.New()
	_, _ = hash.Write(actionBindingDigestDomain)
	_, _ = hash.Write(canonicalBinding)
	var value [planDigestLength]byte
	copy(value[:], hash.Sum(nil))
	return ActionBindingDigest{value: value}
}

// ComputeTrashLayoutBinding hashes canonical authority-selected Trash layout
// evidence with a fixed domain separator. Callers must use an unambiguous
// encoding; only the opaque digest is exposed to consumers.
func ComputeTrashLayoutBinding(canonicalBinding []byte) TrashLayoutBinding {
	hash := sha256.New()
	_, _ = hash.Write(trashLayoutBindingDomain)
	_, _ = hash.Write(canonicalBinding)
	var value [planDigestLength]byte
	copy(value[:], hash.Sum(nil))
	return TrashLayoutBinding{value: value}
}

// Verify reports whether canonical body bytes bind to this digest. It does not
// grant authority and must not be used as an authorization decision.
func (digest PlanDigest) Verify(canonicalBody []byte) bool {
	return digest == ComputePlanDigest(canonicalBody)
}

// Verify reports whether canonical action-binding bytes bind to this digest.
// It is not an authorization or full-plan-membership decision.
func (digest ActionBindingDigest) Verify(canonicalBinding []byte) bool {
	return digest == ComputeActionBindingDigest(canonicalBinding)
}

// Verify reports whether canonical Trash-layout evidence binds to this value.
// It is a data-consistency check and never authorizes a layout or operation.
func (binding TrashLayoutBinding) Verify(canonicalBinding []byte) bool {
	return binding == ComputeTrashLayoutBinding(canonicalBinding)
}

func (digest PlanDigest) Bytes() []byte          { return append([]byte(nil), digest.value[:]...) }
func (digest ActionBindingDigest) Bytes() []byte { return append([]byte(nil), digest.value[:]...) }
func (binding TrashLayoutBinding) Bytes() []byte { return append([]byte(nil), binding.value[:]...) }
func (digest ConfigDigest) Bytes() []byte        { return append([]byte(nil), digest.value[:]...) }
func (digest EvidenceDigest) Bytes() []byte      { return append([]byte(nil), digest.value[:]...) }

func (digest PlanDigest) String() string          { return hex.EncodeToString(digest.value[:]) }
func (digest ActionBindingDigest) String() string { return hex.EncodeToString(digest.value[:]) }
func (binding TrashLayoutBinding) String() string { return hex.EncodeToString(binding.value[:]) }
func (digest ConfigDigest) String() string        { return hex.EncodeToString(digest.value[:]) }
func (digest EvidenceDigest) String() string      { return hex.EncodeToString(digest.value[:]) }

func (digest PlanDigest) Validate() error { return validateDigest("plan digest", digest.value) }
func (digest ActionBindingDigest) Validate() error {
	return validateDigest("action binding digest", digest.value)
}
func (binding TrashLayoutBinding) Validate() error {
	return validateDigest("Trash layout binding", binding.value)
}
func (digest ConfigDigest) Validate() error   { return validateDigest("config digest", digest.value) }
func (digest EvidenceDigest) Validate() error { return validateDigest("evidence digest", digest.value) }

// IsZero reports whether the binding has no valid digest value.
func (binding TrashLayoutBinding) IsZero() bool { return binding.value == [planDigestLength]byte{} }

// Equal reports whether two immutable Trash-layout bindings have the same
// digest value.
func (binding TrashLayoutBinding) Equal(other TrashLayoutBinding) bool { return binding == other }

func newDigest(value []byte) ([planDigestLength]byte, error) {
	if len(value) != planDigestLength {
		return [planDigestLength]byte{}, fmt.Errorf("digest must contain exactly %d bytes", planDigestLength)
	}
	var parsed [planDigestLength]byte
	copy(parsed[:], value)
	if err := validateDigest("digest", parsed); err != nil {
		return [planDigestLength]byte{}, err
	}
	return parsed, nil
}

func validateDigest(name string, value [planDigestLength]byte) error {
	var zero [planDigestLength]byte
	if value == zero {
		return fmt.Errorf("%s must not be all zero", name)
	}
	return nil
}

func validateStableID(name, value string) error {
	if value == "" || len(value) > 96 {
		return fmt.Errorf("%s must contain 1 through 96 bytes", name)
	}
	if value[0] == '-' || value[0] == '.' {
		return fmt.Errorf("%s must begin with a lowercase letter or digit", name)
	}
	for index, character := range []byte(value) {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case index > 0 && (character == '-' || character == '_' || character == '.'):
		default:
			return fmt.Errorf("%s contains unsupported byte %q", name, character)
		}
	}
	return nil
}
