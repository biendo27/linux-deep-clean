//go:build linux

package mounts

import (
	"errors"
	"testing"

	"github.com/biendo27/linux-deep-clean/internal/domain"
	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
	"golang.org/x/sys/unix"
)

func TestOpenTrustedTrashBindsDedicatedBundleAndMapsMetadataOnly(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{
		Basis:  TrashMetadataBasisTopRelative,
		Prefix: mustTrashMetadataPath(t, [][]byte{[]byte("projects"), []byte("root")}),
	})

	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	defer lease.Close()

	if lease.RootID() != rootID {
		t.Fatalf("lease.RootID() = %q, want %q", lease.RootID(), rootID)
	}
	if lease.Placement() != TrashPlacementTopUser {
		t.Fatalf("lease.Placement() = %q, want top user", lease.Placement())
	}
	if lease.MetadataBasis() != TrashMetadataBasisTopRelative {
		t.Fatalf("lease.MetadataBasis() = %q, want top relative", lease.MetadataBasis())
	}

	mapped, err := lease.MetadataPathFor(mustTrashMetadataPath(t, [][]byte{[]byte("cache"), {0xff, 'x'}}))
	if err != nil {
		t.Fatalf("lease.MetadataPathFor() error = %v", err)
	}
	wantMapped := mustTrashMetadataPath(t, [][]byte{[]byte("projects"), []byte("root"), []byte("cache"), {0xff, 'x'}})
	if !mapped.Equal(wantMapped) {
		t.Fatalf("lease.MetadataPathFor() = %x, want %x", mapped.Components(), wantMapped.Components())
	}

	pair, err := lease.duplicateWith(trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()))
	if err != nil {
		t.Fatalf("lease.duplicateWith() error = %v", err)
	}
	defer pair.Close()
	for name, fd := range map[string]int{"files": pair.FilesFD, "info": pair.InfoFD} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("%s duplicate flags = (%#x, %v), want FD_CLOEXEC", name, flags, err)
		}
	}
}

func TestOpenTrustedTrashRequalifiesAStickySharedParent(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopShared, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	authority.Expected.SharedTop.UID = 4242
	authority.Expected.SharedTop.Mode = 0o1770

	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(
			testInspection(),
			authority.Expected.TrashRoot.inspection(),
			authority.Expected.Files.inspection(),
			authority.Expected.Info.inspection(),
			authority.Expected.SharedTop.inspection(),
		),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	defer lease.Close()

	pair, err := lease.duplicateWith(trashInspectionSequence(
		testInspection(),
		authority.Expected.TrashRoot.inspection(),
		authority.Expected.Files.inspection(),
		authority.Expected.Info.inspection(),
		authority.Expected.SharedTop.inspection(),
	))
	if err != nil {
		t.Fatalf("lease.duplicateWith() error = %v", err)
	}
	defer pair.Close()
}

func TestTopologyQualifiedTrashLeaseDuplicatesAllLiteralLayoutRoles(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	authority.Topology = TrashTopology{
		AnchorKind: TrashAnchorFilesystemTop,
		Anchor:     testTrashLayoutEvidence(200, 1000, 0o700),
	}
	directories := trashTestDirectories(t, false)
	authority.Open = func() (TrashDescriptors, error) {
		return openTrashTopologyTestDescriptors(directories, false)
	}

	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(
			testInspection(),
			authority.Topology.Anchor.inspection(),
			authority.Expected.TrashRoot.inspection(),
			authority.Expected.Files.inspection(),
			authority.Expected.Info.inspection(),
		),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	defer lease.Close()

	if lease.AnchorKind() != TrashAnchorFilesystemTop {
		t.Fatalf("lease.AnchorKind() = %q, want filesystem top", lease.AnchorKind())
	}
	set, err := lease.duplicateTopologyWith(trashInspectionSequence(
		testInspection(),
		authority.Expected.TrashRoot.inspection(),
		authority.Expected.Files.inspection(),
		authority.Expected.Info.inspection(),
		authority.Topology.Anchor.inspection(),
	))
	if err != nil {
		t.Fatalf("lease.duplicateTopologyWith() error = %v", err)
	}
	defer set.Close()
	for name, fd := range map[string]int{
		"anchor":     set.AnchorFD,
		"Trash root": set.TrashRootFD,
		"files":      set.FilesFD,
		"info":       set.InfoFD,
	} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("topology %s duplicate flags = (%#x, %v), want FD_CLOEXEC", name, flags, err)
		}
	}
	if set.SharedTopFD != -1 {
		t.Fatalf("top-user topology duplicate shared parent = %d, want absent", set.SharedTopFD)
	}
}

