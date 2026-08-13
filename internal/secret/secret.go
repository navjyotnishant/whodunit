// Author: Navjyot Nishant
// Created: 2026-08-13
// Last updated: 2026-08-13
// Description: The sync password, encrypted at rest with AES-256-GCM under
// a machine-derived key.

// Package secret stores the sync password on disk instead of in a shell
// profile.
//
// # Why this exists
//
// whodunit never wrote the password to config.json — it stored only the
// name of an environment variable. That kept the config file clean, but it
// was the only option, and a pre-push hook needs the variable exported in a
// login shell. The one practical way to get that is ~/.zshrc, so the design
// pushed people toward a plaintext credential in a file that lands in
// backups, in synced dotfiles, and in dotfile repositories on GitHub.
//
// # What protects it
//
// Two files, both owner-only, in the whodunit home:
//
//	sync.enc   the AES-256-GCM ciphertext, with its nonce
//	sync.key   32 random bytes
//
// The encryption key is HKDF-SHA256 over the machine identifier and the
// keyfile bytes together. The machine id binds the ciphertext to this host;
// the keyfile is the part an attacker holding only the ciphertext lacks.
//
// Neither ingredient is sufficient alone, which is the point. The machine
// id is not a secret — macOS hands out IOPlatformUUID to anyone who asks,
// and /etc/machine-id is world-readable on most Linux distributions — so a
// key derived from it alone would be reproducible by anyone who read the
// ciphertext. The keyfile is secret but not bound to a machine, so a key
// derived from it alone would travel with a copied directory.
//
// # What this does not protect against
//
// A process already running as you. It can read sync.key, read sync.enc,
// and decrypt. Encryption at rest is not a sandbox.
//
// What it does protect against is the file leaving the machine: a Time
// Machine backup, a synced ~/.config, a dotfile repository, a stolen disk
// image. On any of those the ciphertext is inert, because the machine id it
// was bound to is elsewhere. That is a real and common exposure, and it is
// the one a shell profile is worst at.
//
// # Why not a passphrase, and why not a keyring
//
// A passphrase-derived key is stronger — it protects against local reads
// too — but it needs a prompt, and this is read from a pre-push hook. A
// hook that blocks on a password prompt is a hook people disable, and a
// disabled hook protects nothing.
//
// An OS keyring (Keychain, Secret Service, DPAPI) is three platform
// backends and a dependency, and it does not defend against a local process
// running as you either — the same gap this has. It buys little over this
// for the cost, and Linux headless has no keyring at all.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// FileMode is the permission both files must carry. Exported because
	// `dun verify` checks it: a mode widened by a careless chmod -R is
	// exactly the silent regression this package exists to prevent.
	FileMode fs.FileMode = 0o600

	keyLen   = 32 // AES-256
	nonceLen = 12 // GCM standard nonce size
)

// ErrNotStored means no secret has been saved. Distinct from a decryption
// failure so callers can tell "nothing here yet" from "something is wrong",
// which need different advice.
var ErrNotStored = errors.New("no secret is stored")

// ErrWrongMachine means the ciphertext exists but was encrypted under a
// different machine's identity.
//
// Worth its own error because the alternative message — GCM's
// authentication failure — reads as "the stored password is corrupt" and
// sends someone looking in the wrong place. The usual cause is a restored
// backup or a copied home directory, and the fix is to set the password
// again on this machine.
var ErrWrongMachine = errors.New("the stored secret was encrypted on a different machine")

// Store encrypts value and writes it under dir.
func Store(dir, value string) error {
	if value == "" {
		return errors.New("refusing to store an empty secret")
	}
	key, err := deriveKey(dir, true)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}

	// A fresh nonce per write, from crypto/rand. Reusing a nonce under the
	// same key breaks GCM outright — it leaks the XOR of the plaintexts and
	// forfeits authentication — so this must never be a counter, a
	// timestamp, or a constant.
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(value), nil)
	return writePrivate(encPath(dir), sealed)
}

// Load decrypts the stored secret.
func Load(dir string) (string, error) {
	sealed, err := os.ReadFile(encPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotStored
		}
		return "", err
	}
	if len(sealed) < nonceLen {
		return "", fmt.Errorf("stored secret is truncated")
	}

	// deriveKey does not create a keyfile here: without one there is
	// nothing that could have encrypted this, and generating fresh random
	// bytes would produce a key guaranteed not to work, reported as a
	// decryption failure rather than as the missing file it is.
	key, err := deriveKey(dir, false)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}

	plain, err := gcm.Open(nil, sealed[:nonceLen], sealed[nonceLen:], nil)
	if err != nil {
		// GCM cannot distinguish a wrong key from tampering, and the
		// overwhelmingly likely cause of a wrong key here is a home
		// directory restored from backup onto another machine.
		return "", ErrWrongMachine
	}
	return string(plain), nil
}

