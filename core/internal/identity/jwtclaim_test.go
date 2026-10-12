package identity

import (
	"testing"
	"time"
)

func TestJWTClaimBoundFloor(t *testing.T) {
	if MinJWTClaimBound != 900*time.Second {
		t.Fatalf("floor = %v, want the probed Console JWT life of 900s", MinJWTClaimBound)
	}
	if err := CheckJWTClaimBound(MinJWTClaimBound - time.Second); err == nil {
		t.Fatal("a bound below the console JWT life was accepted")
	}
	if err := CheckJWTClaimBound(MinJWTClaimBound); err != nil {
		t.Fatal(err)
	}
	if err := CheckJWTClaimBound(0); err == nil {
		t.Fatal("zero accepted")
	}
}

func TestHashJWTSignatureIsSHA256OfTheBytes(t *testing.T) {
	// sha256("abc")
	h := HashJWTSignature([]byte("abc"))
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := hexOf(h[:]); got != want {
		t.Fatalf("hash = %s", got)
	}
}

func hexOf(b []byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, d[x>>4], d[x&15])
	}
	return string(out)
}
