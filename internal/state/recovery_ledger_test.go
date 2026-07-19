package state

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/linuxfs"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"github.com/biendo27/linux-deep-clean/internal/planproto"
	"github.com/biendo27/linux-deep-clean/internal/recoveryport"
)

func TestRecoveryFrameRoundTripsCanonicalImmutableBinding(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	encoded, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encodeRecoveryFrame() error = %v", err)
	}
	decoded, err := decodeRecoveryFrame(encoded)
	if err != nil {
		t.Fatalf("decodeRecoveryFrame() error = %v", err)
	}
	recovery, err := replayRecoveryFrames([]recoveryFrame{decoded})
	if err != nil {
		t.Fatalf("replayRecoveryFrames() error = %v", err)
	}
	if recovery.Event() != RecoveryEventIntentReserved {
		t.Fatalf("recovery event = %q, want %q", recovery.Event(), RecoveryEventIntentReserved)
	}
	if recovery.Destination() != RecoveryDestinationTrash {
		t.Fatalf("recovery destination = %q, want trash", recovery.Destination())
	}
	if recovery.RootID() != binding.root || !recovery.Source().Equal(binding.source) {
		t.Fatalf("recovery source = (%q, %q), want (%q, %q)", recovery.RootID(), recovery.Source().Display(), binding.root, binding.source.Display())
	}
	if recovery.Token() != binding.token || recovery.ActionBindingDigest() != binding.actionBindingDigest {
		t.Fatal("replay changed the immutable reservation identity")
	}

	canonical, err := encodeRecoveryFrame(decoded)
	if err != nil {
		t.Fatalf("encode decoded frame: %v", err)
	}
	if !bytes.Equal(encoded, canonical) {
		t.Fatal("frame did not retain canonical bytes after decode/re-encode")
	}
}

func TestRecoveryV2RequiresAndPreservesTrashLayoutBinding(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	trashAction := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)

	if _, err := ledger.Reserve(ctx, trashAction, planDigest, RecoveryReservation{}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(TrashPath without binding) error = %v, want ErrRecoveryUnsupported", err)
	}
	if records := sessions.count(); records != 0 {
		t.Fatalf("missing Trash binding wrote %d records, want none", records)
	}

	quarantineAction := testRecoveryAction(t, domain.ActionQuarantinePath)
	if _, err := ledger.Reserve(ctx, quarantineAction, planDigest, testRecoveryReservation(t, domain.ActionTrashPath)); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(QuarantinePath with binding) error = %v, want ErrRecoveryUnsupported", err)
	}
	if records := sessions.count(); records != 0 {
		t.Fatalf("Quarantine binding rejection wrote %d records, want none", records)
	}

	reservation := testRecoveryReservation(t, domain.ActionTrashPath)
	reserved, err := ledger.Reserve(ctx, trashAction, planDigest, reservation)
	if err != nil {
		t.Fatalf("Reserve(TrashPath with binding) error = %v", err)
	}
	if !reserved.TrashLayoutBinding().Equal(reservation.TrashLayoutBinding) {
		t.Fatal("reserved recovery did not retain its Trash layout binding")
	}

	fresh := newTestRecoveryLedger(sessions)
	reloaded, err := fresh.Reload(ctx, reserved)
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if !reloaded.TrashLayoutBinding().Equal(reservation.TrashLayoutBinding) {
		t.Fatal("reloaded recovery changed its immutable Trash layout binding")
	}
}

func TestRecoveryV2RejectsMissingOrChangedTrashLayoutBinding(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}

	wire := toRecoveryFrameV2Wire(intent)
	wire.TrashLayoutBinding = nil
	if _, err := fromRecoveryFrameV2Wire(wire); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("fromRecoveryFrameV2Wire(missing Trash binding) error = %v, want ErrRecoveryCorrupt", err)
	}
	wire = toRecoveryFrameV2Wire(intent)
	wire.TrashLayoutBinding = []byte{1}
	if _, err := fromRecoveryFrameV2Wire(wire); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("fromRecoveryFrameV2Wire(malformed Trash binding) error = %v, want ErrRecoveryCorrupt", err)
	}

	dispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("appendRecoveryTransition() error = %v", err)
	}
	changed := dispatch
	changed.binding.trashLayoutBinding = testTrashLayoutBindingWithByte(t, 8)
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, changed}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(changed Trash binding) error = %v, want ErrRecoveryCorrupt", err)
	}

	quarantine := testRecoveryBinding(t, domain.ActionQuarantinePath)
	quarantineIntent, err := newIntentFrame(quarantine)
	if err != nil {
		t.Fatalf("new quarantine intent: %v", err)
	}
	quarantineWire := toRecoveryFrameV2Wire(quarantineIntent)
	quarantineWire.TrashLayoutBinding = testTrashLayoutBinding(t).Bytes()
	if _, err := fromRecoveryFrameV2Wire(quarantineWire); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("fromRecoveryFrameV2Wire(Quarantine binding) error = %v, want ErrRecoveryCorrupt", err)
	}
}

func TestRecoveryV1HistoriesRemainReadableButRejectAppendsAndMixedSchemas(t *testing.T) {
	legacyBinding := testLegacyRecoveryBinding(t, domain.ActionTrashPath)
	legacyIntent, err := newIntentFrameForSchema(legacyBinding, recoveryLedgerSchemaVersionV1)
	if err != nil {
		t.Fatalf("newIntentFrameForSchema(v1) error = %v", err)
	}
	legacyDispatch := testLegacyRecoveryTransitionFrame(t, legacyIntent, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})

	replayed, err := replayRecoveryFrames([]recoveryFrame{legacyIntent, legacyDispatch})
	if err != nil {
		t.Fatalf("replayRecoveryFrames(v1) error = %v", err)
	}
	if !replayed.TrashLayoutBinding().IsZero() {
		t.Fatal("legacy v1 recovery unexpectedly exposed a Trash layout binding")
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{legacyIntent, legacyDispatch}, RecoveryTransition{Event: RecoveryEventMetadataVerified}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("appendRecoveryTransition(v1) error = %v, want ErrRecoveryUnsupported", err)
	}
	sessions := newTestRecoveryLedgerSessions()
	for _, frame := range []recoveryFrame{legacyIntent, legacyDispatch} {
		record := testRecoveryLedgerRecord(t, frame)
		sessions.setRecord(record.id, record.contents)
	}
	ledger := newTestRecoveryLedger(sessions)
	if _, err := ledger.Transition(context.Background(), replayed, RecoveryTransition{Event: RecoveryEventMetadataVerified}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Transition(v1) error = %v, want ErrRecoveryUnsupported", err)
	}
	if records := sessions.count(); records != 2 {
		t.Fatalf("Transition(v1) changed retained record count to %d, want 2", records)
	}

	mixed := legacyDispatch
	mixed.schemaVersion = recoveryLedgerSchemaVersionV2
	mixed.binding.trashLayoutBinding = testTrashLayoutBinding(t)
	if _, err := replayRecoveryFrames([]recoveryFrame{legacyIntent, mixed}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("replayRecoveryFrames(mixed schemas) error = %v, want ErrRecoveryCorrupt", err)
	}
}

