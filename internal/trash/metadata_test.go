package trash

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/biendo27/linux-deep-clean/internal/pathbytes"
)

func TestTrashInfoMarshalAndParseHomeRawBytes(t *testing.T) {
	metadataPath := mustTrashPath(t, [][]byte{
		[]byte("home"),
		{0xff, '%', 'u', 's', 'e', 'r'},
		[]byte("cache files"),
		{0xfe, 'x'},
	})
	deletedAt := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)

	info, err := newTrashInfo("ldc-0123456789abcdef", trashPathBasisHomeAbsolute, metadataPath, deletedAt)
	if err != nil {
		t.Fatalf("newTrashInfo() error = %v", err)
	}
	contents, err := info.marshal()
	if err != nil {
		t.Fatalf("trashInfo.marshal() error = %v", err)
	}
	want := "[Trash Info]\nPath=/home/%FF%25user/cache%20files/%FEx\nDeletionDate=2026-07-16T12:34:56\n"
	if got := string(contents); got != want {
		t.Fatalf("trash info contents = %q, want %q", got, want)
	}

	parsed, err := parseTrashInfo(contents, "ldc-0123456789abcdef", trashPathBasisHomeAbsolute)
	if err != nil {
		t.Fatalf("parseTrashInfo() error = %v", err)
	}
	if !parsed.metadataPath.Equal(metadataPath) {
		t.Fatalf("parsed metadata path = %x, want %x", parsed.metadataPath.Components(), metadataPath.Components())
	}
	if !parsed.deletedAt.Equal(deletedAt) {
		t.Fatalf("parsed deletion time = %s, want %s", parsed.deletedAt, deletedAt)
	}
	if parsed.basis != trashPathBasisHomeAbsolute {
		t.Fatalf("parsed basis = %d, want home", parsed.basis)
	}
}

func TestTrashInfoMarshalAndParseTopDirectoryRawBytes(t *testing.T) {
	metadataPath := mustTrashPath(t, [][]byte{
		[]byte("project"),
		[]byte("space name"),
		{0xc3, 0xa9, '%'},
	})
	deletedAt := time.Date(2026, time.July, 16, 1, 2, 3, 0, time.UTC)

	info, err := newTrashInfo("ldc-fedcba9876543210", trashPathBasisTopDirectoryRelative, metadataPath, deletedAt)
	if err != nil {
		t.Fatalf("newTrashInfo() error = %v", err)
	}
	contents, err := info.marshal()
	if err != nil {
		t.Fatalf("trashInfo.marshal() error = %v", err)
	}
	want := "[Trash Info]\nPath=project/space%20name/%C3%A9%25\nDeletionDate=2026-07-16T01:02:03\n"
	if got := string(contents); got != want {
		t.Fatalf("trash info contents = %q, want %q", got, want)
	}

	parsed, err := parseTrashInfo(contents, "ldc-fedcba9876543210", trashPathBasisTopDirectoryRelative)
	if err != nil {
		t.Fatalf("parseTrashInfo() error = %v", err)
	}
	if !parsed.metadataPath.Equal(metadataPath) {
		t.Fatalf("parsed metadata path = %x, want %x", parsed.metadataPath.Components(), metadataPath.Components())
	}
}

