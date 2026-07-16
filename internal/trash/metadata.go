package trash

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

const (
	trashInfoHeader          = "[Trash Info]"
	trashDeletionDateLayout  = "2006-01-02T15:04:05"
	maxTrashInfoBytes        = 1 << 20
	maxTrashTokenBytes       = 245
	minTrashTokenHexBytes    = 16
	trashInfoFilenameSuffix  = ".trashinfo"
	trashTokenRequiredPrefix = "ldc-"
)

// trashPathBasis is selected only by future engine/helper-owned Trash layout
// authority. It controls metadata syntax, not filesystem resolution: the
// metadata path must never be treated as an apply-time pathname.
type trashPathBasis uint8

const (
	trashPathBasisHomeAbsolute trashPathBasis = iota + 1
	trashPathBasisTopDirectoryRelative
)

// trashInfo is an internal, canonical serialization of an LDC-owned
// .trashinfo record. metadataPath is a validated raw-byte path that a future
// authority has already mapped into the selected metadata basis. It is never
// used by this package as filesystem authority, and parsed metadata never
// creates a restore destination. deletedAt is serialized as a user/filesystem
// local wall clock; a parsed value preserves that wall clock but does not
// establish a recovery instant.
type trashInfo struct {
	token        string
	basis        trashPathBasis
	metadataPath pathbytes.BytePath
	deletedAt    time.Time
}

func newTrashInfo(token string, basis trashPathBasis, metadataPath pathbytes.BytePath, deletedAt time.Time) (trashInfo, error) {
	info := trashInfo{
		token:        token,
		basis:        basis,
		metadataPath: metadataPath,
		deletedAt:    deletedAt,
	}
	if err := info.validate(); err != nil {
		return trashInfo{}, err
	}
	return info, nil
}

// marshal produces the exact LDC-owned profile of the Freedesktop Trash
// format. It is pure serialization; durable creation and directory syncing
// belong to a future descriptor-rooted linuxfs operation.
func (info trashInfo) marshal() ([]byte, error) {
	if err := info.validate(); err != nil {
		return nil, err
	}
	encodedPath := pathbytes.PercentEncodeTrashPath(info.metadataPath)
	if encodedPath == "" {
		return nil, fmt.Errorf("trash metadata path cannot be encoded")
	}
	if info.basis == trashPathBasisHomeAbsolute {
		encodedPath = "/" + encodedPath
	}
	contents := trashInfoHeader + "\nPath=" + encodedPath + "\nDeletionDate=" + info.deletedAt.Format(trashDeletionDateLayout) + "\n"
	if len(contents) > maxTrashInfoBytes {
		return nil, fmt.Errorf("trash info exceeds the supported size of %d bytes", maxTrashInfoBytes)
	}
	return []byte(contents), nil
}

// parseTrashInfo recognizes the LDC-owned canonical profile for a known
// token and expected layout-selected basis. Unknown desktop-entry keys are
// ignored and duplicate required keys use their first occurrence, matching the
// Trash specification. The result remains metadata only.
func parseTrashInfo(contents []byte, token string, expectedBasis trashPathBasis) (trashInfo, error) {
	if _, err := trashInfoBasename(token); err != nil {
		return trashInfo{}, err
	}
	if err := expectedBasis.validate(); err != nil {
		return trashInfo{}, err
	}
	if len(contents) == 0 || len(contents) > maxTrashInfoBytes {
		return trashInfo{}, fmt.Errorf("trash info size is outside the supported bound")
	}
	if bytes.IndexByte(contents, 0) >= 0 || bytes.IndexByte(contents, '\r') >= 0 {
		return trashInfo{}, fmt.Errorf("trash info contains an unsafe control byte")
	}

	lines := bytes.Split(contents, []byte{'\n'})
	if len(lines) == 0 || !bytes.Equal(lines[0], []byte(trashInfoHeader)) {
		return trashInfo{}, fmt.Errorf("trash info must begin with %q", trashInfoHeader)
	}

	var pathValue, dateValue []byte
	pathFound := false
	dateFound := false
	for _, line := range lines[1:] {
		if len(line) >= 2 && line[0] == '[' && line[len(line)-1] == ']' {
			break
		}
		switch {
		case !pathFound && bytes.HasPrefix(line, []byte("Path=")):
			pathValue = append([]byte(nil), line[len("Path="):]...)
			pathFound = true
		case !dateFound && bytes.HasPrefix(line, []byte("DeletionDate=")):
			dateValue = append([]byte(nil), line[len("DeletionDate="):]...)
			dateFound = true
		}
	}
	if !pathFound || !dateFound {
		return trashInfo{}, fmt.Errorf("trash info requires Path and DeletionDate")
	}

	metadataPath, err := parseTrashMetadataPath(pathValue, expectedBasis)
	if err != nil {
		return trashInfo{}, err
	}
	deletedAt, err := parseTrashDeletionDate(dateValue)
	if err != nil {
		return trashInfo{}, err
	}
	return newTrashInfo(token, expectedBasis, metadataPath, deletedAt)
}