func TestTrashLeaseMetadataReconciliationIdentityBindsCompleteQualifiedLayout(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTopologyTrashAuthorityForIdentity(t, rootID, TrashPlacementTopUser, TrashMetadataMapping{
		Basis:  TrashMetadataBasisTopRelative,
		Prefix: mustTrashMetadataPath(t, [][]byte{[]byte("projects"), {0xff, 'x'}}),
	})

	first := openTopologyTrashLeaseForIdentity(t, root, authority)
	firstIdentity, err := first.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("first MetadataReconciliationIdentity() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first topology-qualified Trash lease Close() error = %v", err)
	}
	second := openTopologyTrashLeaseForIdentity(t, root, authority)
	defer second.Close()
	secondIdentity, err := second.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("second MetadataReconciliationIdentity() error = %v", err)
	}
	if firstIdentity.IsZero() {
		t.Fatal("MetadataReconciliationIdentity() returned a zero binding")
	}
	if !firstIdentity.Equal(secondIdentity) {
		t.Fatalf("equivalent reopened Trash layouts had different bindings: %s != %s", firstIdentity, secondIdentity)
	}

	for _, test := range []struct {
		name      string
		mutate    func(*TrashAuthority)
		wantEqual bool
	}{
		{
			name: "raw metadata prefix",
			mutate: func(authority *TrashAuthority) {
				authority.Metadata.Prefix = mustTrashMetadataPath(t, [][]byte{[]byte("projects"), {0xfe, 'x'}})
			},
		},
		{
			name: "metadata component boundary",
			mutate: func(authority *TrashAuthority) {
				authority.Metadata.Prefix = mustTrashMetadataPath(t, [][]byte{[]byte("projects"), {0xff}, []byte("x")})
			},
		},
		{
			name: "topology anchor evidence",
			mutate: func(authority *TrashAuthority) {
				authority.Topology.Anchor.Inode += 10
			},
		},
		{
			name: "expected info evidence",
			mutate: func(authority *TrashAuthority) {
				authority.Expected.Info.Inode++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := authority
			test.mutate(&changed)
			lease := openTopologyTrashLeaseForIdentity(t, root, changed)
			defer lease.Close()

			identity, err := lease.MetadataReconciliationIdentity()
			if err != nil {
				t.Fatalf("MetadataReconciliationIdentity() error = %v", err)
			}
			if firstIdentity.Equal(identity) != test.wantEqual {
				t.Fatalf("layout binding equality = %t, want %t", firstIdentity.Equal(identity), test.wantEqual)
			}
		})
	}

	rootWithDifferentMountEvidence := openLayoutTestRoot(t)
	defer rootWithDifferentMountEvidence.Close()
	rootWithDifferentMountEvidence.expected.Mount.Source = "/dev/different-trash-layout"
	mountChanged := authority
	setTrashAuthorityMountEvidence(&mountChanged, rootWithDifferentMountEvidence.expected.Mount)
	mountChangedLease := openTopologyTrashLeaseForIdentity(t, rootWithDifferentMountEvidence, mountChanged)
	defer mountChangedLease.Close()
	mountChangedIdentity, err := mountChangedLease.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("mount-evidence MetadataReconciliationIdentity() error = %v", err)
	}
	if firstIdentity.Equal(mountChangedIdentity) {
		t.Fatal("different full mount evidence produced the same layout binding")
	}

	sharedAuthority := testTopologyTrashAuthorityForIdentity(t, rootID, TrashPlacementTopShared, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	shared := openTopologyTrashLeaseForIdentity(t, root, sharedAuthority)
	defer shared.Close()
	sharedIdentity, err := shared.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("shared MetadataReconciliationIdentity() error = %v", err)
	}
	sharedChangedAuthority := sharedAuthority
	sharedChangedAuthority.Expected.SharedTop.Inode++
	sharedChanged := openTopologyTrashLeaseForIdentity(t, root, sharedChangedAuthority)
	defer sharedChanged.Close()
	sharedChangedIdentity, err := sharedChanged.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("changed shared MetadataReconciliationIdentity() error = %v", err)
	}
	if sharedIdentity.Equal(sharedChangedIdentity) {
		t.Fatal("different shared Trash role evidence produced the same layout binding")
	}
}

func TestTrashLeaseMetadataReconciliationIdentityFailsClosed(t *testing.T) {
	var nilLease *TrashLease
	identity, err := nilLease.MetadataReconciliationIdentity()
	if !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil MetadataReconciliationIdentity() error = %v, want ErrLeaseClosed", err)
	}
	if !identity.IsZero() {
		t.Fatal("nil MetadataReconciliationIdentity() returned a nonzero binding")
	}

	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	legacyAuthority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	legacy, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: legacyAuthority},
		root,
		trashInspectionSequence(testInspection(), legacyAuthority.Expected.TrashRoot.inspection(), legacyAuthority.Expected.Files.inspection(), legacyAuthority.Expected.Info.inspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open legacy trusted Trash lease: %v", err)
	}
	defer legacy.Close()
	identity, err = legacy.MetadataReconciliationIdentity()
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy MetadataReconciliationIdentity() error = %v, want ErrUnsupported", err)
	}
	if !identity.IsZero() {
		t.Fatal("legacy MetadataReconciliationIdentity() returned a nonzero binding")
	}

	qualified := openTopologyTrashLeaseForIdentity(t, root, testTopologyTrashAuthorityForIdentity(t, rootID, TrashPlacementTopUser, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative}))
	if err := qualified.Close(); err != nil {
		t.Fatalf("qualified Trash lease Close() error = %v", err)
	}
	identity, err = qualified.MetadataReconciliationIdentity()
	if !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("closed MetadataReconciliationIdentity() error = %v, want ErrLeaseClosed", err)
	}
	if !identity.IsZero() {
		t.Fatal("closed MetadataReconciliationIdentity() returned a nonzero binding")
	}
}

func TestTrashLeaseMetadataReconciliationIdentityHasCanonicalKnownVector(t *testing.T) {
	root := openLayoutTestRoot(t)
	authority := testTopologyTrashAuthorityForIdentity(t, root.RootID(), TrashPlacementTopUser, TrashMetadataMapping{
		Basis:  TrashMetadataBasisTopRelative,
		Prefix: mustTrashMetadataPath(t, [][]byte{[]byte("projects"), {0xff, 'x'}}),
	})
	lease := openTopologyTrashLeaseForIdentity(t, root, authority)
	defer lease.Close()

	identity, err := lease.MetadataReconciliationIdentity()
	if err != nil {
		t.Fatalf("MetadataReconciliationIdentity() error = %v", err)
	}
	const want = "fadc32237533dd0a23614f5e12134609ea6355c25479b8e6dec0032542190e5f"
	if got := identity.String(); got != want {
		t.Fatalf("MetadataReconciliationIdentity() = %s, want frozen canonical vector %s", got, want)
	}
}

