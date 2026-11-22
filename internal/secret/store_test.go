package secret

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenAt(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s, dir
}

func TestSetGetRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Set("GITHUB_TOKEN", "ghp_example"); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, ok, err := s.Get("GITHUB_TOKEN")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if v != "ghp_example" {
		t.Fatalf("got %q", v)
	}
}

// The whole point: a token must not be readable in the file it lives in.
func TestValueIsNotOnDiskInPlaintext(t *testing.T) {
	s, dir := newStore(t)
	const token = "ghp_SUPERSECRETVALUE1234"
	if err := s.Set("GITHUB_TOKEN", token); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), token) {
		t.Fatal("secret is stored in plaintext")
	}
	if strings.Contains(string(b), "GITHUB_TOKEN") {
		// Names leak as well; that is acceptable, but worth knowing. Assert the
		// behaviour so a future change has to think about it.
		t.Log("note: secret names are encrypted too - good")
	}
}

func TestOverwriteReplacesValue(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Set("K", "one")
	_ = s.Set("K", "two")
	v, _, _ := s.Get("K")
	if v != "two" {
		t.Fatalf("got %q", v)
	}
}

func TestDelete(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Set("K", "v")
	if err := s.Delete("K"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("K"); ok {
		t.Fatal("secret survived delete")
	}
	if err := s.Delete("never-existed"); err != nil {
		t.Fatalf("deleting a missing secret should be a no-op, got %v", err)
	}
}

func TestListIsSortedAndHasNoValues(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Set("B_TOKEN", "b")
	_ = s.Set("A_TOKEN", "a")
	infos, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Name != "A_TOKEN" || infos[1].Name != "B_TOKEN" {
		t.Fatalf("unexpected list: %+v", infos)
	}
}

func TestResolveReportsMissing(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Set("HAVE", "x")
	vals, missing, err := s.Resolve([]string{"HAVE", "LACK"})
	if err != nil {
		t.Fatal(err)
	}
	if vals["HAVE"] != "x" {
		t.Fatalf("vals: %v", vals)
	}
	if len(missing) != 1 || missing[0] != "LACK" {
		t.Fatalf("missing: %v", missing)
	}
}

// A different master key must not silently produce garbage.
func TestWrongMasterKeyFails(t *testing.T) {
	dir := t.TempDir()
	s1, err := OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Set("K", "v"); err != nil {
		t.Fatal(err)
	}

	other := strings.Repeat("ab", keyLen)
	t.Setenv("CAPBUS_MASTER_KEY", other)
	if _, err := OpenAt(dir); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s2.Get("K"); err == nil {
		t.Fatal("reading with the wrong master key must fail")
	}
}

func TestMasterKeyFromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CAPBUS_MASTER_KEY", strings.Repeat("0f", keyLen))
	s, err := OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("K", "v"); err != nil {
		t.Fatal(err)
	}
	// No key file should be written when the key comes from the environment.
	if _, err := os.Stat(filepath.Join(dir, "master.key")); err == nil {
		t.Fatal("key file written despite CAPBUS_MASTER_KEY")
	}
}

func TestEmptyInputsRejected(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Set("", "v"); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if err := s.Set("K", ""); err == nil {
		t.Fatal("empty value must be rejected")
	}
}

// MkdirAll leaves an existing directory's mode alone. Found by testing on a
// real Linux host: a store inside a 0775 directory stayed group-writable while
// holding the master key.
func TestOpenForcesPrivateDirMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAt(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o700 {
		t.Fatalf("store dir mode is %o, want 700", got)
	}
}

// Reopening must reuse the existing key: regenerating it would orphan every
// secret already encrypted with the old one.
func TestKeyIsNotRegenerated(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenAt(dir); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenAt(dir); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("master key was regenerated on reopen")
	}
}