const (
	// These are frozen canonical v1 CBOR records, gzip-compressed only to keep
	// this compatibility fixture readable. They are intentionally not produced
	// through the current v1 encoder during the test.
	legacyRecoveryV1IntentFixture   = "H4sIAAAAAAAAA+1WsW4UMRC9U4qARBGQEEQ0lAjplIKGggKJ0FNQkMqZs+d2feu1je3d5KDg4D8ouJMQRSJRIiT4mECBREED18J4N7ncLUeiQJEUuNm1Z8YznvfG47fCGROMDxCwE9CHTpwjlqiDkTrQhzn06EoUGEyGentdCd6Bfxw9bwrH8cV9DjzFdRkwT4wTUoNqSeBBGs2k8HVcCkWCrlMvZ/vSTGrRDw58yiyENBMUPJlHGVbLmVWgmZAJCR5dXz5mKOuQGy1k3GCXJ1KMMNNmS//AElSBq0tfeLFgUeRGYGO1PRRePmmurrREGFhMHCaFAodS/2Z59Waam4JS3nR0MXX4uJAOxeryzz5PQScoGIR5rWtX9m7Dezds9ZXUGeNxq3mNdkbhyp48ylgJLCVHlkPfuHmdc1MZBd+QtbUnJHNgJTpPOWw7yqhAjt4bN4WhdcwIOQYQEIAsvDW+gmOz2Me8S5hLnUx3+/zywqcP558++JjeuKd31buVZ5eXxKu7l259vfNw79uGLRt2FgbKgNhYXhv3atFOdzHLRINewkmfcWW2egFcgmFcK/SkQj/wxN6Z35GIFqMknkDBoF9RfC1SvM9NbomfOvhZ5i+sQZ2DBgqImW4feZiklHmBmuPzN5Vvf+iQRUGQIbqy0JWKfiezAZ1e0F6D9akJO2euos5qFTUyqK0z8Yc2chm6iaF5RI3t602c1ASCUmRyUDsTb4FnJGVEYO1rRk/Iq0UdKSTRD+fuuxMzanx6jJq5CP+z6+ywK3dYXfw1V7JILppDVxHpqDHnBKpg2OuRs9cpWEsJjQfvDghp6vglSBWVv6dkVR1+dJSWpJAMj3suFCYRNJIeozcN7O99/RlFan8m3tiOJQVQouhRNarLzCEIZrQaMKmrd5YbmCpblKEaFz/0BzRnhwW4NdenDtdL3LZkRLrUMMO0qPO6RBl0PTk52Wuo2cvL6rLg5K56YDFTkJ8cN38BTCP4FT8KAAA="
	legacyRecoveryV1DispatchFixture = "H4sIAAAAAAAAA+1WO28UMRDOKcWBRBFAiJcQFBQI6ZSCBokUSIQ+BQWpnDl7bte3Xntje+9yUHDwPyi4CESRSCkBKVRUNPkPgQJBgYTgmhQw3k0uuSMkChRJwVbeeXjG830z9oqwxnjjPHiseXS+Fv4RW6j9woUUPQjwwIR0GXgeM4vcWIECvUlQL0wrwWvwj1/DmdxyfHKXA49xWnpMIwoiNaiKBO6l0UwKV+aoUERoa6U42dQmUoumt+BiRmnGiaCDkHvQYSFOMgWaThGR4v6V6j6fysIxtZBhg2UeSdHDRJu2/oEtUDmeH//E812EIjUCR6SVrnDywah0Ykz4ToaRxShXYFHq3zzPXY9Tk2vPRgOdjC3O59KiOF/92eQx6AgFAz9sdfHs+k14Y7tjTSV1wnjYatiiklC6siH3clYCW5IjS6Fp7LDNsYGOkh/RVbQjJFNgLbSOalixVFGBHJ0zdgDD6dWrOF/91tkYv3X5XV7TM68rn2176vnaylrz0vvJDT9EP+MKOObyTczrhLnU0WC3j09PfFg9/nDmbXztjl5WryYenRkXz26fuvFl6t7619msNeKXQUcZELPVycVGqVqq784yMUIvYaVLuDLthgcboV8sDRpSoes4Yu+OZU8Ej14UTqCg0ywoPhko3uQmzYif2rudzN+1H3UKGighZupN5L4fU+UFao6PXxax3XZAFhRe+hAqg7pUtOzvTOjwknYaMhcbv3TkOuqodtFIBXVmTVjQRjZB2zf0H1Bjm3Z9KzWBoBS5bPVOnwY3T0jLiMDalYzuU9QMdaCQRNcdmncHZtTi4TFqxyD8z66jw67UYjH4S64kgVz0D3VFpKOLOSVQBcNGg4K9iCHLqKDh4PUOIT0moQVSBePvMXkVh+/tZSUpJcPDnrsqowAaafexGyT297H+jCJdfyZMbMuiHKhQ9MDqlW1mEQQzWnWY1OHNZWzHFNWiCpW4uK7bojnbbsD20D21LW/hQkZOZEsXph80dVq2KIO6oyAHew2N3uWtYlhwClc8sJjJKU6Kc78AF6KlX0sKAAA="
	legacyRecoveryV1IntentDigest    = "13bd24657107f279fb033b1fc4752d6e50b901ea72773ca5cfb0cf6a1dca2ffb"
	legacyRecoveryV1DispatchDigest  = "510856b5257420bb7a72c7696c2edc333a4d347edfe38664c0f87f7165272ccb"
)