func TestTopologyQualifiedTrashLeaseClosesRejectedDescriptorHandoffs(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	authority.Topology = TrashTopology{
		AnchorKind: TrashAnchorFilesystemTop,
		Anchor:     testTrashLayoutEvidence(200, 1000, 0o700),
	}
	directories := trashTestDirectories(t, false)
	authority.Open = func() (TrashDescriptors, error) {
		return openTrashTopologyTestDescriptors(directories, false)
	}

	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(
			testInspection(),
			authority.Topology.Anchor.inspection(),
			authority.Expected.TrashRoot.inspection(),
			authority.Expected.Files.inspection(),
			authority.Expected.Info.inspection(),
		),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	defer lease.Close()

	inspections := func() rootInspector {
		return trashInspectionSequence(
			testInspection(),
			authority.Expected.TrashRoot.inspection(),
			authority.Expected.Files.inspection(),
			authority.Expected.Info.inspection(),
			authority.Topology.Anchor.inspection(),
		)
	}

	firstDuplicate := -1
	calls := 0
	_, err = lease.duplicateTopologyWithFcntl(inspections(), func(fd int) (int, error) {
		calls++
		if calls == 1 {
			duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
			firstDuplicate = duplicate
			return duplicate, err
		}
		return -1, unix.EMFILE
	})
	if !errors.Is(err, unix.EMFILE) {
		t.Fatalf("lease.duplicateTopologyWithFcntl() error = %v, want EMFILE", err)
	}
	assertTrashFDClosed(t, "first topology duplicate after partial failure", firstDuplicate)

	if _, err := lease.duplicateTopologyWithFcntl(inspections(), func(int) (int, error) { return 2, nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateTopologyWithFcntl(standard stream) error = %v, want ErrInvalidAuthority", err)
	}

	if _, err := lease.duplicateTopologyWithFcntl(inspections(), func(int) (int, error) { return lease.descriptors.Anchor, unix.EIO }); !errors.Is(err, unix.EIO) {
		t.Fatalf("lease.duplicateTopologyWithFcntl(held descriptor with error) error = %v, want EIO", err)
	}
	if _, err := unix.FcntlInt(uintptr(lease.descriptors.Anchor), unix.F_GETFD, 0); err != nil {
		t.Fatalf("held topology anchor after rejected duplicate error = %v, want still open", err)
	}
	if _, err := lease.duplicateTopologyWithFcntl(nil, func(int) (int, error) { return -1, nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateTopologyWithFcntl(nil inspect) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := lease.duplicateTopologyWithFcntl(inspections(), nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateTopologyWithFcntl(nil duplicate) error = %v, want ErrInvalidAuthority", err)
	}
}

func TestTopologyQualifiedTrashLeaseRejectsLegacyOrInconsistentAuthority(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	legacy := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: legacy},
		root,
		trashInspectionSequence(testInspection(), legacy.Expected.TrashRoot.inspection(), legacy.Expected.Files.inspection(), legacy.Expected.Info.inspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open trusted legacy Trash lease: %v", err)
	}
	defer lease.Close()
	if _, err := lease.duplicateTopologyWith(testInspectionFor); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("legacy lease.duplicateTopologyWith() error = %v, want ErrUnsupported", err)
	}

	valid := legacy
	valid.Topology = TrashTopology{
		AnchorKind: TrashAnchorFilesystemTop,
		Anchor:     testTrashLayoutEvidence(200, 1000, 0o700),
	}
	for _, test := range []struct {
		name   string
		mutate func(*TrashAuthority)
	}{
		{
			name: "home uses filesystem-top anchor",
			mutate: func(authority *TrashAuthority) {
				authority.Placement = TrashPlacementHome
				authority.Metadata.Basis = TrashMetadataBasisHomeAbsolute
			},
		},
		{
			name: "anchor aliases Trash root",
			mutate: func(authority *TrashAuthority) {
				authority.Topology.Anchor.Inode = authority.Expected.TrashRoot.Inode
			},
		},
		{
			name: "anchor mount differs",
			mutate: func(authority *TrashAuthority) {
				authority.Topology.Anchor.Mount.ID++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := valid
			test.mutate(&authority)
			if err := authority.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("TrashAuthority.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
}

func TestOpenTrustedTrashRejectsTopologyDescriptorWithoutTopologyAuthority(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	directories := trashTestDirectories(t, false)
	authority.Open = func() (TrashDescriptors, error) {
		return openTrashTopologyTestDescriptors(directories, false)
	}

	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(
			testInspection(),
			authority.Expected.TrashRoot.inspection(),
			authority.Expected.Files.inspection(),
			authority.Expected.Info.inspection(),
		),
		func() error { return nil },
	)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedTrashWith() error = %v, want ErrInvalidAuthority", err)
	}
	if lease != nil {
		defer lease.Close()
		t.Fatal("legacy authority accepted an unbound topology descriptor")
	}
}

func TestTrashAuthorityFailsClosedForPlacementBasisOwnershipAndLayout(t *testing.T) {
	rootID := testRootID(t)
	valid := testTrashAuthority(t, rootID, TrashPlacementTopShared, 1000, TrashMetadataMapping{
		Basis:  TrashMetadataBasisTopRelative,
		Prefix: mustTrashMetadataPath(t, [][]byte{[]byte("root")}),
	})
	if err := valid.validate(rootID); err != nil {
		t.Fatalf("valid TrashAuthority.validate() error = %v", err)
	}
	nonRootShared := valid
	nonRootShared.Expected.SharedTop.UID = 4242
	nonRootShared.Expected.SharedTop.Mode = 0o1770
	if err := nonRootShared.validate(rootID); err != nil {
		t.Fatalf("non-root sticky Trash parent validation error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*TrashAuthority)
	}{
		{
			name:   "unknown placement",
			mutate: func(authority *TrashAuthority) { authority.Placement = TrashPlacement("unknown") },
		},
		{
			name:   "top shared absolute metadata",
			mutate: func(authority *TrashAuthority) { authority.Metadata.Basis = TrashMetadataBasisHomeAbsolute },
		},
		{
			name:   "shared trash without sticky bit",
			mutate: func(authority *TrashAuthority) { authority.Expected.SharedTop.Mode &^= unix.S_ISVTX },
		},
		{
			name:   "files owned by another uid",
			mutate: func(authority *TrashAuthority) { authority.Expected.Files.UID++ },
		},
		{
			name:   "files permit group access",
			mutate: func(authority *TrashAuthority) { authority.Expected.Files.Mode = 0o750 },
		},
		{
			name:   "info aliases files",
			mutate: func(authority *TrashAuthority) { authority.Expected.Info.Inode = authority.Expected.Files.Inode },
		},
		{
			name:   "bundle crosses mount",
			mutate: func(authority *TrashAuthority) { authority.Expected.Info.Mount.ID++ },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authority := valid
			test.mutate(&authority)
			if err := authority.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("TrashAuthority.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}

	topUser := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	topUser.Expected.TrashRoot.Mode = 0o755
	if err := topUser.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("TopUser non-0700 validation error = %v, want ErrInvalidAuthority", err)
	}
	topUser = testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	topUser.Expected.SharedTop = valid.Expected.SharedTop
	if err := topUser.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("TopUser unselected shared-top evidence validation error = %v, want ErrInvalidAuthority", err)
	}

	home := testTrashAuthority(t, rootID, TrashPlacementHome, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisHomeAbsolute})
	home.Expected.HasSharedTop = true
	home.Expected.SharedTop = valid.Expected.SharedTop
	if err := home.validate(rootID); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("Home shared-top validation error = %v, want ErrInvalidAuthority", err)
	}
}

func TestTrashDescriptorBundleValidationFailsClosed(t *testing.T) {
	valid := TrashDescriptors{Anchor: -1, TrashRoot: 3, Files: 4, Info: 5, SharedTop: -1, normalized: true}
	if err := valid.validate(TrashPlacementTopUser); err != nil {
		t.Fatalf("valid TrashDescriptors.validate() error = %v", err)
	}

	tests := []struct {
		name      string
		placement TrashPlacement
		bundle    TrashDescriptors
	}{
		{name: "unknown placement", placement: TrashPlacement("future"), bundle: valid},
		{name: "unnormalized bundle", placement: TrashPlacementTopUser, bundle: TrashDescriptors{Anchor: -1, TrashRoot: 3, Files: 4, Info: 5, SharedTop: -1}},
		{name: "reserved descriptor", placement: TrashPlacementTopUser, bundle: TrashDescriptors{Anchor: -1, TrashRoot: 2, Files: 4, Info: 5, SharedTop: -1, normalized: true}},
		{name: "aliased protected descriptors", placement: TrashPlacementTopUser, bundle: TrashDescriptors{Anchor: -1, TrashRoot: 3, Files: 4, Info: 4, SharedTop: -1, normalized: true}},
		{name: "unexpected shared parent", placement: TrashPlacementTopUser, bundle: TrashDescriptors{Anchor: -1, TrashRoot: 3, Files: 4, Info: 5, SharedTop: 6, normalized: true}},
		{name: "missing shared parent", placement: TrashPlacementTopShared, bundle: valid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.bundle.validate(test.placement); !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("TrashDescriptors.validate() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
}

func TestOpenTrustedTrashClosesEveryOpenerDescriptorOnFailure(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	opened := TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}
	directories := trashTestDirectories(t, false)
	authority.Open = func() (TrashDescriptors, error) {
		var err error
		opened, err = openTrashTestDescriptors(directories, false)
		return opened, err
	}
	driftedFiles := authority.Expected.Files.inspection()
	driftedFiles.UID++

	_, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), driftedFiles),
		func() error { return nil },
	)
	if !errors.Is(err, ErrDrifted) {
		t.Fatalf("openTrustedTrashWith() error = %v, want ErrDrifted", err)
	}
	for name, fd := range map[string]int{"trash root": opened.TrashRoot, "files": opened.Files, "info": opened.Info} {
		if fd < 0 {
			t.Fatalf("opener did not return %s descriptor", name)
		}
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
			t.Fatalf("rejected %s descriptor error = %v, want EBADF", name, err)
		}
	}
}

func TestOpenTrustedTrashRejectsAliasedBundleBeforeRoleInspection(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	directories := trashTestDirectories(t, false)
	opened := TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}
	authority.Open = func() (TrashDescriptors, error) {
		var err error
		opened, err = openTrashTestDescriptors(directories, false)
		if err != nil {
			return opened, err
		}
		opened.Files = opened.TrashRoot
		return opened, nil
	}

	inspections := 0
	_, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		func(int) (MountInspection, error) {
			inspections++
			return testInspection(), nil
		},
		func() error { return nil },
	)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("openTrustedTrashWith() error = %v, want ErrInvalidAuthority", err)
	}
	if inspections != 1 {
		t.Fatalf("inspection calls = %d, want only root requalification before bundle rejection", inspections)
	}
	for name, fd := range map[string]int{"trash root": opened.TrashRoot, "info": opened.Info} {
		assertTrashFDClosed(t, name, fd)
	}
}