func TestTrashInfoUsesLocalWallClockDeletionDate(t *testing.T) {
	metadataPath := mustTrashPath(t, [][]byte{[]byte("project"), []byte("item")})
	local := time.Date(2026, time.July, 16, 19, 20, 21, 0, time.FixedZone("filesystem", 7*60*60))

	info, err := newTrashInfo("ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative, metadataPath, local)
	if err != nil {
		t.Fatalf("newTrashInfo() local timestamp error = %v", err)
	}
	contents, err := info.marshal()
	if err != nil {
		t.Fatalf("trashInfo.marshal() local timestamp error = %v", err)
	}
	if got, want := string(contents), "[Trash Info]\nPath=project/item\nDeletionDate=2026-07-16T19:20:21\n"; got != want {
		t.Fatalf("local Trash date contents = %q, want %q", got, want)
	}
	parsed, err := parseTrashInfo(contents, "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative)
	if err != nil {
		t.Fatalf("parseTrashInfo() local timestamp error = %v", err)
	}
	if got, want := parsed.deletedAt.Format(trashDeletionDateLayout), "2026-07-16T19:20:21"; got != want {
		t.Fatalf("parsed local wall-clock date = %q, want %q", got, want)
	}
}

func TestTrashInfoMarshalRejectsRecordBeyondParserBound(t *testing.T) {
	metadataPath := mustTrashPath(t, [][]byte{bytes.Repeat([]byte("a"), maxTrashInfoBytes)})
	info := trashInfo{
		token:        "ldc-0123456789abcdef",
		basis:        trashPathBasisTopDirectoryRelative,
		metadataPath: metadataPath,
		deletedAt:    time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC),
	}

	if _, err := info.marshal(); err == nil {
		t.Fatal("trashInfo.marshal() accepted a record its parser would reject for size")
	}
}

func TestParseTrashInfoRejectsBasisMismatchAndEscapingTraversal(t *testing.T) {
	validDate := "2026-07-16T12:34:56"
	for _, test := range []struct {
		name  string
		basis trashPathBasis
		path  string
	}{
		{name: "home requires absolute path", basis: trashPathBasisHomeAbsolute, path: "home/user/file"},
		{name: "top requires relative path", basis: trashPathBasisTopDirectoryRelative, path: "/home/user/file"},
		{name: "literal parent traversal", basis: trashPathBasisTopDirectoryRelative, path: "safe/../file"},
		{name: "escaped parent traversal", basis: trashPathBasisTopDirectoryRelative, path: "safe/%2E%2E/file"},
		{name: "escaped slash", basis: trashPathBasisTopDirectoryRelative, path: "safe%2Ffile"},
		{name: "lowercase escape", basis: trashPathBasisTopDirectoryRelative, path: "safe%2ffile"},
		{name: "empty component", basis: trashPathBasisTopDirectoryRelative, path: "safe//file"},
		{name: "raw non ascii", basis: trashPathBasisTopDirectoryRelative, path: string([]byte{'s', 0xff})},
		{name: "NUL escape", basis: trashPathBasisTopDirectoryRelative, path: "safe%00file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			contents := "[Trash Info]\nPath=" + test.path + "\nDeletionDate=" + validDate + "\n"
			if _, err := parseTrashInfo([]byte(contents), "ldc-0123456789abcdef", test.basis); err == nil {
				t.Fatalf("parseTrashInfo() accepted unsafe Path=%q", test.path)
			}
		})
	}
}