func (info trashInfo) validate() error {
	if _, err := trashInfoBasename(info.token); err != nil {
		return err
	}
	if err := info.basis.validate(); err != nil {
		return err
	}
	if _, err := pathbytes.New(info.metadataPath.Components()); err != nil {
		return fmt.Errorf("trash metadata path: %w", err)
	}
	encodedPath := pathbytes.PercentEncodeTrashPath(info.metadataPath)
	if _, err := pathbytes.PercentDecodeTrashPath(encodedPath); err != nil {
		return fmt.Errorf("trash metadata path is outside the recoverable decoder profile: %w", err)
	}
	return validateTrashDeletionDate(info.deletedAt)
}

func (basis trashPathBasis) validate() error {
	switch basis {
	case trashPathBasisHomeAbsolute, trashPathBasisTopDirectoryRelative:
		return nil
	default:
		return fmt.Errorf("unknown trash metadata path basis %d", basis)
	}
}

func parseTrashMetadataPath(encoded []byte, basis trashPathBasis) (pathbytes.BytePath, error) {
	if len(encoded) == 0 {
		return pathbytes.BytePath{}, fmt.Errorf("trash Path is empty")
	}
	if err := basis.validate(); err != nil {
		return pathbytes.BytePath{}, err
	}
	pathValue := encoded
	switch basis {
	case trashPathBasisHomeAbsolute:
		if encoded[0] != '/' || len(encoded) == 1 {
			return pathbytes.BytePath{}, fmt.Errorf("home-trash Path must be absolute")
		}
		pathValue = encoded[1:]
	case trashPathBasisTopDirectoryRelative:
		if encoded[0] == '/' {
			return pathbytes.BytePath{}, fmt.Errorf("top-directory Trash Path must be relative")
		}
	}
	decoded, err := pathbytes.PercentDecodeTrashPath(string(pathValue))
	if err != nil {
		return pathbytes.BytePath{}, fmt.Errorf("decode trash Path: %w", err)
	}
	return decoded, nil
}

func validateTrashDeletionDate(deletedAt time.Time) error {
	if deletedAt.IsZero() {
		return fmt.Errorf("trash deletion date is required")
	}
	if deletedAt.Nanosecond() != 0 {
		return fmt.Errorf("trash deletion date must have whole-second precision")
	}
	if deletedAt.Year() < 1 || deletedAt.Year() > 9999 {
		return fmt.Errorf("trash deletion date year is outside the four-digit range")
	}
	return nil
}

func parseTrashDeletionDate(value []byte) (time.Time, error) {
	if len(value) != len(trashDeletionDateLayout) {
		return time.Time{}, fmt.Errorf("trash DeletionDate has an invalid length")
	}
	parsed, err := time.Parse(trashDeletionDateLayout, string(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse trash DeletionDate: %w", err)
	}
	if got := parsed.Format(trashDeletionDateLayout); got != string(value) {
		return time.Time{}, fmt.Errorf("trash DeletionDate is not canonical")
	}
	if err := validateTrashDeletionDate(parsed); err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

// trashInfoBasename gives a known LDC token its required Freedesktop metadata
// filename. It never normalizes a caller string: invalid input cannot select a
// different entry.
func trashInfoBasename(token string) (string, error) {
	if err := validateTrashToken(token); err != nil {
		return "", err
	}
	return token + trashInfoFilenameSuffix, nil
}

func validateTrashToken(token string) error {
	if len(token) == 0 || len(token) > maxTrashTokenBytes {
		return fmt.Errorf("trash token must contain 1 through %d bytes", maxTrashTokenBytes)
	}
	if !strings.HasPrefix(token, trashTokenRequiredPrefix) || len(token)-len(trashTokenRequiredPrefix) < minTrashTokenHexBytes {
		return fmt.Errorf("trash token must begin with %q and contain at least %d hexadecimal bytes", trashTokenRequiredPrefix, minTrashTokenHexBytes)
	}
	for _, value := range []byte(token[len(trashTokenRequiredPrefix):]) {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return fmt.Errorf("trash token contains an unsupported byte %q", value)
		}
	}
	return nil
}