func TestOpenTrustedTrashClosesPartialBundleReturnedWithError(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	directories := trashTestDirectories(t, false)
	opened := TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}
	authority.Open = func() (TrashDescriptors, error) {
		var err error
		opened, err = openTrashTestDescriptors(directories, false)
		if err != nil {
			return opened, err
		}
		return opened, unix.EACCES
	}

	_, err := openTrustedTrashWith(StaticTrashRegistry{rootID: authority}, root, testInspectionFor, func() error { return nil })
	if !errors.Is(err, unix.EACCES) {
		t.Fatalf("openTrustedTrashWith() error = %v, want EACCES", err)
	}
	for name, fd := range map[string]int{"trash root": opened.TrashRoot, "files": opened.Files, "info": opened.Info} {
		assertTrashFDClosed(t, name, fd)
	}
}

func TestOpenTrustedTrashFailsClosedBeforeCallingTheOpener(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	valid := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	openerCalls := 0
	valid.Open = func() (TrashDescriptors, error) {
		openerCalls++
		return TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}, nil
	}
	invalid := valid
	invalid.Metadata.Basis = TrashMetadataBasisHomeAbsolute

	tests := []struct {
		name        string
		registry    TrashRegistry
		inspect     rootInspector
		checkKernel kernelChecker
		want        error
	}{
		{name: "unknown bundle", registry: StaticTrashRegistry{}, inspect: testInspectionFor, checkKernel: func() error { return nil }, want: ErrUnknownTrash},
		{name: "invalid authority", registry: StaticTrashRegistry{rootID: invalid}, inspect: testInspectionFor, checkKernel: func() error { return nil }, want: ErrInvalidAuthority},
		{name: "kernel rejection", registry: StaticTrashRegistry{rootID: valid}, inspect: testInspectionFor, checkKernel: func() error { return ErrUnsupported }, want: ErrUnsupported},
		{name: "root requalification drift", registry: StaticTrashRegistry{rootID: valid}, inspect: func(int) (MountInspection, error) { return MountInspection{}, ErrDrifted }, checkKernel: func() error { return nil }, want: ErrDrifted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			openerCalls = 0
			_, err := openTrustedTrashWith(test.registry, root, test.inspect, test.checkKernel)
			if !errors.Is(err, test.want) {
				t.Fatalf("openTrustedTrashWith() error = %v, want %v", err, test.want)
			}
			if openerCalls != 0 {
				t.Fatalf("opener calls = %d, want 0 before %s rejection", openerCalls, test.name)
			}
		})
	}
}

