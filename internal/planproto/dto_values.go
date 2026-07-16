package planproto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/fxamacker/cbor/v2"
)

func toCallerWire(caller domain.CallerEcho) callerWire {
	selected := make([]string, len(caller.SelectedCandidateIDs))
	for index, id := range caller.SelectedCandidateIDs {
		selected[index] = id.String()
	}
	return callerWire{
		RunID:                caller.RunID.String(),
		UID:                  caller.UID,
		Interactive:          caller.Interactive,
		SelectedCandidateIDs: selected,
	}
}

func fromCallerWire(wire callerWire) (domain.CallerEcho, error) {
	runID, err := domain.NewRunID(wire.RunID)
	if err != nil {
		return domain.CallerEcho{}, fmt.Errorf("caller run ID: %w", err)
	}
	selected := make([]domain.CandidateID, len(wire.SelectedCandidateIDs))
	for index, value := range wire.SelectedCandidateIDs {
		id, err := domain.NewCandidateID(value)
		if err != nil {
			return domain.CallerEcho{}, fmt.Errorf("caller selected candidate %d: %w", index, err)
		}
		selected[index] = id
	}
	return domain.CallerEcho{
		RunID:                runID,
		UID:                  wire.UID,
		Interactive:          wire.Interactive,
		SelectedCandidateIDs: selected,
	}, nil
}

func toCreationWire(context domain.CreationContext) creationWire {
	return creationWire{
		CreatedAtUnixNano: context.CreatedAt.UnixNano(),
		ToolVersion:       context.ToolVersion,
		HostProfile:       context.HostProfile,
	}
}

func fromCreationWire(wire creationWire) domain.CreationContext {
	return domain.CreationContext{
		CreatedAt:   time.Unix(0, wire.CreatedAtUnixNano).UTC(),
		ToolVersion: wire.ToolVersion,
		HostProfile: wire.HostProfile,
	}
}

func toCapabilitySnapshotWire(snapshot domain.CapabilitySnapshot) capabilitySnapshotWire {
	facts := make([]capabilityFactWire, len(snapshot.Facts))
	for index, fact := range snapshot.Facts {
		facts[index] = capabilityFactWire{ID: fact.ID.String(), State: string(fact.State), Version: fact.Version}
	}
	return capabilitySnapshotWire{Facts: facts}
}

func fromCapabilitySnapshotWire(wire capabilitySnapshotWire) (domain.CapabilitySnapshot, error) {
	facts := make([]domain.CapabilityFact, len(wire.Facts))
	for index, fact := range wire.Facts {
		id, err := domain.NewCapabilityID(fact.ID)
		if err != nil {
			return domain.CapabilitySnapshot{}, fmt.Errorf("capability fact %d ID: %w", index, err)
		}
		facts[index] = domain.CapabilityFact{ID: id, State: domain.CapabilityState(fact.State), Version: fact.Version}
	}
	return domain.NewCapabilitySnapshot(facts)
}

func toTargetWire(target domain.Target) targetWire {
	wire := targetWire{Kind: string(target.Kind)}
	if target.Filesystem != nil {
		wire.Filesystem = &filesystemTargetWire{
			Root: target.Filesystem.Root.String(),
			Path: toPathWire(target.Filesystem.Path),
		}
	}
	if target.ManagerObject != nil {
		wire.ManagerObject = &managerObjectTargetWire{
			Provider: target.ManagerObject.Provider.String(),
			Object:   target.ManagerObject.Object.String(),
			Scope:    string(target.ManagerObject.Scope),
		}
	}
	return wire
}

func fromTargetWire(wire targetWire, limits DecodeLimits) (domain.Target, error) {
	switch domain.TargetKind(wire.Kind) {
	case domain.TargetFilesystem:
		if wire.Filesystem == nil || wire.ManagerObject != nil {
			return domain.Target{}, fmt.Errorf("filesystem target must contain exactly one filesystem variant")
		}
		root, err := domain.NewTrustedRootID(wire.Filesystem.Root)
		if err != nil {
			return domain.Target{}, fmt.Errorf("filesystem target root: %w", err)
		}
		path, err := fromPathWire(wire.Filesystem.Path, limits)
		if err != nil {
			return domain.Target{}, fmt.Errorf("filesystem target path: %w", err)
		}
		return domain.NewFilesystemTarget(root, path)
	case domain.TargetManagerObject:
		if wire.ManagerObject == nil || wire.Filesystem != nil {
			return domain.Target{}, fmt.Errorf("manager-object target must contain exactly one manager-object variant")
		}
		provider, err := domain.NewProviderID(wire.ManagerObject.Provider)
		if err != nil {
			return domain.Target{}, fmt.Errorf("manager-object target provider: %w", err)
		}
		object, err := domain.NewManagerObjectID(wire.ManagerObject.Object)
		if err != nil {
			return domain.Target{}, fmt.Errorf("manager-object target object: %w", err)
		}
		return domain.NewManagerObjectTarget(provider, object, domain.ManagerScope(wire.ManagerObject.Scope))
	default:
		return domain.Target{}, fmt.Errorf("unknown target kind %q", wire.Kind)
	}
}