func TestParseTrashInfoUsesFirstRequiredField(t *testing.T) {
	contents := []byte("[Trash Info]\nUnknown=ignored\nPath=first/path\nPath=second/path\nDeletionDate=2026-07-16T12:34:56\nDeletionDate=2026-07-17T01:02:03\n")
	parsed, err := parseTrashInfo(contents, "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative)
	if err != nil {
		t.Fatalf("parseTrashInfo() error = %v", err)
	}
	wantPath := mustTrashPath(t, [][]byte{[]byte("first"), []byte("path")})
	if !parsed.metadataPath.Equal(wantPath) {
		t.Fatalf("parsed first Path = %x, want %x", parsed.metadataPath.Components(), wantPath.Components())
	}
	if got, want := parsed.deletedAt.Format(trashDeletionDateLayout), "2026-07-16T12:34:56"; got != want {
		t.Fatalf("parsed first DeletionDate = %q, want %q", got, want)
	}

	invalidFirst := []byte("[Trash Info]\nPath=../unsafe\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n")
	if _, err := parseTrashInfo(invalidFirst, "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative); err == nil {
		t.Fatal("parseTrashInfo() allowed a later Path= to rescue an invalid first value")
	}
}

func TestParseTrashInfoDoesNotReadFieldsFromLaterDesktopEntryGroups(t *testing.T) {
	contents := []byte("[Trash Info]\nUnknown=ignored\n[Other]\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n")
	if _, err := parseTrashInfo(contents, "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative); err == nil {
		t.Fatal("parseTrashInfo() accepted required fields from a later desktop-entry group")
	}
}

func TestTrashInfoRejectsMalformedHeaderAndRequiredFields(t *testing.T) {
	valid := "[Trash Info]\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n"
	tests := []struct {
		name     string
		contents string
	}{
		{name: "missing header", contents: strings.TrimPrefix(valid, "[Trash Info]\n")},
		{name: "wrong header case", contents: strings.Replace(valid, "[Trash Info]", "[Trash info]", 1)},
		{name: "header is not first", contents: "Unknown=value\n" + valid},
		{name: "missing path", contents: "[Trash Info]\nDeletionDate=2026-07-16T12:34:56\n"},
		{name: "missing deletion date", contents: "[Trash Info]\nPath=safe/path\n"},
		{name: "wrong path case", contents: strings.Replace(valid, "Path=", "path=", 1)},
		{name: "wrong deletion date case", contents: strings.Replace(valid, "DeletionDate=", "deletiondate=", 1)},
		{name: "CRLF injection", contents: strings.Replace(valid, "\n", "\r\n", 1)},
		{name: "NUL injection", contents: strings.Replace(valid, "safe/path", "safe\x00path", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseTrashInfo([]byte(test.contents), "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative); err == nil {
				t.Fatal("parseTrashInfo() accepted malformed metadata")
			}
		})
	}
}

func TestTrashInfoRejectsUnrepresentableDeletionDate(t *testing.T) {
	metadataPath := mustTrashPath(t, [][]byte{[]byte("safe"), []byte("path")})
	for _, test := range []struct {
		name string
		at   time.Time
	}{
		{name: "zero", at: time.Time{}},
		{name: "fractional seconds", at: time.Date(2026, time.July, 16, 12, 34, 56, 1, time.UTC)},
		{name: "year zero", at: time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "five digit year", at: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newTrashInfo("ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative, metadataPath, test.at); err == nil {
				t.Fatal("newTrashInfo() accepted an unrepresentable deletion time")
			}
		})
	}

	for _, value := range []string{
		"2026-07-16T12:34:56Z",
		"2026-07-16T12:34:56.001",
		"2026-02-30T12:34:56",
		"0000-01-01T00:00:00",
		"10000-01-01T00:00:00",
	} {
		contents := "[Trash Info]\nPath=safe/path\nDeletionDate=" + value + "\n"
		if _, err := parseTrashInfo([]byte(contents), "ldc-0123456789abcdef", trashPathBasisTopDirectoryRelative); err == nil {
			t.Fatalf("parseTrashInfo() accepted deletion date %q", value)
		}
	}
}

func TestTrashInfoTokenAndFilenameFailClosed(t *testing.T) {
	for _, token := range []string{
		"",
		".",
		"..",
		"ldc-",  // no opaque random suffix
		"ldc-a", // insufficient entropy for an owned recovery token
		"ldc-UPPER",
		"ldc-0123/4567",
		"ldc-0123\x004567",
		"ldc-0123.trashinfo",
		"ldc-0123\n4567",
		"ldc-0123456789abcdeg",
		"ldc-" + strings.Repeat("a", maxTrashTokenBytes),
	} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(token, "\x00", "nul"), "/", "slash"), func(t *testing.T) {
			if _, err := trashInfoBasename(token); err == nil {
				t.Fatalf("trashInfoBasename(%q) accepted an invalid token", token)
			}
		})
	}

	if got, err := trashInfoBasename("ldc-0123456789abcdef"); err != nil || got != "ldc-0123456789abcdef.trashinfo" {
		t.Fatalf("trashInfoBasename() = %q, %v; want exact .trashinfo basename", got, err)
	}
}

func TestTrashInfoRejectsInvalidTrustedInputsBeforeSerialization(t *testing.T) {
	validPath := mustTrashPath(t, [][]byte{[]byte("safe"), []byte("path")})
	validDate := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)
	for _, test := range []struct {
		name string
		info trashInfo
	}{
		{
			name: "invalid token",
			info: trashInfo{token: "unowned", basis: trashPathBasisTopDirectoryRelative, metadataPath: validPath, deletedAt: validDate},
		},
		{
			name: "invalid basis",
			info: trashInfo{token: "ldc-0123456789abcdef", basis: trashPathBasis(99), metadataPath: validPath, deletedAt: validDate},
		},
		{
			name: "invalid path",
			info: trashInfo{token: "ldc-0123456789abcdef", basis: trashPathBasisTopDirectoryRelative, deletedAt: validDate},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.info.marshal(); err == nil {
				t.Fatal("trashInfo.marshal() accepted an invalid metadata record")
			}
		})
	}
}