func TestRecoveryV1FrozenCBORAndDigestCompatibility(t *testing.T) {
	fixtures := []struct {
		name      string
		encoded   string
		digest    string
		wantEvent RecoveryEvent
	}{
		{name: "intent", encoded: legacyRecoveryV1IntentFixture, digest: legacyRecoveryV1IntentDigest, wantEvent: RecoveryEventIntentReserved},
		{name: "metadata dispatch", encoded: legacyRecoveryV1DispatchFixture, digest: legacyRecoveryV1DispatchDigest, wantEvent: RecoveryEventMetadataDispatchRecorded},
	}

	frames := make([]recoveryFrame, len(fixtures))
	digests := make([][32]byte, len(fixtures))
	for index, fixture := range fixtures {
		raw := testFrozenRecoveryV1Frame(t, fixture.encoded)
		if len(raw) == 0 || raw[0] != 0xb0 {
			t.Fatalf("%s fixture map header = %x, want b0 for the frozen v1 field count", fixture.name, raw)
		}

		frame, err := decodeRecoveryFrame(raw)
		if err != nil {
			t.Fatalf("decode frozen %s frame: %v", fixture.name, err)
		}
		if frame.schemaVersion != recoveryLedgerSchemaVersionV1 || frame.event != fixture.wantEvent {
			t.Fatalf("frozen %s frame = schema %d, event %q", fixture.name, frame.schemaVersion, frame.event)
		}
		if !frame.binding.trashLayoutBinding.IsZero() {
			t.Fatalf("frozen %s v1 frame unexpectedly carries a Trash layout binding", fixture.name)
		}

		canonical, err := encodeRecoveryFrame(frame)
		if err != nil {
			t.Fatalf("re-encode frozen %s frame: %v", fixture.name, err)
		}
		if !bytes.Equal(raw, canonical) {
			t.Fatalf("frozen %s frame changed during decode/re-encode", fixture.name)
		}

		digest, err := recoveryFrameDigest(frame)
		if err != nil {
			t.Fatalf("digest frozen %s frame: %v", fixture.name, err)
		}
		if got := fmt.Sprintf("%x", digest); got != fixture.digest {
			t.Fatalf("frozen %s digest = %s, want %s", fixture.name, got, fixture.digest)
		}
		frames[index] = frame
		digests[index] = digest
	}

	if frames[1].predecessor != digests[0] {
		t.Fatal("frozen v1 dispatch frame does not bind the frozen intent digest")
	}
	replayed, err := replayRecoveryFrames(frames)
	if err != nil {
		t.Fatalf("replay frozen v1 predecessor chain: %v", err)
	}
	if replayed.schemaVersion != recoveryLedgerSchemaVersionV1 {
		t.Fatalf("replayed frozen schema = %d, want v1", replayed.schemaVersion)
	}
}

func testFrozenRecoveryV1Frame(t *testing.T, encoded string) []byte {
	t.Helper()
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode frozen v1 fixture base64: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open frozen v1 fixture gzip: %v", err)
	}
	raw, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatalf("read frozen v1 fixture gzip: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close frozen v1 fixture gzip: %v", closeErr)
	}
	return raw
}

func testLegacyRecoveryTransitionFrame(t *testing.T, previous recoveryFrame, transition RecoveryTransition) recoveryFrame {
	t.Helper()
	predecessor, err := recoveryFrameDigest(previous)
	if err != nil {
		t.Fatalf("recoveryFrameDigest(previous) error = %v", err)
	}
	frame := recoveryFrame{
		schemaVersion: recoveryLedgerSchemaVersionV1,
		binding:       previous.binding,
		event:         transition.Event,
		ordinal:       previous.ordinal + 1,
		predecessor:   predecessor,
		outcome:       transition.ReconciliationOutcome,
		metadata:      transition.MetadataDisposition,
	}
	if err := validateRecoveryFrame(frame); err != nil {
		t.Fatalf("validateRecoveryFrame(v1 transition) error = %v", err)
	}
	return frame
}

func TestRecoveryReducerEnforcesClosedTrashAndQuarantineGraphs(t *testing.T) {
	trashBinding := testRecoveryBinding(t, domain.ActionTrashPath)
	trashIntent, err := newIntentFrame(trashBinding)
	if err != nil {
		t.Fatalf("new trash intent: %v", err)
	}
	trashHistory := []recoveryFrame{trashIntent}
	for _, transition := range []RecoveryTransition{
		{Event: RecoveryEventMetadataDispatchRecorded},
		{Event: RecoveryEventMetadataVerified},
		{Event: RecoveryEventMoveDispatchRecorded},
		{Event: RecoveryEventMoveVerified},
		{Event: RecoveryEventRestoreDispatchRecorded},
		{Event: RecoveryEventRestoreVerified},
	} {
		next, err := appendRecoveryTransition(trashHistory, transition)
		if err != nil {
			t.Fatalf("append %q: %v", transition.Event, err)
		}
		trashHistory = append(trashHistory, next)
	}
	trashRecovery, err := replayRecoveryFrames(trashHistory)
	if err != nil {
		t.Fatalf("replay full trash graph: %v", err)
	}
	if !trashRecovery.Closed() {
		t.Fatal("verified restore did not close recovery")
	}
	if _, err := appendRecoveryTransition(trashHistory, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err == nil {
		t.Fatal("closed recovery accepted another transition")
	}

	if _, err := appendRecoveryTransition([]recoveryFrame{trashIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err == nil {
		t.Fatal("trash skipped required metadata boundary")
	}

	quarantineBinding := testRecoveryBinding(t, domain.ActionQuarantinePath)
	quarantineIntent, err := newIntentFrame(quarantineBinding)
	if err != nil {
		t.Fatalf("new quarantine intent: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded}); err == nil {
		t.Fatal("quarantine accepted a metadata event")
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); err != nil {
		t.Fatalf("quarantine did not permit its first move boundary: %v", err)
	}
}

func TestRecoveryReducerConstrainsIndeterminateAndCancellationFacts(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}

	cancelled, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
		MetadataDisposition:   RecoveryMetadataAbsent,
	})
	if err != nil {
		t.Fatalf("cancellation reconciliation: %v", err)
	}
	recovery, err := replayRecoveryFrames([]recoveryFrame{intent, cancelled})
	if err != nil || !recovery.Closed() {
		t.Fatalf("cancelled recovery = %#v, %v; want closed", recovery, err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
	}); err == nil {
		t.Fatal("cancellation reconciliation omitted its metadata fact")
	}

	metadataDispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("metadata dispatch: %v", err)
	}
	metadataIndeterminate, err := appendRecoveryTransition([]recoveryFrame{intent, metadataDispatch}, RecoveryTransition{Event: RecoveryEventMetadataIndeterminate})
	if err != nil {
		t.Fatalf("metadata indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{intent, metadataDispatch, metadataIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeMoveVerified,
		MetadataDisposition:   RecoveryMetadataRetained,
	}); err == nil {
		t.Fatal("metadata indeterminate accepted a content-side reconciliation outcome")
	}

	quarantineIntent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionQuarantinePath))
	if err != nil {
		t.Fatalf("new quarantine intent: %v", err)
	}
	moveDispatch, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if err != nil {
		t.Fatalf("move dispatch: %v", err)
	}
	moveIndeterminate, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch}, RecoveryTransition{Event: RecoveryEventMoveIndeterminate})
	if err != nil {
		t.Fatalf("move indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeRestoreVerified,
	}); err == nil {
		t.Fatal("move indeterminate accepted a restore-verified reconciliation without a restore dispatch")
	}

	moveVerified, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch}, RecoveryTransition{Event: RecoveryEventMoveVerified})
	if err != nil {
		t.Fatalf("move verified: %v", err)
	}
	restoreDispatch, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified}, RecoveryTransition{Event: RecoveryEventRestoreDispatchRecorded})
	if err != nil {
		t.Fatalf("restore dispatch: %v", err)
	}
	restoreIndeterminate, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch}, RecoveryTransition{Event: RecoveryEventRestoreIndeterminate})
	if err != nil {
		t.Fatalf("restore indeterminate: %v", err)
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch, restoreIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
	}); err == nil {
		t.Fatal("restore indeterminate accepted a closed not-applied fact while the verified move remains retained")
	}
	if _, err := appendRecoveryTransition([]recoveryFrame{quarantineIntent, moveDispatch, moveVerified, restoreDispatch, restoreIndeterminate}, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeMoveVerified,
	}); err != nil {
		t.Fatalf("restore indeterminate did not preserve a verified retained move: %v", err)
	}
}