func toPathWire(path pathbytes.BytePath) pathWire {
	components := path.Components()
	wire := pathWire{Display: path.Display(), Components: make([]pathComponentWire, len(components))}
	for index, component := range components {
		if utf8.Valid(component) {
			value := string(component)
			wire.Components[index].UTF8 = &value
			continue
		}
		wire.Components[index].Base64 = append([]byte(nil), component...)
	}
	return wire
}

// cborPathWire is deliberately separate from the JSON-facing pathWire shape:
// every CBOR component is a byte string, whether or not it happens to be valid
// UTF-8. JSON retains its explicit utf8/base64 tagged union for readability
// without ever turning Display into path authority.
type cborPathWire struct {
	Display    string   `cbor:"display"`
	Components [][]byte `cbor:"components"`
}

var strictPathDecMode = newStrictPathDecMode()

func newStrictPathDecMode() cbor.DecMode {
	// DecodePlan and DecodeResult run scanCanonicalCBOR with the caller's
	// selected limits before invoking this custom unmarshaler. The nested mode
	// therefore needs to enforce only the structural CBOR rules; using the
	// library ceiling here prevents it from silently narrowing a deliberate
	// caller-selected profile. The mode is immutable and has no per-call state.
	mode, err := strictDecMode(
		cborMaximumNestedLevels,
		cborMaximumArrayElements,
		cborMaximumMapPairs,
	)
	if err != nil {
		panic(fmt.Sprintf("planproto strict path CBOR mode: %v", err))
	}
	return mode
}

// MarshalCBOR makes the authoritative raw component form unambiguous in CBOR.
// It is intentionally implemented on the shared DTO so every nested target,
// evidence, precondition, and recovery path follows the same representation.
func (wire pathWire) MarshalCBOR() ([]byte, error) {
	components := make([][]byte, len(wire.Components))
	for index, component := range wire.Components {
		switch {
		case component.UTF8 != nil && component.Base64 != nil:
			return nil, fmt.Errorf("path component %d has multiple exact forms", index)
		case component.UTF8 != nil:
			components[index] = []byte(*component.UTF8)
		case component.Base64 != nil:
			components[index] = append([]byte(nil), component.Base64...)
			if utf8.Valid(components[index]) {
				return nil, fmt.Errorf("path component %d must use utf8 exact form", index)
			}
		default:
			return nil, fmt.Errorf("path component %d has no exact form", index)
		}
	}
	return canonicalEncMode.Marshal(cborPathWire{Display: wire.Display, Components: components})
}

// UnmarshalCBOR reconstructs the JSON-facing exact representation from a CBOR
// path whose components are all raw byte strings. The outer strict scanner has
// already bounded this item before this method runs; canonical re-encoding
// rejects missing/unknown/noncanonical nested map representations.
func (wire *pathWire) UnmarshalCBOR(encoded []byte) error {
	var raw cborPathWire
	// The caller's raw scanner supplies the selected frame/path budgets before
	// this custom unmarshaler runs. This mode closes the remaining map-level
	// gaps (unknown/duplicate fields, tags, invalid UTF-8); outer re-encoding
	// then proves preferred encoding and exact field presence.
	if err := strictPathDecMode.Unmarshal(encoded, &raw); err != nil {
		return err
	}
	components := make([]pathComponentWire, len(raw.Components))
	for index, component := range raw.Components {
		if utf8.Valid(component) {
			value := string(component)
			components[index].UTF8 = &value
			continue
		}
		components[index].Base64 = append([]byte(nil), component...)
	}
	*wire = pathWire{Display: raw.Display, Components: components}
	return nil
}

type jsonPathWire struct {
	Display    string                  `json:"display"`
	Components []jsonPathComponentWire `json:"components"`
}

type jsonPathComponentWire struct {
	UTF8   *string `json:"utf8"`
	Base64 *string `json:"base64"`
}

