package trust

import (
	"strings"
	"testing"
)

func TestExactSettingsIdentity(t *testing.T) {
	entry := `<dict><key>issuerName</key><data>YWJj</data><key>trustSettings</key><array><dict><key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAED</data><key>kSecTrustSettingsPolicyName</key><string>sslServer</string><key>kSecTrustSettingsResult</key><integer>1</integer></dict></array></dict>`
	fixture := `<plist version="1.0"><dict><key>trustVersion</key><integer>1</integer><key>trustList</key><dict><key>ABCDEF</key>` + entry + `</dict></dict></plist>`
	got, found, err := settingsEntry([]byte(fixture), "abcdef")
	if err != nil || !found || !exactSSLSettings(got) {
		t.Fatal(got, found, err)
	}
	if _, found, err = settingsEntry([]byte(fixture), "other"); err != nil || found {
		t.Fatal("name guessed as authority")
	}
	for _, changed := range []string{strings.Replace(got, "<integer>1</integer>", "<integer>3</integer>", 1), strings.Replace(got, "sslServer", "basicX509", 1), strings.Replace(got, "</array>", "<dict></dict></array>", 1), strings.Replace(got, "KoZIhvdjZAED", "unexpected", 1)} {
		if exactSSLSettings(changed) {
			t.Fatal("external settings accepted", changed)
		}
	}
	for _, bad := range []string{
		strings.Replace(fixture, "<key>ABCDEF</key>", "<key>ABCDEF</key>"+entry+"<key>abcdef</key>", 1),
		strings.Replace(fixture, "<key>ABCDEF</key>", "<key>ABCDEF</key>"+entry+"<key>ABCDEF</key>", 1),
		strings.Replace(fixture, "<key>ABCDEF</key>", "<string>ABCDEF</string>", 1),
	} {
		if _, _, err := settingsEntry([]byte(bad), "abcdef"); err == nil {
			t.Fatal("ambiguous/malformed list treated as absent")
		}
	}
	if _, _, err = settingsEntry([]byte(strings.Replace(fixture, "<integer>1</integer>", "<integer>2</integer>", 1)), "abcdef"); err == nil {
		t.Fatal("unsupported settings version accepted")
	}
}