func TestRecoveryFramesRejectNoncanonicalBindingAndChainGaps(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	encoded, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if _, err := decodeRecoveryFrame(append(encoded, 0)); err == nil {
		t.Fatal("decoder accepted a frame with trailing noncanonical bytes")
	}

	next, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("append transition: %v", err)
	}
	next.predecessor[0] ^= 1
	if _, err := replayRecoveryFrames([]recoveryFrame{intent, next}); err == nil {
		t.Fatal("replay accepted a predecessor digest mismatch")
	}

	badBinding := intent
	badBinding.binding.precondition.Target.Filesystem.Root = mustRecoveryRoot(t, "other-root")
	if _, err := encodeRecoveryFrame(badBinding); err == nil {
		t.Fatal("encoder accepted a precondition/root binding mismatch")
	}

	changedAction := testRecoveryAction(t, domain.ActionTrashPath)
	changedAction.Risk = domain.RiskMedium
	if err := changedAction.Validate(); err != nil {
		t.Fatalf("changed action validation: %v", err)
	}
	changedPayload, err := planproto.EncodeActionBinding(changedAction, binding.planDigest)
	if err != nil {
		t.Fatalf("EncodeActionBinding(changed action) error = %v", err)
	}
	badPayload := intent
	badPayload.binding.actionBindingPayload = changedPayload
	if _, err := encodeRecoveryFrame(badPayload); err == nil {
		t.Fatal("encoder accepted a binding payload that does not match the stored action digest")
	}
}

func TestRecoveryFrameDecoderRejectsStrictOuterCBORViolations(t *testing.T) {
	intent, err := newIntentFrame(testRecoveryBinding(t, domain.ActionTrashPath))
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	valid, err := encodeRecoveryFrame(intent)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if len(valid) == 0 || valid[0] != 0xb1 {
		t.Fatalf("canonical frame map header = %x, want b1 for the fixed v2 field count", valid)
	}

	unknownFields := testRecoveryFrameV2WireValues(toRecoveryFrameV2Wire(intent))
	unknownFields["future_field"] = "retained-but-unsupported"
	unknown, err := recoveryFrameEncMode.Marshal(unknownFields)
	if err != nil {
		t.Fatalf("encode unknown-field frame: %v", err)
	}
	duplicateKey, err := recoveryFrameEncMode.Marshal("event")
	if err != nil {
		t.Fatalf("encode duplicate key: %v", err)
	}
	duplicateValue, err := recoveryFrameEncMode.Marshal(string(RecoveryEventIntentReserved))
	if err != nil {
		t.Fatalf("encode duplicate value: %v", err)
	}
	duplicate := append([]byte{valid[0] + 1}, valid[1:]...)
	duplicate = append(duplicate, duplicateKey...)
	duplicate = append(duplicate, duplicateValue...)
	indefinite := append([]byte{0xbf}, valid[1:]...)
	indefinite = append(indefinite, 0xff)
	nonMinimal := append([]byte{0xb8, 0x11}, valid[1:]...)
	tagged := append([]byte{0xc0}, valid...)

	for name, encoded := range map[string][]byte{
		"unknown field":          unknown,
		"duplicate key":          duplicate,
		"indefinite length map":  indefinite,
		"non-minimal map header": nonMinimal,
		"tagged frame":           tagged,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRecoveryFrame(encoded); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("decodeRecoveryFrame() error = %v, want ErrRecoveryCorrupt", err)
			}
		})
	}
}

func testRecoveryFrameV2WireValues(wire recoveryFrameV2Wire) map[string]any {
	values := testRecoveryFrameWireValues(recoveryFrameV1Wire{
		SchemaVersion:         wire.SchemaVersion,
		ActionBindingDigest:   wire.ActionBindingDigest,
		ActionBindingPayload:  wire.ActionBindingPayload,
		PlanDigest:            wire.PlanDigest,
		Token:                 wire.Token,
		Root:                  wire.Root,
		Source:                wire.Source,
		ActionID:              wire.ActionID,
		ActionKind:            wire.ActionKind,
		Destination:           wire.Destination,
		Precondition:          wire.Precondition,
		Event:                 wire.Event,
		Ordinal:               wire.Ordinal,
		PredecessorDigest:     wire.PredecessorDigest,
		ReconciliationOutcome: wire.ReconciliationOutcome,
		MetadataDisposition:   wire.MetadataDisposition,
	})
	values["trash_layout_binding"] = wire.TrashLayoutBinding
	return values
}

func testRecoveryFrameWireValues(wire recoveryFrameWire) map[string]any {
	return map[string]any{
		"schema_version":         wire.SchemaVersion,
		"action_binding_digest":  wire.ActionBindingDigest,
		"action_binding_payload": wire.ActionBindingPayload,
		"plan_digest":            wire.PlanDigest,
		"token":                  wire.Token,
		"root":                   wire.Root,
		"source":                 wire.Source,
		"action_id":              wire.ActionID,
		"action_kind":            wire.ActionKind,
		"destination":            wire.Destination,
		"precondition":           wire.Precondition,
		"event":                  wire.Event,
		"ordinal":                wire.Ordinal,
		"predecessor_digest":     wire.PredecessorDigest,
		"reconciliation_outcome": wire.ReconciliationOutcome,
		"metadata_disposition":   wire.MetadataDisposition,
	}
}