func TestTrashLeaseRequalifiesAtPointOfUseAndClosesPartialDuplicate(t *testing.T) {
	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementTopUser, 1000, TrashMetadataMapping{Basis: TrashMetadataBasisTopRelative})
	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	defer lease.Close()

	driftedInfo := authority.Expected.Info.inspection()
	driftedInfo.Inode++
	if _, err := lease.duplicateWith(trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), driftedInfo)); !errors.Is(err, ErrDrifted) {
		t.Fatalf("lease.duplicateWith(drifted info) error = %v, want ErrDrifted", err)
	}

	firstDuplicate := -1
	calls := 0
	_, err = lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(fd int) (int, error) {
			calls++
			if calls == 1 {
				duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
				firstDuplicate = duplicate
				return duplicate, err
			}
			return -1, unix.EMFILE
		},
	)
	if !errors.Is(err, unix.EMFILE) {
		t.Fatalf("lease.duplicateWithFcntl() error = %v, want EMFILE", err)
	}
	if firstDuplicate < 0 {
		t.Fatal("duplicate seam did not create the first descriptor")
	}
	if _, err := unix.FcntlInt(uintptr(firstDuplicate), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("first duplicate after partial failure error = %v, want EBADF", err)
	}

	if _, err := lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(int) (int, error) { return 2, nil },
	); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(standard stream) error = %v, want ErrInvalidAuthority", err)
	}

	firstDuplicate = -1
	calls = 0
	_, err = lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(fd int) (int, error) {
			calls++
			if calls == 1 {
				duplicate, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 3)
				firstDuplicate = duplicate
				return duplicate, err
			}
			return firstDuplicate, nil
		},
	)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(aliased pair) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := unix.FcntlInt(uintptr(firstDuplicate), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("first duplicate after aliased pair error = %v, want EBADF", err)
	}

	if _, err := lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(int) (int, error) { return lease.descriptors.Files, nil },
	); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(held descriptor) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := unix.FcntlInt(uintptr(lease.descriptors.Files), unix.F_GETFD, 0); err != nil {
		t.Fatalf("held files descriptor after rejected alias error = %v, want still open", err)
	}

	unsafeDuplicate, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatalf("open non-CLOEXEC duplicate candidate: %v", err)
	}
	if _, err := lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(int) (int, error) { return unsafeDuplicate, nil },
	); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(non-CLOEXEC descriptor) error = %v, want ErrInvalidAuthority", err)
	}
	assertTrashFDClosed(t, "rejected non-CLOEXEC duplicate", unsafeDuplicate)

	duplicateWithError, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open duplicate-with-error candidate: %v", err)
	}
	if _, err := lease.duplicateWithFcntl(
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func(int) (int, error) { return duplicateWithError, unix.EIO },
	); !errors.Is(err, unix.EIO) {
		t.Fatalf("lease.duplicateWithFcntl(descriptor plus error) error = %v, want EIO", err)
	}
	assertTrashFDClosed(t, "duplicate returned with error", duplicateWithError)

	if _, err := lease.duplicateWithFcntl(nil, func(int) (int, error) { return -1, nil }); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(nil inspect) error = %v, want ErrInvalidAuthority", err)
	}
	if _, err := lease.duplicateWithFcntl(testInspectionFor, nil); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.duplicateWithFcntl(nil duplicate) error = %v, want ErrInvalidAuthority", err)
	}

	originalRootID := lease.rootID
	lease.rootID = domain.TrustedRootID("trash-root-drift")
	if _, err := lease.duplicateWith(testInspectionFor); !errors.Is(err, ErrDrifted) {
		t.Fatalf("lease.duplicateWith(root identity drift) error = %v, want ErrDrifted", err)
	}
	lease.rootID = originalRootID
}

