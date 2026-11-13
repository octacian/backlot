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

func TestExactSSLSettingsPolicy(t *testing.T) {
	policy := `<key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAED</data><key>kSecTrustSettingsPolicyName</key><string>sslServer</string>`
	result := `<key>kSecTrustSettingsResult</key><integer>1</integer>`
	for _, tc := range []struct {
		name, policy string
		want         bool
	}{
		{"default trustRoot", policy, true},
		{"explicit trustRoot", policy + result, true},
		{"deny", policy + strings.Replace(result, ">1<", ">3<", 1), false},
		{"trustAsRoot", policy + strings.Replace(result, ">1<", ">2<", 1), false},
		{"wrong result type", policy + strings.ReplaceAll(result, "integer", "string"), false},
		{"broader policy", strings.Replace(policy, "sslServer", "basicX509", 1), false},
		{"wrong policy OID", strings.Replace(policy, "KoZIhvdjZAED", "unexpected", 1), false},
		{"extra constraint", policy + `<key>kSecTrustSettingsPolicyString</key><string>localhost</string>`, false},
		{"explicit extra constraint", policy + result + `<key>kSecTrustSettingsPolicyString</key><string>localhost</string>`, false},
		{"duplicate key", policy + `<key>kSecTrustSettingsPolicyName</key><string>sslServer</string>`, false},
		{"missing value", policy + `<key>kSecTrustSettingsResult</key>`, false},
		{"malformed key", strings.Replace(policy, "<key>kSecTrustSettingsPolicy</key>", "<string>kSecTrustSettingsPolicy</string>", 1), false},
		{"nested key", strings.Replace(policy, "Policy</key>", "Policy<dict/></key>", 1), false},
		{"nested policy", strings.Replace(policy, "KoZIhvdjZAED</data>", "KoZIhvdjZAED<dict/></data>", 1), false},
		{"nested name", strings.Replace(policy, "sslServer</string>", "sslServer<dict/></string>", 1), false},
		{"nested result", policy + strings.Replace(result, "1</integer>", "1<dict/></integer>", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := `<dict><key>trustSettings</key><array><dict>` + tc.policy + `</dict></array></dict>`
			if got := exactSSLSettings(entry); got != tc.want {
				t.Fatalf("exactSSLSettings = %v, want %v", got, tc.want)
			}
		})
	}
}
