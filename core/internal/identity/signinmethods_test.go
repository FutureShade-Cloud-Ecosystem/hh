package identity

import (
	"reflect"
	"testing"
)

func TestSignInMethodVocabularyIsClosed(t *testing.T) {
	want := []string{"email_password", "google", "microsoft", "magic_link"}
	if !reflect.DeepEqual(SignInMethodCodes(), want) {
		t.Fatalf("vocabulary = %v, want %v", SignInMethodCodes(), want)
	}
	// A caller cannot widen the vocabulary through the returned slice.
	got := SignInMethodCodes()
	got[0] = "x"
	if SignInMethodCodes()[0] != "email_password" {
		t.Fatal("the vocabulary was mutated through its copy")
	}
}

func TestDefaultSignInMethodsAreEmailAndGoogle(t *testing.T) {
	if got := DefaultSignInMethods(); !reflect.DeepEqual(got, []string{"email_password", "google"}) {
		t.Fatalf("default = %v", got)
	}
	d := DefaultSignInMethods()
	d[0] = "x"
	if DefaultSignInMethods()[0] != "email_password" {
		t.Fatal("the default was mutated through its copy")
	}
}

func TestSignInMethodSetFromRows(t *testing.T) {
	cases := []struct {
		name string
		rows []string
		want []string
		bad  bool
	}{
		{"no rows is the default", nil, []string{"email_password", "google"}, false},
		{"one code", []string{"google"}, []string{"google"}, false},
		{"returned in vocabulary order", []string{"magic_link", "email_password"}, []string{"email_password", "magic_link"}, false},
		{"duplicate rows collapse", []string{"google", "google"}, []string{"google"}, false},
		{"an unknown code is a defect", []string{"google", "sms"}, nil, true},
		{"a lone unknown code is a defect", []string{"password"}, nil, true},
		{"an empty code is a defect", []string{""}, nil, true},
		{"a differently cased code is a defect", []string{"Google"}, nil, true},
		{"a padded code is a defect", []string{"google "}, nil, true},
	}
	for _, c := range cases {
		got, err := signInMethodSet(c.rows)
		if c.bad {
			if err == nil || got != nil {
				t.Errorf("%s: got %v, %v; want a refusal and no codes", c.name, got, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
}

func TestSignInMethodStoreRequiresAPool(t *testing.T) {
	if _, err := NewSignInMethodStore(nil); err == nil {
		t.Fatal("a nil pool was accepted")
	}
}