func TestReplayRecoveryLedgerRecordsRejectsIdentifierMismatchAndDuplicateOutstandingSource(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	record := testRecoveryLedgerRecord(t, intent)
	wrongID, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, 1)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	record.id = wrongID
	if _, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{record}); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("identifier/frame mismatch error = %v, want ErrRecoveryCorrupt", err)
	}

	first := testRecoveryLedgerRecord(t, intent)
	secondBinding := binding
	secondBinding.token = "ldc-" + string(bytes.Repeat([]byte{'b'}, 64))
	secondIntent, err := newIntentFrame(secondBinding)
	if err != nil {
		t.Fatalf("new second intent: %v", err)
	}
	second := testRecoveryLedgerRecord(t, secondIntent)
	if _, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{second, first}); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("duplicate outstanding source error = %v, want ErrRecoveryConflict", err)
	}
}

func TestReplayRecoveryLedgerRecordsUsesImmutableRecordOrderAndRejectsCapacity(t *testing.T) {
	binding := testRecoveryBinding(t, domain.ActionQuarantinePath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("new intent: %v", err)
	}
	dispatch, err := appendRecoveryTransition([]recoveryFrame{intent}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if err != nil {
		t.Fatalf("append dispatch: %v", err)
	}
	verified, err := appendRecoveryTransition([]recoveryFrame{intent, dispatch}, RecoveryTransition{Event: RecoveryEventMoveVerified})
	if err != nil {
		t.Fatalf("append verification: %v", err)
	}
	histories, err := replayRecoveryLedgerRecords([]recoveryLedgerRecord{
		testRecoveryLedgerRecord(t, verified),
		testRecoveryLedgerRecord(t, intent),
		testRecoveryLedgerRecord(t, dispatch),
	})
	if err != nil {
		t.Fatalf("out-of-order replay error = %v", err)
	}
	if len(histories) != 1 || histories[0].recovery.Event() != RecoveryEventMoveVerified {
		t.Fatalf("out-of-order replay result = %#v, want move-verified recovery", histories)
	}

	overCapacity := make([]recoveryLedgerRecord, maximumRecoveryLedgerRecords+1)
	for index := range overCapacity {
		overCapacity[index] = testRecoveryLedgerRecord(t, intent)
	}
	if _, err := replayRecoveryLedgerRecords(overCapacity); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("over-capacity replay error = %v, want ErrRecoveryUnsupported", err)
	}
}

func TestRecoveryLedgerPublicationCapacityBlocksTheNextRecord(t *testing.T) {
	atCapacity := []recoveryHistory{{frames: make([]recoveryFrame, maximumRecoveryLedgerRecords)}}
	if err := requireRecoveryLedgerPublicationCapacity(atCapacity); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("exact-capacity publication guard error = %v, want ErrRecoveryConflict", err)
	}

	belowCapacity := []recoveryHistory{{frames: make([]recoveryFrame, maximumRecoveryLedgerRecords-1)}}
	if err := requireRecoveryLedgerPublicationCapacity(belowCapacity); err != nil {
		t.Fatalf("below-capacity publication guard error = %v", err)
	}
}

func TestRecoverySourceKeySeparatesRawComponentBoundaries(t *testing.T) {
	first := testRecoveryBinding(t, domain.ActionTrashPath)
	second := first
	firstPath, err := pathbytes.New([][]byte{[]byte("a"), []byte("bc")})
	if err != nil {
		t.Fatalf("first path: %v", err)
	}
	secondPath, err := pathbytes.New([][]byte{[]byte("ab"), []byte("c")})
	if err != nil {
		t.Fatalf("second path: %v", err)
	}
	first.source = firstPath
	second.source = secondPath
	if recoverySourceKey(first) == recoverySourceKey(second) {
		t.Fatal("recovery source key merged distinct raw-component paths")
	}
}

func TestRecoveryLedgerPublicLifecycleReplaysRetainedFacts(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	action := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)

	reserved, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if reserved.Event() != RecoveryEventIntentReserved || reserved.Ordinal() != 0 {
		t.Fatalf("reserved recovery = (%q, %d), want intent reservation at ordinal zero", reserved.Event(), reserved.Ordinal())
	}
	if _, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("duplicate Reserve() error = %v, want ErrRecoveryConflict", err)
	}

	found, ok, err := ledger.FindOutstanding(ctx, reserved.RootID(), reserved.Source())
	if err != nil || !ok || found.Token() != reserved.Token() {
		t.Fatalf("FindOutstanding() = (%#v, %t, %v), want retained reservation", found, ok, err)
	}
	outstanding, err := ledger.ListOutstanding(ctx, 1)
	if err != nil || len(outstanding) != 1 || outstanding[0].Token() != reserved.Token() {
		t.Fatalf("ListOutstanding() = (%#v, %v), want retained reservation", outstanding, err)
	}

	dispatch, err := ledger.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMetadataDispatchRecorded})
	if err != nil {
		t.Fatalf("metadata dispatch Transition() error = %v", err)
	}
	if dispatch.Ordinal() != 1 {
		t.Fatalf("metadata dispatch ordinal = %d, want 1", dispatch.Ordinal())
	}
	verified, err := ledger.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMetadataVerified})
	if err != nil {
		t.Fatalf("stale-view metadata verification Transition() error = %v", err)
	}
	if verified.Ordinal() != 2 || verified.Event() != RecoveryEventMetadataVerified {
		t.Fatalf("verified recovery = (%q, %d), want metadata verified ordinal 2", verified.Event(), verified.Ordinal())
	}

	fresh := newTestRecoveryLedger(sessions)
	reloaded, err := fresh.Reload(ctx, reserved)
	if err != nil {
		t.Fatalf("fresh Reload() error = %v", err)
	}
	if reloaded.Event() != RecoveryEventMetadataVerified || reloaded.Ordinal() != 2 {
		t.Fatalf("fresh Reload() = (%q, %d), want metadata verified ordinal 2", reloaded.Event(), reloaded.Ordinal())
	}
	if reloaded.PlanDigest() != planDigest || reloaded.ActionID() != action.ID || reloaded.ActionKind() != action.Kind || reloaded.Outcome() != "" || reloaded.MetadataDisposition() != "" {
		t.Fatal("fresh Reload() changed immutable action facts or added transition facts")
	}

	closed, err := fresh.Transition(ctx, reserved, RecoveryTransition{
		Event:                 RecoveryEventReconciliationResolved,
		ReconciliationOutcome: RecoveryOutcomeNotApplied,
		MetadataDisposition:   RecoveryMetadataAbsent,
	})
	if err != nil {
		t.Fatalf("closing Transition() error = %v", err)
	}
	if !closed.Closed() {
		t.Fatal("no-effect reconciliation did not close the retained recovery")
	}
	if closed.Outcome() != RecoveryOutcomeNotApplied || closed.MetadataDisposition() != RecoveryMetadataAbsent {
		t.Fatal("closed recovery did not retain reconciliation facts")
	}
	if _, ok, err := fresh.FindOutstanding(ctx, reserved.RootID(), reserved.Source()); err != nil || ok {
		t.Fatalf("FindOutstanding() after close = (_, %t, %v), want not found", ok, err)
	}

	replacement, err := fresh.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind))
	if err != nil {
		t.Fatalf("Reserve() after closed recovery error = %v", err)
	}
	if replacement.Token() == reserved.Token() {
		t.Fatal("new reservation reused a closed recovery token")
	}
}