func TestTrashInfoParserRejectsInvalidCallInputsAndBounds(t *testing.T) {
	valid := []byte("[Trash Info]\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n")
	for _, test := range []struct {
		name     string
		contents []byte
		token    string
		basis    trashPathBasis
	}{
		{name: "invalid token", contents: valid, token: "unowned", basis: trashPathBasisTopDirectoryRelative},
		{name: "invalid basis", contents: valid, token: "ldc-0123456789abcdef", basis: trashPathBasis(99)},
		{name: "empty record", contents: nil, token: "ldc-0123456789abcdef", basis: trashPathBasisTopDirectoryRelative},
		{name: "oversize record", contents: bytes.Repeat([]byte("x"), maxTrashInfoBytes+1), token: "ldc-0123456789abcdef", basis: trashPathBasisTopDirectoryRelative},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseTrashInfo(test.contents, test.token, test.basis); err == nil {
				t.Fatal("parseTrashInfo() accepted invalid call input")
			}
		})
	}
}

func TestParseTrashMetadataPathRejectsEmptyAndUnknownBasis(t *testing.T) {
	if _, err := parseTrashMetadataPath(nil, trashPathBasisTopDirectoryRelative); err == nil {
		t.Fatal("parseTrashMetadataPath() accepted an empty path")
	}
	if _, err := parseTrashMetadataPath([]byte("safe/path"), trashPathBasis(99)); err == nil {
		t.Fatal("parseTrashMetadataPath() accepted an unknown path basis")
	}
}

func TestNewTrashInfoAcceptsOnlyRecoverablePathBudgets(t *testing.T) {
	limits := pathbytes.DefaultTrashDecodeLimits()
	deletedAt := time.Date(2026, time.July, 16, 12, 34, 56, 0, time.UTC)

	maxComponents := make([][]byte, limits.MaxComponents)
	for index := range maxComponents {
		maxComponents[index] = []byte("a")
	}
	if _, err := newTrashInfo(
		"ldc-0123456789abcdef",
		trashPathBasisTopDirectoryRelative,
		mustTrashPath(t, maxComponents),
		deletedAt,
	); err != nil {
		t.Fatalf("newTrashInfo() rejected the exact component limit: %v", err)
	}

	maxEncoded := mustTrashPath(t, [][]byte{[]byte(strings.Repeat("a", limits.MaxInputBytes))})
	if _, err := newTrashInfo(
		"ldc-0123456789abcdef",
		trashPathBasisTopDirectoryRelative,
		maxEncoded,
		deletedAt,
	); err != nil {
		t.Fatalf("newTrashInfo() rejected the exact encoded-input limit: %v", err)
	}

	tests := []struct {
		name string
		path pathbytes.BytePath
	}{
		{
			name: "too many components",
			path: mustTrashPath(t, append(maxComponents, []byte("a"))),
		},
		{
			name: "percent encoding expands past input limit",
			path: mustTrashPath(t, [][]byte{bytes.Repeat([]byte{0xff}, limits.MaxInputBytes/3+1)}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newTrashInfo(
				"ldc-0123456789abcdef",
				trashPathBasisTopDirectoryRelative,
				test.path,
				deletedAt,
			); err == nil {
				t.Fatal("newTrashInfo() accepted a path it could not later parse for recovery")
			}
		})
	}
}

func FuzzParseTrashInfo(f *testing.F) {
	f.Add([]byte("[Trash Info]\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n"), "ldc-0123456789abcdef", uint8(trashPathBasisTopDirectoryRelative))
	f.Add([]byte("[Trash Info]\n[Other]\nPath=safe/path\nDeletionDate=2026-07-16T12:34:56\n"), "ldc-0123456789abcdef", uint8(trashPathBasisTopDirectoryRelative))
	f.Add([]byte{0, '[', 'T', 'r', 'a', 's', 'h', ' ', 'I', 'n', 'f', 'o', ']'}, "ldc-0123456789abcdef", uint8(trashPathBasisHomeAbsolute))

	f.Fuzz(func(t *testing.T, contents []byte, token string, basis uint8) {
		_, _ = parseTrashInfo(contents, token, trashPathBasis(basis))
	})
}

func mustTrashPath(t *testing.T, components [][]byte) pathbytes.BytePath {
	t.Helper()
	path, err := pathbytes.New(components)
	if err != nil {
		t.Fatalf("pathbytes.New() error = %v", err)
	}
	return path
}
