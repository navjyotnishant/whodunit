// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The sync secret — round trip, machine binding, permissions.

package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const password = "hunter2-but-longer"

func TestStoreAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != password {
		t.Fatalf("Load returned %q, stored %q", got, password)
	}
}

// The whole point: what lands on disk must not be the password. A bug that
// wrote plaintext would still pass a round-trip test, which is why this is
// separate.
func TestTheStoredFileDoesNotContainThePassword(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(encPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(password)) {
		t.Fatalf("the password is on disk in plaintext:\n%q", b)
	}
}

// NAV-80: both files owner-only. The encryption is theatre if the keyfile
// beside the ciphertext is world-readable.
func TestBothFilesAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{encPath(dir), keyPath(dir)} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != FileMode {
			t.Errorf("%s has mode %04o, want %04o", filepath.Base(p), mode, FileMode)
		}
	}
	if wide := CheckPermissions(dir); len(wide) != 0 {
		t.Errorf("a correctly written secret was reported as too permissive: %v", wide)
	}
}

// A mode widened after the fact — chmod -R across a home directory is the
// usual way — must be caught, because nothing else in the system would
// notice.
func TestWidenedPermissionsAreReported(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath(dir), 0o644); err != nil {
		t.Fatal(err)
	}

	wide := CheckPermissions(dir)
	if len(wide) != 1 {
		t.Fatalf("got %d findings, want 1: %v", len(wide), wide)
	}
	if !strings.Contains(wide[0], "sync.key") {
		t.Errorf("the widened file was not named: %q", wide[0])
	}
}

// Storing twice must not reuse the nonce. Under GCM a repeated nonce with
// the same key leaks the XOR of the plaintexts and forfeits authentication
// outright — this is the one mistake that silently destroys the cipher.
func TestEveryWriteUsesAFreshNonce(t *testing.T) {
	dir := t.TempDir()

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		if err := Store(dir, password); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(encPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		nonce := string(b[:nonceLen])
		if seen[nonce] {
			t.Fatal("a nonce was reused; GCM is broken under nonce reuse")
		}
		seen[nonce] = true
	}
}

// The ciphertext must be inert away from the machine that wrote it — the
// backup, synced-dotfiles and copied-home cases this exists for.
//
// Simulated by replacing the keyfile, which is the half of the key material
// that a copied directory would not match. The machine id cannot be faked
// in a test without mocking the OS.
func TestACopiedSecretDoesNotDecrypt(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}

	other := make([]byte, keyLen)
	for i := range other {
		other[i] = byte(i)
	}
	if err := writePrivate(keyPath(dir), other); err != nil {
		t.Fatal(err)
	}

	got, err := Load(dir)
	if err == nil {
		t.Fatalf("a secret decrypted under the wrong key material, returning %q", got)
	}
	if !errors.Is(err, ErrWrongMachine) {
		t.Fatalf("got %v, want ErrWrongMachine — the message must point at the "+
			"machine rather than reading as a corrupt password", err)
	}
}

// The machine binding itself, which is the security property NAV-80 asks
// for and the one a keyfile swap does not cover.
//
// Both files are carried across intact — exactly what a restored backup or
// a synced ~/.whodunit looks like — and only the host identity differs. A
// derivation that ignored the machine id would decrypt this happily.
func TestASecretDoesNotDecryptOnAnotherMachine(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}

	restore := machineID
	defer func() { machineID = restore }()
	machineID = func() (string, error) { return "some-other-host-uuid", nil }

	got, err := Load(dir)
	if err == nil {
		t.Fatalf("the secret decrypted on a different machine, returning %q — "+
			"the ciphertext is not bound to this host at all", got)
	}
	if !errors.Is(err, ErrWrongMachine) {
		t.Fatalf("got %v, want ErrWrongMachine", err)
	}
}

// A host with no usable identifier must say so rather than fall back to a
// constant. A silent fallback would strip the machine binding while every
// test and every user-facing message continued to claim it.
func TestAnUnidentifiableMachineRefusesToStore(t *testing.T) {
	restore := machineID
	defer func() { machineID = restore }()
	machineID = func() (string, error) { return "", errors.New("no identifier") }

	if err := Store(t.TempDir(), password); err == nil {
		t.Fatal("a secret was stored on a machine with no identifier; the " +
			"binding would be silently absent")
	}
}

// Tampering must be detected, not silently decrypted to garbage. This is
// what GCM buys over a bare cipher, so it is worth asserting.
func TestTamperedCiphertextIsRejected(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(encPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	b[len(b)-1] ^= 0xff
	if err := writePrivate(encPath(dir), b); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Fatal("a tampered ciphertext was accepted")
	}
}

// "Nothing stored yet" and "stored but unreadable" need different advice,
// so they must not collapse into one error.
func TestNothingStoredIsItsOwnError(t *testing.T) {
	dir := t.TempDir()

	if Stored(dir) {
		t.Error("Stored reported a secret in an empty directory")
	}
	_, err := Load(dir)
	if !errors.Is(err, ErrNotStored) {
		t.Fatalf("got %v, want ErrNotStored", err)
	}
}

// Loading must not create a keyfile. If it did, the fresh random bytes
// would produce a key guaranteed not to work, and the real problem — no
// secret stored — would be reported as a decryption failure.
func TestLoadDoesNotCreateAKeyfile(t *testing.T) {
	dir := t.TempDir()
	_, _ = Load(dir)

	if _, err := os.Stat(keyPath(dir)); !os.IsNotExist(err) {
		t.Fatal("Load created a keyfile; a later Store would then silently " +
			"encrypt under material that was generated during a failed read")
	}
}

func TestDeleteLeavesTheKeyfile(t *testing.T) {
	dir := t.TempDir()
	if err := Store(dir, password); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir); err != nil {
		t.Fatal(err)
	}
	if Stored(dir) {
		t.Error("the secret survived Delete")
	}
	// Deleting an absent secret has achieved what the caller asked for.
	if err := Delete(dir); err != nil {
		t.Errorf("deleting an absent secret errored: %v", err)
	}
}

func TestEmptySecretIsRefused(t *testing.T) {
	if err := Store(t.TempDir(), ""); err == nil {
		t.Fatal("an empty secret was stored; that would read back as " +
			"'no password' and be impossible to distinguish from unset")
	}
}

// The machine identifier has to be stable — a key derived from something
// that changes with the network or the interface would break decryption on
// a machine that had not actually changed.
func TestMachineIDIsStable(t *testing.T) {
	first, err := machineID()
	if err != nil {
		t.Skipf("no machine identifier on this platform: %v", err)
	}
	second, err := machineID()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("machineID is not stable: %q then %q", first, second)
	}
	if strings.TrimSpace(first) == "" {
		t.Fatal("machineID returned blank without an error")
	}
}