func TestRecoveryLedgerPublicQueriesRemainBoundedAndExact(t *testing.T) {
	ctx := context.Background()
	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	first := testRecoveryAction(t, domain.ActionTrashPath)
	second := testRecoveryActionAtPath(t, domain.ActionQuarantinePath, [][]byte{[]byte("cache"), []byte("other")})
	planDigest := testRecoveryPlanDigest(t)
	if _, err := ledger.Reserve(ctx, first, planDigest, testRecoveryReservation(t, first.Kind)); err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	if _, err := ledger.Reserve(ctx, second, planDigest, testRecoveryReservation(t, second.Kind)); err != nil {
		t.Fatalf("second Reserve() error = %v", err)
	}

	if _, err := ledger.ListOutstanding(ctx, 1); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("bounded ListOutstanding() error = %v, want ErrRecoveryUnsupported", err)
	}
	missing, err := pathbytes.New([][]byte{[]byte("cache"), []byte("missing")})
	if err != nil {
		t.Fatalf("missing path: %v", err)
	}
	if _, found, err := ledger.FindOutstanding(ctx, first.Target.Filesystem.Root, missing); err != nil || found {
		t.Fatalf("FindOutstanding(missing) = (_, %t, %v), want not found", found, err)
	}
}

func TestRecoveryLedgerPublicPublicationInterruptionsReturnReloadableCandidates(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	action := testRecoveryAction(t, domain.ActionQuarantinePath)
	planDigest := testRecoveryPlanDigest(t)

	sessions.interruptNextPublish()
	reserved, err := ledger.Reserve(ctx, action, planDigest, testRecoveryReservation(t, action.Kind))
	if !errors.Is(err, linuxfs.ErrInterrupted) || reserved.Token() == "" {
		t.Fatalf("interrupted Reserve() = (%#v, %v), want candidate plus ErrInterrupted", reserved, err)
	}
	fresh := newTestRecoveryLedger(sessions)
	reloaded, err := fresh.Reload(ctx, reserved)
	if err != nil || reloaded.Token() != reserved.Token() {
		t.Fatalf("Reload() after interrupted reserve = (%#v, %v), want retained candidate", reloaded, err)
	}

	sessions.interruptNextPublish()
	updated, err := fresh.Transition(ctx, reserved, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded})
	if !errors.Is(err, linuxfs.ErrInterrupted) || updated.Event() != RecoveryEventMoveDispatchRecorded {
		t.Fatalf("interrupted Transition() = (%#v, %v), want move-dispatch candidate plus ErrInterrupted", updated, err)
	}
	reloaded, err = newTestRecoveryLedger(sessions).Reload(ctx, reserved)
	if err != nil || reloaded.Event() != RecoveryEventMoveDispatchRecorded || reloaded.Ordinal() != 1 {
		t.Fatalf("Reload() after interrupted transition = (%#v, %v), want retained move dispatch", reloaded, err)
	}
}

func TestRecoveryLedgerPublicReadersFailClosedWithoutRepairingCorruption(t *testing.T) {
	ctx := context.Background()
	sessions := newTestRecoveryLedgerSessions()
	ledger := newTestRecoveryLedger(sessions)
	binding := testRecoveryBinding(t, domain.ActionTrashPath)
	intent, err := newIntentFrame(binding)
	if err != nil {
		t.Fatalf("newIntentFrame() error = %v", err)
	}
	recovery, err := recoveryFromFrame(intent)
	if err != nil {
		t.Fatalf("recoveryFromFrame() error = %v", err)
	}
	id, err := linuxfs.NewPrivateLedgerRecordID(binding.actionBindingDigest.String(), binding.token, 0)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	corrupt := []byte{0xff}
	sessions.setRecord(id, corrupt)

	for name, call := range map[string]func() error{
		"find": func() error {
			_, _, err := ledger.FindOutstanding(ctx, recovery.RootID(), recovery.Source())
			return err
		},
		"list": func() error {
			_, err := ledger.ListOutstanding(ctx, 1)
			return err
		},
		"reload": func() error {
			_, err := ledger.Reload(ctx, recovery)
			return err
		},
		"reserve": func() error {
			_, err := ledger.Reserve(ctx, testRecoveryAction(t, domain.ActionTrashPath), testRecoveryPlanDigest(t), testRecoveryReservation(t, domain.ActionTrashPath))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatalf("%s error = %v, want ErrRecoveryCorrupt", name, err)
			}
			if retained := sessions.record(id); !bytes.Equal(retained, corrupt) {
				t.Fatalf("%s repaired corrupt record = %x, want %x", name, retained, corrupt)
			}
		})
	}
}

func TestRecoveryLedgerPublicGuardsAndProductionConstructor(t *testing.T) {
	if _, err := NewRecoveryLedger(nil); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("NewRecoveryLedger(nil) error = %v, want ErrRecoveryUnsupported", err)
	}
	production, err := NewRecoveryLedger(&linuxfs.PrivateDirectoryLease{})
	if err != nil {
		t.Fatalf("NewRecoveryLedger(zero lease) error = %v", err)
	}
	action := testRecoveryAction(t, domain.ActionTrashPath)
	planDigest := testRecoveryPlanDigest(t)
	if _, err := production.Reserve(context.Background(), action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("Reserve() through unqualified production lease error = %v, want linuxfs.ErrUnsupported", err)
	}
	if _, _, err := production.FindOutstanding(context.Background(), action.Target.Filesystem.Root, action.Target.Filesystem.Path); !errors.Is(err, linuxfs.ErrUnsupported) {
		t.Fatalf("FindOutstanding() through unqualified production lease error = %v, want linuxfs.ErrUnsupported", err)
	}

	ledger := newTestRecoveryLedger(newTestRecoveryLedgerSessions())
	if _, err := ledger.Reserve(nil, action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("Reserve(nil context) error = %v, want ErrInterrupted", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ledger.Reserve(cancelled, action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, linuxfs.ErrInterrupted) {
		t.Fatalf("Reserve(cancelled context) error = %v, want ErrInterrupted", err)
	}
	if _, err := ledger.Reserve(context.Background(), action, domain.PlanDigest{}, testRecoveryReservation(t, action.Kind)); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(zero digest) error = %v, want ErrRecoveryUnsupported", err)
	}
	unsupportedAction := action.Clone()
	unsupportedAction.Kind = domain.ActionDeleteRecreatablePath
	if _, err := ledger.Reserve(context.Background(), unsupportedAction, planDigest, testRecoveryReservation(t, domain.ActionTrashPath)); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Reserve(unsupported action) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, err := ledger.Transition(context.Background(), Recovery{}, RecoveryTransition{Event: RecoveryEventMoveDispatchRecorded}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("Transition(zero recovery) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, _, err := ledger.FindOutstanding(context.Background(), "", action.Target.Filesystem.Path); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("FindOutstanding(zero root) error = %v, want ErrRecoveryUnsupported", err)
	}
	if _, _, err := ledger.FindOutstanding(context.Background(), action.Target.Filesystem.Root, pathbytes.BytePath{}); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("FindOutstanding(empty path) error = %v, want ErrRecoveryUnsupported", err)
	}
	for _, limit := range []int{0, maximumRecoveryLedgerRecords + 1} {
		if _, err := ledger.ListOutstanding(context.Background(), limit); !errors.Is(err, ErrRecoveryUnsupported) {
			t.Fatalf("ListOutstanding(%d) error = %v, want ErrRecoveryUnsupported", limit, err)
		}
	}

	var nilLedger *RecoveryLedger
	if _, err := nilLedger.Reserve(context.Background(), action, planDigest, testRecoveryReservation(t, action.Kind)); !errors.Is(err, ErrRecoveryUnsupported) {
		t.Fatalf("nil ledger Reserve() error = %v, want ErrRecoveryUnsupported", err)
	}
}

