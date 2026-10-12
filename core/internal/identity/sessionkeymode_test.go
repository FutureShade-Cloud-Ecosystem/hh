package identity

import (
	"os"
	"strings"
	"testing"
)

// The private key file must not be readable by group or other.
func TestSigningKeyFileModeIsChecked(t *testing.T) {
	dir := t.TempDir()
	k := freshPriv(t)
	p := writeSEC1(t, dir, "k.pem", k)
	for _, mode := range []os.FileMode{0o600, 0o400} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSessionKeys(SessionKeyFiles{Current: p}, signerIssuer, signerAud); err != nil {
			t.Errorf("mode %o refused: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666, 0o440, 0o404} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		_, err := LoadSessionKeys(SessionKeyFiles{Current: p}, signerIssuer, signerAud)
		if err == nil {
			t.Errorf("mode %o accepted", mode)
			continue
		}
		if strings.Contains(err.Error(), dir) {
			t.Errorf("mode %o: error names the path", mode)
		}
	}
	// Public halves are public: their mode is not checked.
	pub := writePub(t, dir, "pub.pem", &freshPriv(t).PublicKey)
	if err := os.Chmod(pub, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSessionKeys(SessionKeyFiles{Current: p, Next: pub}, signerIssuer, signerAud); err != nil {
		t.Errorf("a 0644 public file refused: %v", err)
	}
}