func TestTrashDescriptorClosuresIgnoreReservedAndRepeatedFDs(t *testing.T) {
	var nilBundle *TrashDescriptors
	if err := nilBundle.Close(); err != nil {
		t.Fatalf("nil TrashDescriptors.Close() error = %v", err)
	}
	bundle := TrashDescriptors{}
	if err := bundle.Close(); err != nil {
		t.Fatalf("zero TrashDescriptors.Close() error = %v", err)
	}
	if bundle != (TrashDescriptors{Anchor: -1, TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}) {
		t.Fatalf("zero TrashDescriptors.Close() left %+v, want all fields released", bundle)
	}

	var nilPair *TrashDescriptorPair
	if err := nilPair.Close(); err != nil {
		t.Fatalf("nil TrashDescriptorPair.Close() error = %v", err)
	}
	pair := TrashDescriptorPair{}
	if err := pair.Close(); err != nil {
		t.Fatalf("zero TrashDescriptorPair.Close() error = %v", err)
	}
	if pair != (TrashDescriptorPair{FilesFD: -1, InfoFD: -1}) {
		t.Fatalf("zero TrashDescriptorPair.Close() left %+v, want both fields released", pair)
	}

	directory := t.TempDir()
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open repeated bundle descriptor: %v", err)
	}
	bundle = TrashDescriptors{Anchor: -1, TrashRoot: fd, Files: fd, Info: -1, SharedTop: -1, normalized: true}
	if err := bundle.Close(); err != nil {
		t.Fatalf("repeated TrashDescriptors.Close() error = %v", err)
	}
	assertTrashFDClosed(t, "repeated bundle descriptor", fd)

	fd, err = unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open repeated pair descriptor: %v", err)
	}
	pair = TrashDescriptorPair{FilesFD: fd, InfoFD: fd}
	if err := pair.Close(); err != nil {
		t.Fatalf("repeated TrashDescriptorPair.Close() error = %v", err)
	}
	assertTrashFDClosed(t, "repeated pair descriptor", fd)

	var nilTopologySet *TrashTopologyDescriptorSet
	if err := nilTopologySet.Close(); err != nil {
		t.Fatalf("nil TrashTopologyDescriptorSet.Close() error = %v", err)
	}
	topologySet := TrashTopologyDescriptorSet{}
	if err := topologySet.Close(); err != nil {
		t.Fatalf("zero TrashTopologyDescriptorSet.Close() error = %v", err)
	}
	if topologySet != EmptyTrashTopologyDescriptorSet() {
		t.Fatalf("zero TrashTopologyDescriptorSet.Close() left %+v, want all fields released", topologySet)
	}

	fd, err = unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open repeated topology descriptor: %v", err)
	}
	topologySet = TrashTopologyDescriptorSet{AnchorFD: fd, TrashRootFD: fd, FilesFD: fd, InfoFD: fd, SharedTopFD: fd}
	if err := topologySet.Close(); err != nil {
		t.Fatalf("repeated TrashTopologyDescriptorSet.Close() error = %v", err)
	}
	assertTrashFDClosed(t, "repeated topology descriptor", fd)
}

func TestNewTrashTopologyDescriptorsRequiresAnchorAndClosesTransferredDescriptors(t *testing.T) {
	directory := t.TempDir()
	openDirectory := func() int {
		t.Helper()
		fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatalf("open topology descriptor candidate: %v", err)
		}
		return fd
	}
	trashRoot := openDirectory()
	files := openDirectory()
	info := openDirectory()

	descriptors, err := NewTrashTopologyDescriptors(-1, trashRoot, files, info, -1)
	if !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("NewTrashTopologyDescriptors() error = %v, want ErrInvalidAuthority", err)
	}
	if descriptors != emptyTrashDescriptors() {
		t.Fatalf("NewTrashTopologyDescriptors() descriptors = %+v, want empty bundle", descriptors)
	}
	for name, fd := range map[string]int{"Trash root": trashRoot, "files": files, "info": info} {
		assertTrashFDClosed(t, name+" after missing anchor", fd)
	}
}

func TestNewTrashDescriptorsNormalizesAnOwnedStandardDescriptor(t *testing.T) {
	directories := trashTestDirectories(t, false)
	closeTrashTestStandardInput(t)
	trashRoot, err := unix.Open(directories.trashRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open Trash root after closing stdin: %v", err)
	}
	if trashRoot != 0 {
		_ = unix.Close(trashRoot)
		t.Fatalf("Trash root descriptor = %d, want 0 after closing stdin", trashRoot)
	}
	files, err := unix.Open(directories.files, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(trashRoot)
		t.Fatalf("open Trash files: %v", err)
	}
	info, err := unix.Open(directories.info, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = unix.Close(trashRoot)
		_ = unix.Close(files)
		t.Fatalf("open Trash info: %v", err)
	}

	descriptors, err := NewTrashDescriptors(trashRoot, files, info, -1)
	if err != nil {
		t.Fatalf("NewTrashDescriptors() error = %v", err)
	}
	defer descriptors.Close()
	if err := descriptors.validate(TrashPlacementTopUser); err != nil {
		t.Fatalf("normalized descriptors validate() error = %v", err)
	}
	if descriptors.TrashRoot < 3 || descriptors.Files < 3 || descriptors.Info < 3 {
		t.Fatalf("normalized descriptors = %+v, want all owned descriptors >= 3", descriptors)
	}
	if _, err := unix.FcntlInt(0, unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("owned descriptor 0 after normalization error = %v, want EBADF", err)
	}
}

func closeTrashTestStandardInput(t *testing.T) {
	t.Helper()
	saved, err := unix.FcntlInt(0, unix.F_DUPFD_CLOEXEC, 3)
	if err != nil && !errors.Is(err, unix.EBADF) {
		t.Fatalf("duplicate stdin before descriptor normalization test: %v", err)
	}
	if err == nil {
		t.Cleanup(func() {
			if err := unix.Dup3(saved, 0, 0); err != nil {
				t.Errorf("restore stdin after descriptor normalization test: %v", err)
			}
			if err := unix.Close(saved); err != nil {
				t.Errorf("close saved stdin after descriptor normalization test: %v", err)
			}
		})
	}
	if err := unix.Close(0); err != nil && !errors.Is(err, unix.EBADF) {
		t.Fatalf("close stdin before descriptor normalization test: %v", err)
	}
}