// UnmarshalJSON preserves the distinction between an omitted union arm and a
// JSON null. Exact path components have exactly one string-valued arm; a null
// arm would otherwise normalize away and make two JSON values express the same
// authority.
func (component *jsonPathComponentWire) UnmarshalJSON(encoded []byte) error {
	type rawComponent struct {
		UTF8   json.RawMessage `json:"utf8"`
		Base64 json.RawMessage `json:"base64"`
	}

	var raw rawComponent
	if err := decodeJSONDTO(encoded, &raw); err != nil {
		return err
	}
	if raw.UTF8 != nil && raw.Base64 != nil {
		return fmt.Errorf("path component has multiple exact forms")
	}
	if raw.UTF8 == nil && raw.Base64 == nil {
		return fmt.Errorf("path component has no exact form")
	}

	if raw.UTF8 != nil {
		if bytes.Equal(bytes.TrimSpace(raw.UTF8), []byte("null")) {
			return fmt.Errorf("path component utf8 form must be a string")
		}
		if err := validateJSONStringSurrogateEscapes(raw.UTF8); err != nil {
			return fmt.Errorf("path component utf8 form: %w", err)
		}
		var value string
		if err := json.Unmarshal(raw.UTF8, &value); err != nil {
			return fmt.Errorf("path component utf8 form: %w", err)
		}
		*component = jsonPathComponentWire{UTF8: &value}
		return nil
	}

	if bytes.Equal(bytes.TrimSpace(raw.Base64), []byte("null")) {
		return fmt.Errorf("path component base64 form must be a string")
	}
	var value string
	if err := json.Unmarshal(raw.Base64, &value); err != nil {
		return fmt.Errorf("path component base64 form: %w", err)
	}
	*component = jsonPathComponentWire{Base64: &value}
	return nil
}

// UnmarshalJSON accepts only the explicit exact path form. In particular, it
// rejects ambiguous UTF-8/base64 dual forms and base64 spellings that would
// normalize to different JSON bytes. Display is retained only for later
// one-way canonical rendering comparison in fromPathWire.
func (wire *pathWire) UnmarshalJSON(encoded []byte) error {
	var raw jsonPathWire
	if err := decodeJSONDTO(encoded, &raw); err != nil {
		return err
	}
	components := make([]pathComponentWire, len(raw.Components))
	for index, component := range raw.Components {
		switch {
		case component.UTF8 != nil && component.Base64 != nil:
			return fmt.Errorf("path component %d has multiple exact forms", index)
		case component.UTF8 != nil:
			components[index].UTF8 = component.UTF8
		case component.Base64 != nil:
			decoded, err := base64.StdEncoding.Strict().DecodeString(*component.Base64)
			if err != nil || base64.StdEncoding.EncodeToString(decoded) != *component.Base64 {
				return fmt.Errorf("path component %d has noncanonical base64", index)
			}
			components[index].Base64 = decoded
		default:
			return fmt.Errorf("path component %d has no exact form", index)
		}
	}
	*wire = pathWire{Display: raw.Display, Components: components}
	return nil
}

func fromPathWire(wire pathWire, limits DecodeLimits) (pathbytes.BytePath, error) {
	if len(wire.Components) == 0 {
		return pathbytes.BytePath{}, fmt.Errorf("path requires at least one component")
	}
	if len(wire.Components) > limits.MaxPathComponents {
		return pathbytes.BytePath{}, fmt.Errorf("path exceeds %d-component limit", limits.MaxPathComponents)
	}
	components := make([][]byte, len(wire.Components))
	for index, component := range wire.Components {
		switch {
		case component.UTF8 != nil && component.Base64 != nil:
			return pathbytes.BytePath{}, fmt.Errorf("path component %d has multiple exact forms", index)
		case component.UTF8 != nil:
			components[index] = []byte(*component.UTF8)
		case component.Base64 != nil:
			components[index] = append([]byte(nil), component.Base64...)
			if utf8.Valid(components[index]) {
				return pathbytes.BytePath{}, fmt.Errorf("path component %d must use utf8 exact form", index)
			}
		default:
			return pathbytes.BytePath{}, fmt.Errorf("path component %d has no exact form", index)
		}
	}
	path, err := pathbytes.New(components)
	if err != nil {
		return pathbytes.BytePath{}, err
	}
	if wire.Display != path.Display() {
		return pathbytes.BytePath{}, fmt.Errorf("path display is not the canonical one-way rendering")
	}
	return path, nil
}

