//go:build linux

package mounts

import (
	"errors"
	"testing"
)

func TestParseMountInfoDecodesEscapedFields(t *testing.T) {
	input := []byte("42 1 8:2 /root\\040with\\134slash /mnt\\040data rw,relatime shared:7 - ext4 /dev/disk\\040name rw,data=ordered\\n")

	records, err := ParseMountInfo(input)
	if err != nil {
		t.Fatalf("ParseMountInfo() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}

	record := records[0]
	if record.ID != 42 || record.ParentID != 1 {
		t.Fatalf("record IDs = (%d, %d), want (42, 1)", record.ID, record.ParentID)
	}
	if record.Device != (DeviceIdentity{Major: 8, Minor: 2}) {
		t.Fatalf("record Device = %#v, want 8:2", record.Device)
	}
	if record.Root != "/root with\\slash" {
		t.Fatalf("record Root = %q", record.Root)
	}
	if record.MountPoint != "/mnt data" {
		t.Fatalf("record MountPoint = %q", record.MountPoint)
	}
	if record.Filesystem != FilesystemExt4 {
		t.Fatalf("record Filesystem = %q, want %q", record.Filesystem, FilesystemExt4)
	}
	if record.Source != "/dev/disk name" {
		t.Fatalf("record Source = %q", record.Source)
	}
}

func TestParseMountInfoRejectsMalformedInput(t *testing.T) {
	tests := map[string][]byte{
		"empty":                   nil,
		"embedded empty line":     []byte("42 1 8:2 / / rw - ext4 /dev/sda rw\n\n43 1 8:3 / /other rw - ext4 /dev/sdb rw\n"),
		"multiple field spaces":   []byte("42  1 8:2 / / rw - ext4 /dev/sda rw\n"),
		"missing separator":       []byte("42 1 8:2 / / rw,relatime ext4 /dev/sda rw\n"),
		"multiple separators":     []byte("42 1 8:2 / / rw - ext4 /dev/sda rw - extra\n"),
		"missing post separator":  []byte("42 1 8:2 / / rw,relatime - ext4\n"),
		"non numeric mount ID":    []byte("nope 1 8:2 / / rw - ext4 /dev/sda rw\n"),
		"zero mount ID":           []byte("0 1 8:2 / / rw - ext4 /dev/sda rw\n"),
		"zero parent ID":          []byte("42 0 8:2 / / rw - ext4 /dev/sda rw\n"),
		"duplicate mount ID":      []byte("42 1 8:2 / / rw - ext4 /dev/sda rw\n42 1 8:3 / /other rw - ext4 /dev/sdb rw\n"),
		"bad device":              []byte("42 1 eight:2 / / rw - ext4 /dev/sda rw\n"),
		"bad device minor":        []byte("42 1 8:nope / / rw - ext4 /dev/sda rw\n"),
		"bad escape short":        []byte("42 1 8:2 /bad\\04 / rw - ext4 /dev/sda rw\n"),
		"bad escape unknown":      []byte("42 1 8:2 /bad\\041 / rw - ext4 /dev/sda rw\n"),
		"bad mountpoint escape":   []byte("42 1 8:2 / /bad\\04 rw - ext4 /dev/sda rw\n"),
		"mountpoint not absolute": []byte("42 1 8:2 / relative rw - ext4 /dev/sda rw\n"),
		"invalid filesystem":      []byte("42 1 8:2 / / rw - ext\\1344 /dev/sda rw\n"),
		"bad source escape":       []byte("42 1 8:2 / / rw - ext4 /dev\\04 rw\n"),
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMountInfo(input)
			if err == nil {
				t.Fatal("ParseMountInfo() error = nil, want malformed-input failure")
			}
			if !errors.Is(err, ErrInvalidAuthority) {
				t.Fatalf("ParseMountInfo() error = %v, want ErrInvalidAuthority", err)
			}
		})
	}
}

func TestParseMountInfoPreservesOpaquePseudoFilesystemRoot(t *testing.T) {
	records, err := ParseMountInfo([]byte("42 1 0:4 mnt:[4026534080] /run/ns rw - nsfs nsfs rw\n"))
	if err != nil {
		t.Fatalf("ParseMountInfo() error = %v", err)
	}
	if records[0].Root != "mnt:[4026534080]" {
		t.Fatalf("record root = %q", records[0].Root)
	}
}

func TestFindMountRecord(t *testing.T) {
	records := []MountRecord{{ID: 4}, {ID: 7}}

	found, ok := FindMountRecord(records, 7)
	if !ok || found.ID != 7 {
		t.Fatalf("FindMountRecord() = (%#v, %t), want record 7", found, ok)
	}
	if _, ok := FindMountRecord(records, 8); ok {
		t.Fatal("FindMountRecord() found an absent mount ID")
	}
}
