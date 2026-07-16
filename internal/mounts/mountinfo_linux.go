//go:build linux

package mounts

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const mountInfoPath = "/proc/self/mountinfo"

// MountRecord is the complete decoded mountinfo evidence used to qualify a
// held root descriptor. MountPoint and Source are drift evidence only; neither
// is reopened or used as apply-time path authority.
type MountRecord struct {
	ID         uint64
	ParentID   uint64
	Device     DeviceIdentity
	Root       string
	MountPoint string
	Filesystem FilesystemType
	Source     string
}

// ParseMountInfo parses Linux /proc/*/mountinfo bytes fail-closed. Linux
// escapes space, tab, newline, and backslash as octal sequences; malformed or
// unknown escapes are rejected rather than normalized.
func ParseMountInfo(input []byte) ([]MountRecord, error) {
	lines := bytes.Split(input, []byte{'\n'})
	records := make([]MountRecord, 0, len(lines))
	seenIDs := make(map[uint64]struct{}, len(lines))
	for lineNumber, line := range lines {
		if len(line) == 0 {
			if lineNumber == len(lines)-1 {
				continue // the conventional final newline
			}
			return nil, fmt.Errorf("%w: mountinfo line %d is empty", ErrInvalidAuthority, lineNumber+1)
		}
		record, err := parseMountInfoLine(string(line))
		if err != nil {
			return nil, fmt.Errorf("%w: mountinfo line %d: %v", ErrInvalidAuthority, lineNumber+1, err)
		}
		if _, duplicate := seenIDs[record.ID]; duplicate {
			return nil, fmt.Errorf("%w: mountinfo line %d repeats mount ID %d", ErrInvalidAuthority, lineNumber+1, record.ID)
		}
		seenIDs[record.ID] = struct{}{}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("%w: mountinfo contains no records", ErrInvalidAuthority)
	}
	return records, nil
}

func parseMountInfoLine(line string) (MountRecord, error) {
	// mountinfo uses ASCII spaces as separators and escapes a space inside a
	// path as \040. Splitting only on that byte preserves arbitrary raw path
	// bytes instead of applying Unicode whitespace normalization.
	fields := strings.Split(line, " ")
	for _, field := range fields {
		if field == "" {
			return MountRecord{}, fmt.Errorf("empty mountinfo field")
		}
	}
	separator := -1
	for index, field := range fields {
		if field == "-" {
			if separator != -1 {
				return MountRecord{}, fmt.Errorf("multiple mountinfo separators")
			}
			separator = index
		}
	}
	if separator < 6 {
		return MountRecord{}, fmt.Errorf("missing or truncated mountinfo separator")
	}
	if len(fields) != separator+4 {
		return MountRecord{}, fmt.Errorf("mountinfo post-separator fields are malformed")
	}

	id, err := parsePositiveUint64("mount ID", fields[0])
	if err != nil {
		return MountRecord{}, err
	}
	parentID, err := parsePositiveUint64("parent mount ID", fields[1])
	if err != nil {
		return MountRecord{}, err
	}
	device, err := parseMountDevice(fields[2])
	if err != nil {
		return MountRecord{}, err
	}
	root, err := decodeMountInfoField(fields[3])
	if err != nil {
		return MountRecord{}, fmt.Errorf("root: %w", err)
	}
	mountPoint, err := decodeMountInfoField(fields[4])
	if err != nil {
		return MountRecord{}, fmt.Errorf("mount point: %w", err)
	}
	// Most mount roots are absolute paths. Pseudo filesystems such as nsfs may
	// legitimately expose an opaque root (for example mnt:[402653...]). Keep
	// parsing the complete table so an unrelated pseudo mount cannot hide the
	// qualified root; CheckSupportedFilesystem rejects any non-/ target root.
	if !strings.HasPrefix(mountPoint, "/") {
		return MountRecord{}, fmt.Errorf("mount point %q is not absolute", mountPoint)
	}

	filesystem := FilesystemType(fields[separator+1])
	if filesystem == "" || strings.ContainsAny(string(filesystem), " \t\n\\") {
		return MountRecord{}, fmt.Errorf("invalid filesystem type %q", filesystem)
	}
	source, err := decodeMountInfoField(fields[separator+2])
	if err != nil {
		return MountRecord{}, fmt.Errorf("source: %w", err)
	}
	if source == "" {
		return MountRecord{}, fmt.Errorf("empty mount source")
	}
	if fields[separator+3] == "" {
		return MountRecord{}, fmt.Errorf("empty super options")
	}

	return MountRecord{
		ID:         id,
		ParentID:   parentID,
		Device:     device,
		Root:       root,
		MountPoint: mountPoint,
		Filesystem: filesystem,
		Source:     source,
	}, nil
}

func parsePositiveUint64(name, value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("invalid %s %q", name, value)
	}
	return parsed, nil
}

func parseMountDevice(value string) (DeviceIdentity, error) {
	majorText, minorText, found := strings.Cut(value, ":")
	if !found || majorText == "" || minorText == "" || strings.Contains(minorText, ":") {
		return DeviceIdentity{}, fmt.Errorf("invalid mount device %q", value)
	}
	major, err := strconv.ParseUint(majorText, 10, 32)
	if err != nil {
		return DeviceIdentity{}, fmt.Errorf("invalid mount device major %q", majorText)
	}
	minor, err := strconv.ParseUint(minorText, 10, 32)
	if err != nil {
		return DeviceIdentity{}, fmt.Errorf("invalid mount device minor %q", minorText)
	}
	return DeviceIdentity{Major: uint32(major), Minor: uint32(minor)}, nil
}

func decodeMountInfoField(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty field")
	}
	decoded := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '\\' {
			if character == 0 {
				return "", fmt.Errorf("NUL byte")
			}
			decoded = append(decoded, character)
			continue
		}
		if index+3 >= len(value) {
			return "", fmt.Errorf("truncated escape")
		}
		escape := value[index+1 : index+4]
		var replacement byte
		switch escape {
		case "040":
			replacement = ' '
		case "011":
			replacement = '\t'
		case "012":
			replacement = '\n'
		case "134":
			replacement = '\\'
		default:
			return "", fmt.Errorf("unsupported escape \\%s", escape)
		}
		decoded = append(decoded, replacement)
		index += 3
	}
	return string(decoded), nil
}

// ReadMountInfo reads the current process mount table. It is intentionally
// scoped to /proc/self rather than any caller-supplied proc root.
func ReadMountInfo() ([]MountRecord, error) {
	contents, err := os.ReadFile(mountInfoPath)
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	return ParseMountInfo(contents)
}

// FindMountRecord finds a current mountinfo record by the mount ID obtained
// from statx on a held descriptor. Its result must still be cross-checked with
// device, filesystem, namespace, and inode evidence.
func FindMountRecord(records []MountRecord, id uint64) (MountRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return MountRecord{}, false
}