func toSizeFactsWire(facts domain.SizeFacts) sizeFactsWire {
	return sizeFactsWire{
		Apparent:  toByteQuantityWire(facts.Apparent),
		Allocated: toByteQuantityWire(facts.Allocated),
		Estimated: toSizeEffectWire(facts.Estimated),
		Verified:  toSizeEffectWire(facts.Verified),
		LinkCount: toUint64FactWire(facts.LinkCount),
		Aggregate: facts.Aggregate,
	}
}

func fromSizeFactsWire(wire sizeFactsWire) domain.SizeFacts {
	return domain.SizeFacts{
		Apparent:  fromByteQuantityWire(wire.Apparent),
		Allocated: fromByteQuantityWire(wire.Allocated),
		Estimated: fromSizeEffectWire(wire.Estimated),
		Verified:  fromSizeEffectWire(wire.Verified),
		LinkCount: fromUint64FactWire(wire.LinkCount),
		Aggregate: wire.Aggregate,
	}
}

func toByteQuantityWire(value domain.ByteQuantity) byteQuantityWire {
	return byteQuantityWire{Available: value.Available, Bytes: value.Bytes}
}

func fromByteQuantityWire(wire byteQuantityWire) domain.ByteQuantity {
	return domain.ByteQuantity{Available: wire.Available, Bytes: wire.Bytes}
}

func toSizeEffectWire(value domain.SizeEffect) sizeEffectWire {
	return sizeEffectWire{Apparent: toByteQuantityWire(value.Apparent), Allocated: toByteQuantityWire(value.Allocated)}
}

func fromSizeEffectWire(wire sizeEffectWire) domain.SizeEffect {
	return domain.SizeEffect{Apparent: fromByteQuantityWire(wire.Apparent), Allocated: fromByteQuantityWire(wire.Allocated)}
}

func toUint64FactWire(value domain.Uint64Fact) uint64FactWire {
	return uint64FactWire{Known: value.Known, Value: value.Value}
}

func fromUint64FactWire(wire uint64FactWire) domain.Uint64Fact {
	return domain.Uint64Fact{Known: wire.Known, Value: wire.Value}
}

func toUint32FactWire(value domain.Uint32Fact) uint32FactWire {
	return uint32FactWire{Known: value.Known, Value: value.Value}
}

func fromUint32FactWire(wire uint32FactWire) domain.Uint32Fact {
	return domain.Uint32Fact{Known: wire.Known, Value: wire.Value}
}

func toInt64FactWire(value domain.Int64Fact) int64FactWire {
	return int64FactWire{Known: value.Known, Value: value.Value}
}

func fromInt64FactWire(wire int64FactWire) domain.Int64Fact {
	return domain.Int64Fact{Known: wire.Known, Value: wire.Value}
}

func toFilesystemSnapshotWire(snapshot domain.FilesystemSnapshot) filesystemSnapshotWire {
	return filesystemSnapshotWire{
		DeviceMajor: toUint32FactWire(snapshot.DeviceMajor),
		DeviceMinor: toUint32FactWire(snapshot.DeviceMinor),
		Inode:       toUint64FactWire(snapshot.Inode),
		Type:        string(snapshot.Type),
		UID:         toUint32FactWire(snapshot.UID),
		GID:         toUint32FactWire(snapshot.GID),
		Mode:        toUint32FactWire(snapshot.Mode),
		LinkCount:   toUint64FactWire(snapshot.LinkCount),
		Size:        toUint64FactWire(snapshot.Size),
		ModifiedAt:  toInt64FactWire(snapshot.ModifiedAt),
		ChangedAt:   toInt64FactWire(snapshot.ChangedAt),
		MountID:     toUint64FactWire(snapshot.MountID),
	}
}

func fromFilesystemSnapshotWire(wire filesystemSnapshotWire) domain.FilesystemSnapshot {
	return domain.FilesystemSnapshot{
		DeviceMajor: fromUint32FactWire(wire.DeviceMajor),
		DeviceMinor: fromUint32FactWire(wire.DeviceMinor),
		Inode:       fromUint64FactWire(wire.Inode),
		Type:        domain.FileType(wire.Type),
		UID:         fromUint32FactWire(wire.UID),
		GID:         fromUint32FactWire(wire.GID),
		Mode:        fromUint32FactWire(wire.Mode),
		LinkCount:   fromUint64FactWire(wire.LinkCount),
		Size:        fromUint64FactWire(wire.Size),
		ModifiedAt:  fromInt64FactWire(wire.ModifiedAt),
		ChangedAt:   fromInt64FactWire(wire.ChangedAt),
		MountID:     fromUint64FactWire(wire.MountID),
	}
}