func testRecoveryActionAtPath(t *testing.T, kind domain.ActionKind, components [][]byte) domain.Action {
	t.Helper()
	action := testRecoveryAction(t, kind)
	path, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(action.Target.Filesystem.Root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	action.Target = target
	action.Evidence[0].Filesystem.Target = target
	action.Precondition.Filesystem.Target = target
	if err := action.Validate(); err != nil {
		t.Fatalf("test action with source validation error = %v", err)
	}
	return action
}

// testRecoveryLedgerSessions is a package-local test seam. It models only the
// opaque session contract used by state; it grants no path or descriptor
// authority and is not a production storage implementation.
type testRecoveryLedgerSessions struct {
	mu                      sync.Mutex
	records                 map[linuxfs.PrivateLedgerRecordID][]byte
	listErr                 error
	readErr                 error
	prePublishErr           error
	interruptAfterNextWrite bool
}

func newTestRecoveryLedgerSessions() *testRecoveryLedgerSessions {
	return &testRecoveryLedgerSessions{records: make(map[linuxfs.PrivateLedgerRecordID][]byte)}
}

func newTestRecoveryLedger(sessions *testRecoveryLedgerSessions) *RecoveryLedger {
	return &RecoveryLedger{sessions: sessions}
}

func (sessions *testRecoveryLedgerSessions) withRead(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return sessions.withSession(ctx, false, callback)
}

func (sessions *testRecoveryLedgerSessions) withWrite(ctx context.Context, callback func(recoveryLedgerSession) error) error {
	return sessions.withSession(ctx, true, callback)
}

func (sessions *testRecoveryLedgerSessions) withSession(ctx context.Context, writable bool, callback func(recoveryLedgerSession) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: test session context: %v", linuxfs.ErrInterrupted, err)
	}
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return callback(testRecoveryLedgerSession{sessions: sessions, writable: writable})
}

func (sessions *testRecoveryLedgerSessions) interruptNextPublish() {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.interruptAfterNextWrite = true
}

func (sessions *testRecoveryLedgerSessions) failNextList(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.listErr = err
}

func (sessions *testRecoveryLedgerSessions) failNextRead(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.readErr = err
}

func (sessions *testRecoveryLedgerSessions) failNextPublishBeforeCreate(err error) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.prePublishErr = err
}

func (sessions *testRecoveryLedgerSessions) setRecord(id linuxfs.PrivateLedgerRecordID, contents []byte) {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	sessions.records[id] = append([]byte(nil), contents...)
}

func (sessions *testRecoveryLedgerSessions) record(id linuxfs.PrivateLedgerRecordID) []byte {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return append([]byte(nil), sessions.records[id]...)
}

func (sessions *testRecoveryLedgerSessions) count() int {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	return len(sessions.records)
}

type testRecoveryLedgerSession struct {
	sessions *testRecoveryLedgerSessions
	writable bool
}

func (session testRecoveryLedgerSession) Publish(ctx context.Context, id linuxfs.PrivateLedgerRecordID, contents []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: test publish context: %v", linuxfs.ErrInterrupted, err)
	}
	if !session.writable {
		return fmt.Errorf("%w: test read-only session cannot publish", linuxfs.ErrUnsupported)
	}
	if session.sessions.prePublishErr != nil {
		err := session.sessions.prePublishErr
		session.sessions.prePublishErr = nil
		return err
	}
	if _, exists := session.sessions.records[id]; exists {
		return fmt.Errorf("%w: test ledger record already exists", linuxfs.ErrUnsupported)
	}
	session.sessions.records[id] = append([]byte(nil), contents...)
	if session.sessions.interruptAfterNextWrite {
		session.sessions.interruptAfterNextWrite = false
		return fmt.Errorf("%w: injected post-create publication ambiguity", linuxfs.ErrInterrupted)
	}
	return nil
}

func (session testRecoveryLedgerSession) Read(ctx context.Context, id linuxfs.PrivateLedgerRecordID, maximumBytes int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: test read context: %v", linuxfs.ErrInterrupted, err)
	}
	if session.sessions.readErr != nil {
		err := session.sessions.readErr
		session.sessions.readErr = nil
		return nil, err
	}
	contents, found := session.sessions.records[id]
	if !found || len(contents) > maximumBytes {
		return nil, fmt.Errorf("%w: test ledger record is unavailable", linuxfs.ErrUnsupported)
	}
	return append([]byte(nil), contents...), nil
}

func (session testRecoveryLedgerSession) List(ctx context.Context, limit int) ([]linuxfs.PrivateLedgerRecordID, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: test list context: %v", linuxfs.ErrInterrupted, err)
	}
	if session.sessions.listErr != nil {
		err := session.sessions.listErr
		session.sessions.listErr = nil
		return nil, err
	}
	if limit <= 0 || len(session.sessions.records) > limit {
		return nil, fmt.Errorf("%w: test ledger list limit", linuxfs.ErrUnsupported)
	}
	ids := make([]linuxfs.PrivateLedgerRecordID, 0, len(session.sessions.records))
	for id := range session.sessions.records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		if ids[left].ActionBindingDigest() == ids[right].ActionBindingDigest() {
			if ids[left].Token() == ids[right].Token() {
				return ids[left].Ordinal() < ids[right].Ordinal()
			}
			return ids[left].Token() < ids[right].Token()
		}
		return ids[left].ActionBindingDigest() < ids[right].ActionBindingDigest()
	})
	return ids, nil
}

