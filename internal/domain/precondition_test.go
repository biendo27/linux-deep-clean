package domain

import "testing"

func TestFilesystemPreconditionRequiresNamedMask(t *testing.T) {
	target := testFilesystemTarget(t)
	valid := testFilesystemPrecondition(t, target)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid precondition error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*FilesystemPrecondition)
	}{
		{
			name: "empty mask",
			mutate: func(precondition *FilesystemPrecondition) {
				precondition.Required = 0
			},
		},
		{
			name: "unknown mask bit",
			mutate: func(precondition *FilesystemPrecondition) {
				precondition.Required |= FilesystemFieldMask(1 << 31)
			},
		},
		{
			name: "required inode unavailable",
			mutate: func(precondition *FilesystemPrecondition) {
				precondition.Snapshot.Inode.Known = false
			},
		},
		{
			name: "required type unavailable",
			mutate: func(precondition *FilesystemPrecondition) {
				precondition.Snapshot.Type = FileTypeUnknown
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := valid
			candidate.Filesystem = &FilesystemPrecondition{}
			*candidate.Filesystem = *valid.Filesystem
			tt.mutate(candidate.Filesystem)
			if err := candidate.Validate(); err == nil {
				t.Fatal("Precondition.Validate() error = nil, want error")
			}
		})
	}
}

func TestClosedEvidenceAndTargetVariantsRejectMismatch(t *testing.T) {
	target := testFilesystemTarget(t)
	evidence := testFilesystemEvidence(t, target)
	evidence.Kind = EvidenceKind("future_evidence")
	if err := evidence.Validate(); err == nil {
		t.Fatal("unknown evidence kind accepted")
	}

	precondition := testFilesystemPrecondition(t, target)
	precondition.Kind = PreconditionManagerObjectState
	if err := precondition.Validate(); err == nil {
		t.Fatal("mismatched precondition kind accepted")
	}
}