func TestTrashLeaseClosedAndMetadataMappingFailClosed(t *testing.T) {
	var nilLease *TrashLease
	if nilLease.RootID() != "" || nilLease.Placement() != "" || nilLease.AnchorKind() != "" || nilLease.MetadataBasis() != "" {
		t.Fatal("nil TrashLease accessors exposed authority")
	}
	if err := nilLease.Close(); err != nil {
		t.Fatalf("nil TrashLease.Close() error = %v", err)
	}
	if _, err := nilLease.DuplicateTrashDescriptorPair(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil TrashLease.DuplicateTrashDescriptorPair() error = %v, want ErrLeaseClosed", err)
	}
	if _, err := nilLease.DuplicateTrashTopologyDescriptorSet(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil TrashLease.DuplicateTrashTopologyDescriptorSet() error = %v, want ErrLeaseClosed", err)
	}
	if _, err := nilLease.MetadataPathFor(mustTrashMetadataPath(t, [][]byte{[]byte("source")})); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("nil TrashLease.MetadataPathFor() error = %v, want ErrLeaseClosed", err)
	}

	root := openLayoutTestRoot(t)
	rootID := root.RootID()
	authority := testTrashAuthority(t, rootID, TrashPlacementHome, 1000, TrashMetadataMapping{
		Basis:  TrashMetadataBasisHomeAbsolute,
		Prefix: mustTrashMetadataPath(t, [][]byte{[]byte("home"), []byte("ldc")}),
	})
	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{rootID: authority},
		root,
		trashInspectionSequence(testInspection(), authority.Expected.TrashRoot.inspection(), authority.Expected.Files.inspection(), authority.Expected.Info.inspection()),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("openTrustedTrashWith() error = %v", err)
	}
	if _, err := lease.MetadataPathFor(pathbytes.BytePath{}); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.MetadataPathFor(empty path) error = %v, want ErrInvalidAuthority", err)
	}
	overBudgetBytes := make([]byte, 256<<10)
	for index := range overBudgetBytes {
		overBudgetBytes[index] = 'x'
	}
	overBudget := mustTrashMetadataPath(t, [][]byte{overBudgetBytes})
	if _, err := lease.MetadataPathFor(overBudget); !errors.Is(err, ErrInvalidAuthority) {
		t.Fatalf("lease.MetadataPathFor(over decoder budget) error = %v, want ErrInvalidAuthority", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("lease.Close() error = %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatalf("second lease.Close() error = %v", err)
	}
	if _, err := lease.DuplicateTrashDescriptorPair(); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("closed TrashLease.DuplicateTrashDescriptorPair() error = %v, want ErrLeaseClosed", err)
	}
	if _, err := lease.MetadataPathFor(mustTrashMetadataPath(t, [][]byte{[]byte("source")})); !errors.Is(err, ErrLeaseClosed) {
		t.Fatalf("closed TrashLease.MetadataPathFor() error = %v, want ErrLeaseClosed", err)
	}
}

func mustTrashMetadataPath(t *testing.T, components [][]byte) pathbytes.BytePath {
	t.Helper()
	path, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	return path
}

func testTrashAuthority(t *testing.T, rootID domain.TrustedRootID, placement TrashPlacement, ownerUID uint32, metadata TrashMetadataMapping) TrashAuthority {
	t.Helper()
	hasSharedTop := placement == TrashPlacementTopShared
	directories := trashTestDirectories(t, hasSharedTop)
	return TrashAuthority{
		Root:      rootID,
		Placement: placement,
		OwnerUID:  ownerUID,
		Metadata:  metadata,
		Open: func() (TrashDescriptors, error) {
			return openTrashTestDescriptors(directories, hasSharedTop)
		},
		Expected: testTrashLayoutExpectation(placement, ownerUID),
	}
}

func testTopologyTrashAuthorityForIdentity(t *testing.T, rootID domain.TrustedRootID, placement TrashPlacement, metadata TrashMetadataMapping) TrashAuthority {
	t.Helper()
	authority := testTrashAuthority(t, rootID, placement, 1000, metadata)
	authority.Topology = TrashTopology{
		AnchorKind: topologyAnchorKindForPlacement(placement),
		Anchor:     testTrashLayoutEvidence(200, 1000, 0o700),
	}
	directories := trashTestDirectories(t, placement == TrashPlacementTopShared)
	authority.Open = func() (TrashDescriptors, error) {
		return openTrashTopologyTestDescriptors(directories, placement == TrashPlacementTopShared)
	}
	return authority
}

func openTopologyTrashLeaseForIdentity(t *testing.T, root *RootLease, authority TrashAuthority) *TrashLease {
	t.Helper()
	inspections := []MountInspection{
		rootInspectionForIdentity(root),
		authority.Topology.Anchor.inspection(),
		authority.Expected.TrashRoot.inspection(),
		authority.Expected.Files.inspection(),
		authority.Expected.Info.inspection(),
	}
	if authority.Placement == TrashPlacementTopShared {
		inspections = append(inspections, authority.Expected.SharedTop.inspection())
	}
	lease, err := openTrustedTrashWith(
		StaticTrashRegistry{root.RootID(): authority},
		root,
		trashInspectionSequence(inspections...),
		func() error { return nil },
	)
	if err != nil {
		t.Fatalf("open topology-qualified Trash lease: %v", err)
	}
	return lease
}

func rootInspectionForIdentity(root *RootLease) MountInspection {
	inspection := testInspection()
	expectation := root.expected
	inspection.Namespace = expectation.Namespace
	inspection.Device = expectation.Device
	inspection.Inode = expectation.Inode
	inspection.UID = expectation.UID
	inspection.GID = expectation.GID
	inspection.Mode = expectation.Mode
	inspection.Mount = expectation.Mount
	inspection.Filesystem.RootDevice = expectation.Device
	inspection.Filesystem.MountDevice = expectation.Mount.Device
	inspection.Filesystem.Filesystem = expectation.Mount.Filesystem
	inspection.Filesystem.MountFilesystem = expectation.Mount.Filesystem
	inspection.Filesystem.MountRoot = expectation.Mount.Root
	return inspection
}