func testRecoveryLedgerRecord(t *testing.T, frame recoveryFrame) recoveryLedgerRecord {
	t.Helper()
	contents, err := encodeRecoveryFrame(frame)
	if err != nil {
		t.Fatalf("encodeRecoveryFrame() error = %v", err)
	}
	id, err := linuxfs.NewPrivateLedgerRecordID(frame.binding.actionBindingDigest.String(), frame.binding.token, frame.ordinal)
	if err != nil {
		t.Fatalf("NewPrivateLedgerRecordID() error = %v", err)
	}
	return recoveryLedgerRecord{id: id, contents: contents}
}

func testRecoveryBinding(t *testing.T, kind domain.ActionKind) recoveryBinding {
	t.Helper()
	binding, err := newRecoveryBinding(
		testRecoveryAction(t, kind),
		testRecoveryPlanDigest(t),
		"ldc-"+string(bytes.Repeat([]byte{'a'}, 64)),
		testRecoveryReservation(t, kind).TrashLayoutBinding,
	)
	if err != nil {
		t.Fatalf("newRecoveryBinding() error = %v", err)
	}
	return binding
}

func testLegacyRecoveryBinding(t *testing.T, kind domain.ActionKind) recoveryBinding {
	t.Helper()
	binding, err := newRecoveryBindingForSchema(
		testRecoveryAction(t, kind),
		testRecoveryPlanDigest(t),
		"ldc-"+string(bytes.Repeat([]byte{'a'}, 64)),
		domain.TrashLayoutBinding{},
		recoveryLedgerSchemaVersionV1,
	)
	if err != nil {
		t.Fatalf("newRecoveryBindingForSchema(v1) error = %v", err)
	}
	return binding
}

func testRecoveryReservation(t *testing.T, kind domain.ActionKind) RecoveryReservation {
	t.Helper()
	if kind != domain.ActionTrashPath {
		return RecoveryReservation{}
	}
	return RecoveryReservation{TrashLayoutBinding: testTrashLayoutBinding(t)}
}

func testRecoveryPortReservation(t *testing.T, kind domain.ActionKind) recoveryport.Reservation {
	t.Helper()
	return recoveryport.Reservation{TrashLayoutBinding: testRecoveryReservation(t, kind).TrashLayoutBinding}
}

func testTrashLayoutBinding(t *testing.T) domain.TrashLayoutBinding {
	return testTrashLayoutBindingWithByte(t, 9)
}

func testTrashLayoutBindingWithByte(t *testing.T, value byte) domain.TrashLayoutBinding {
	t.Helper()
	binding, err := domain.NewTrashLayoutBinding(bytes.Repeat([]byte{value}, 32))
	if err != nil {
		t.Fatalf("NewTrashLayoutBinding() error = %v", err)
	}
	return binding
}

func testRecoveryAction(t *testing.T, kind domain.ActionKind) domain.Action {
	t.Helper()
	root := mustRecoveryRoot(t, "state-test-root")
	path, err := pathbytes.New([][]byte{[]byte("cache"), []byte("item")})
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	target, err := domain.NewFilesystemTarget(root, path)
	if err != nil {
		t.Fatalf("NewFilesystemTarget() error = %v", err)
	}
	snapshot := domain.FilesystemSnapshot{
		DeviceMajor: domain.Uint32Fact{Known: true, Value: 8},
		DeviceMinor: domain.Uint32Fact{Known: true, Value: 1},
		Inode:       domain.Uint64Fact{Known: true, Value: 42},
		Type:        domain.FileTypeRegular,
		UID:         domain.Uint32Fact{Known: true, Value: 1000},
		GID:         domain.Uint32Fact{Known: true, Value: 1000},
		Mode:        domain.Uint32Fact{Known: true, Value: 0o600},
		LinkCount:   domain.Uint64Fact{Known: true, Value: 1},
		Size:        domain.Uint64Fact{Known: true, Value: 4096},
		ModifiedAt:  domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		ChangedAt:   domain.Int64Fact{Known: true, Value: 1_721_000_000_000_000_000},
		MountID:     domain.Uint64Fact{Known: true, Value: 17},
	}
	actionID, err := domain.NewActionID("state-ledger-action")
	if err != nil {
		t.Fatalf("NewActionID() error = %v", err)
	}
	capability, err := domain.NewCapabilityID("state-ledger-capability")
	if err != nil {
		t.Fatalf("NewCapabilityID() error = %v", err)
	}
	action := domain.Action{
		ID:     actionID,
		Kind:   kind,
		Target: target,
		Evidence: []domain.Evidence{{
			Kind:       domain.EvidenceFilesystemIdentity,
			Filesystem: &domain.FilesystemEvidence{Target: target, Snapshot: snapshot},
		}},
		Precondition: domain.Precondition{
			Kind: domain.PreconditionFilesystemIdentity,
			Filesystem: &domain.FilesystemPrecondition{
				Target: target,
				Required: domain.FilesystemFieldDevice |
					domain.FilesystemFieldInode |
					domain.FilesystemFieldType |
					domain.FilesystemFieldUID |
					domain.FilesystemFieldGID |
					domain.FilesystemFieldMode |
					domain.FilesystemFieldLinkCount |
					domain.FilesystemFieldSize |
					domain.FilesystemFieldModifiedAt |
					domain.FilesystemFieldChangedAt |
					domain.FilesystemFieldMountID,
				Snapshot: snapshot,
			},
		},
		Risk:                  domain.RiskLow,
		Reversibility:         domain.ReversibilityRecoverable,
		EstimatedEffect:       domain.SizeFacts{LinkCount: domain.Uint64Fact{Known: true, Value: 1}},
		RequiredCapability:    capability,
		ProviderGuarantee:     domain.ProviderGuarantee{Kind: domain.ProviderGuaranteeReadOnlyInventory},
		ExpectedPostcondition: domain.PostconditionTargetAbsent,
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("test action validation: %v", err)
	}
	return action
}

func testRecoveryPlanDigest(t *testing.T) domain.PlanDigest {
	t.Helper()
	digest, err := domain.NewPlanDigest(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("NewPlanDigest() error = %v", err)
	}
	return digest
}

func mustRecoveryRoot(t *testing.T, value string) domain.TrustedRootID {
	t.Helper()
	root, err := domain.NewTrustedRootID(value)
	if err != nil {
		t.Fatalf("NewTrustedRootID() error = %v", err)
	}
	return root
}
