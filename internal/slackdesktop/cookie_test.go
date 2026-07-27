package slackdesktop

import "testing"

func TestDecryptCookieValueLinuxPeanuts(t *testing.T) {
	// v10 on linux -> password "peanuts", rounds 1
	want := []byte("xoxd-linux-peanuts")
	enc := append([]byte("v10"), cbcEncrypt(t, want, []byte("peanuts"), 1)...)
	got, err := decryptCookieValue("linux", enc, func() ([]byte, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecryptCookieValueLinuxKeyring(t *testing.T) {
	want := []byte("xoxd-linux-keyring")
	enc := append([]byte("v11"), cbcEncrypt(t, want, []byte("keyring-pw"), 1)...)
	got, err := decryptCookieValue("linux", enc, func() ([]byte, error) { return []byte("keyring-pw"), nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