func setTrashAuthorityMountEvidence(authority *TrashAuthority, mount MountRecord) {
	authority.Topology.Anchor.Mount = mount
	authority.Expected.TrashRoot.Mount = mount
	authority.Expected.Files.Mount = mount
	authority.Expected.Info.Mount = mount
	if authority.Expected.HasSharedTop {
		authority.Expected.SharedTop.Mount = mount
	}
}

func topologyAnchorKindForPlacement(placement TrashPlacement) TrashAnchorKind {
	if placement == TrashPlacementHome {
		return TrashAnchorHomeData
	}
	return TrashAnchorFilesystemTop
}

func testTrashLayoutExpectation(placement TrashPlacement, ownerUID uint32) TrashLayoutExpectation {
	expectation := TrashLayoutExpectation{
		TrashRoot: testTrashLayoutEvidence(201, ownerUID, 0o700),
		Files:     testTrashLayoutEvidence(202, ownerUID, 0o700),
		Info:      testTrashLayoutEvidence(203, ownerUID, 0o700),
	}
	if placement == TrashPlacementTopShared {
		expectation.HasSharedTop = true
		expectation.SharedTop = testTrashLayoutEvidence(204, 0, 0o1777)
	}
	return expectation
}

func testTrashLayoutEvidence(inode uint64, uid uint32, mode uint32) LayoutExpectation {
	inspection := testInspection()
	inspection.Inode = inode
	inspection.UID = uid
	inspection.GID = uid
	inspection.Mode = mode
	return LayoutExpectation{
		Namespace: inspection.Namespace,
		Device:    inspection.Device,
		Inode:     inspection.Inode,
		UID:       inspection.UID,
		GID:       inspection.GID,
		Mode:      inspection.Mode,
		Mount:     inspection.Mount,
	}
}

func (expectation LayoutExpectation) inspection() MountInspection {
	inspection := testInspection()
	inspection.Namespace = expectation.Namespace
	inspection.Device = expectation.Device
	inspection.Inode = expectation.Inode
	inspection.UID = expectation.UID
	inspection.GID = expectation.GID
	inspection.Mode = expectation.Mode
	inspection.Mount = expectation.Mount
	inspection.Filesystem.RootDevice = expectation.Device
	inspection.Filesystem.MountDevice = expectation.Mount.Device
	inspection.Filesystem.Filesystem = expectation.Mount.Filesystem
	inspection.Filesystem.MountFilesystem = expectation.Mount.Filesystem
	inspection.Filesystem.MountRoot = expectation.Mount.Root
	return inspection
}

type trashTestDirectorySet struct {
	anchor    string
	trashRoot string
	files     string
	info      string
	sharedTop string
}

func trashTestDirectories(t *testing.T, hasSharedTop bool) trashTestDirectorySet {
	t.Helper()
	directories := trashTestDirectorySet{
		anchor:    t.TempDir(),
		trashRoot: t.TempDir(),
		files:     t.TempDir(),
		info:      t.TempDir(),
	}
	if hasSharedTop {
		directories.sharedTop = t.TempDir()
	}
	return directories
}

func openTrashTestDescriptors(directories trashTestDirectorySet, hasSharedTop bool) (TrashDescriptors, error) {
	return openTrashTestDescriptorsWithTopology(directories, hasSharedTop, false)
}

func openTrashTopologyTestDescriptors(directories trashTestDirectorySet, hasSharedTop bool) (TrashDescriptors, error) {
	return openTrashTestDescriptorsWithTopology(directories, hasSharedTop, true)
}

func openTrashTestDescriptorsWithTopology(directories trashTestDirectorySet, hasSharedTop, includeTopology bool) (TrashDescriptors, error) {
	descriptors := emptyTrashDescriptors()
	var err error
	if includeTopology {
		descriptors.Anchor, err = unix.Open(directories.anchor, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return descriptors, err
		}
	}
	descriptors.TrashRoot, err = unix.Open(directories.trashRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		closeTrashTestDescriptors(descriptors)
		return descriptors, err
	}
	descriptors.Files, err = unix.Open(directories.files, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		closeTrashTestDescriptors(descriptors)
		return TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}, err
	}
	descriptors.Info, err = unix.Open(directories.info, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		closeTrashTestDescriptors(descriptors)
		return TrashDescriptors{TrashRoot: -1, Files: -1, Info: -1, SharedTop: -1}, err
	}
	if hasSharedTop {
		descriptors.SharedTop, err = unix.Open(directories.sharedTop, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			closeTrashTestDescriptors(descriptors)
			return emptyTrashDescriptors(), err
		}
	}
	var normalized TrashDescriptors
	if includeTopology {
		normalized, err = NewTrashTopologyDescriptors(descriptors.Anchor, descriptors.TrashRoot, descriptors.Files, descriptors.Info, descriptors.SharedTop)
	} else {
		normalized, err = NewTrashDescriptors(descriptors.TrashRoot, descriptors.Files, descriptors.Info, descriptors.SharedTop)
	}
	if err != nil {
		return emptyTrashDescriptors(), err
	}
	return normalized, nil
}

func closeTrashTestDescriptors(descriptors TrashDescriptors) {
	for _, fd := range []int{descriptors.Anchor, descriptors.TrashRoot, descriptors.Files, descriptors.Info, descriptors.SharedTop} {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
	}
}

func assertTrashFDClosed(t *testing.T, name string, fd int) {
	t.Helper()
	if fd < 3 {
		t.Fatalf("%s descriptor = %d, want owned descriptor", name, fd)
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("%s descriptor after rejection error = %v, want EBADF", name, err)
	}
}

func trashInspectionSequence(inspections ...MountInspection) rootInspector {
	index := 0
	return func(int) (MountInspection, error) {
		if len(inspections) == 0 {
			return MountInspection{}, ErrDrifted
		}
		if index >= len(inspections) {
			return inspections[len(inspections)-1], nil
		}
		inspection := inspections[index]
		index++
		return inspection, nil
	}
}