// Delete removes the stored secret, leaving the keyfile alone so a
// subsequent Store does not invalidate anything else derived from it.
// Returns nil when nothing was stored: deleting an absent secret has
// achieved what the caller asked for.
func Delete(dir string) error {
	err := os.Remove(encPath(dir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Stored reports whether a secret exists, without decrypting it. Used by
// status output that should not need the key.
func Stored(dir string) bool {
	_, err := os.Stat(encPath(dir))
	return err == nil
}

// CheckPermissions reports any stored file whose mode is wider than
// FileMode, as "<name>: <mode>" strings.
//
// A correctly encrypted secret under a world-readable keyfile is not
// protected, and nothing else in the system would notice — `chmod -R 755`
// on a home directory is the usual way it happens.
func CheckPermissions(dir string) []string {
	var wide []string
	for _, p := range []string{encPath(dir), keyPath(dir)} {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if mode := info.Mode().Perm(); mode&^FileMode != 0 {
			wide = append(wide, fmt.Sprintf("%s: %04o", filepath.Base(p), mode))
		}
	}
	return wide
}

func encPath(dir string) string { return filepath.Join(dir, "sync.enc") }
func keyPath(dir string) string { return filepath.Join(dir, "sync.key") }

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// deriveKey builds the AES key from the machine identity and the keyfile.
//
// create says whether a missing keyfile should be generated: true when
// storing, false when loading.
func deriveKey(dir string, create bool) ([]byte, error) {
	material, err := keyfile(dir, create)
	if err != nil {
		return nil, err
	}
	id, err := machineID()
	if err != nil {
		return nil, err
	}

	// The machine id is the salt rather than part of the secret: it is not
	// confidential, and HKDF's contract is that the salt need not be. The
	// keyfile is the input keying material, being the part that is
	// genuinely secret.
	return hkdf.Key(sha256.New, material, []byte(id), "whodunit sync secret v1", keyLen)
}

// keyfile reads the random half of the key material, creating it on first
// use when create is set.
func keyfile(dir string, create bool) ([]byte, error) {
	b, err := os.ReadFile(keyPath(dir))
	switch {
	case err == nil:
		if len(b) != keyLen {
			return nil, fmt.Errorf("keyfile is %d bytes, expected %d", len(b), keyLen)
		}
		return b, nil
	case !os.IsNotExist(err):
		return nil, err
	case !create:
		return nil, ErrNotStored
	}

	b = make([]byte, keyLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate key material: %w", err)
	}
	if err := writePrivate(keyPath(dir), b); err != nil {
		return nil, err
	}
	return b, nil
}

// writePrivate writes owner-only, replacing any existing file.
//
// The mode is set explicitly after creation rather than trusted to
// OpenFile: an existing file keeps its own permissions, so a file that was
// once world-readable would silently stay that way.
func writePrivate(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, FileMode); err != nil {
		return err
	}
	return os.Chmod(path, FileMode)
}

// machineID is a variable so a test can substitute another machine's
// identity. Without that, the property this package exists for — a
// ciphertext that does not travel — cannot be tested at all: dropping the
// machine id from the derivation would leave every test passing.
var machineID = realMachineID

// realMachineID returns a stable identifier for this host.
//
// Deliberately not a hostname or a MAC address: hostnames change with a
// network, and MAC addresses vary per interface and are randomized by
// modern operating systems. Both would break decryption on a machine that
// had not actually changed.
func realMachineID() (string, error) {
	var id string

	switch runtime.GOOS {
	case "darwin":
		// An absolute path, not a bare name. A hook runs with whatever
		// PATH git hands it, which on a minimal environment need not
		// include /usr/sbin — and a lookup failure here would report "no
		// machine identifier" on a machine that plainly has one.
		out, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			out, err = exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		}
		if err == nil {
			id = between(string(out), `"IOPlatformUUID" = "`, `"`)
		}
	case "windows":
		// MachineGuid is written at install time and survives hardware
		// changes, unlike anything WMI reports about the motherboard.
		reg := filepath.Join(os.Getenv("SystemRoot"), "System32", "reg.exe")
		if os.Getenv("SystemRoot") == "" {
			reg = "reg"
		}
		out, err := exec.Command(reg, "query",
			`HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
		if err == nil {
			if i := strings.LastIndex(string(out), "REG_SZ"); i >= 0 {
				id = strings.TrimSpace(string(out)[i+len("REG_SZ"):])
			}
		}
	default:
		// /etc/machine-id is the systemd standard; the D-Bus file predates
		// it and is still all that exists on some systems.
		for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if b, err := os.ReadFile(p); err == nil {
				id = strings.TrimSpace(string(b))
				break
			}
		}
	}

	if id = strings.TrimSpace(id); id == "" {
		// No fallback to a random or constant value. A random one would
		// silently re-key on every run, and a constant one would strip the
		// machine binding without saying so — a security property quietly
		// removed is worse than a feature that reports it cannot work.
		return "", fmt.Errorf("cannot determine a machine identifier on %s; "+
			"set the password through an environment variable instead", runtime.GOOS)
	}
	return id, nil
}

// between returns the text between the first open and the next close.
func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
