package trust

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strings"
)

// plistValue is only an XML trust-settings reader, never a store writer. Reject
// unknown shapes rather than importing or replacing an account-wide settings file.
type plistValue struct {
	XMLName  xml.Name
	Text     string       `xml:",chardata"`
	Children []plistValue `xml:",any"`
}

func (v plistValue) dictionary() bool {
	if v.XMLName.Local != "dict" || len(v.Children)%2 != 0 {
		return false
	}
	seen := make(map[string]bool, len(v.Children)/2)
	for i := 0; i < len(v.Children); i += 2 {
		if v.Children[i].XMLName.Local != "key" || len(v.Children[i].Children) != 0 || seen[v.Children[i].Text] {
			return false
		}
		seen[v.Children[i].Text] = true
	}
	return true
}

func (v plistValue) member(key string) (plistValue, bool) {
	if !v.dictionary() {
		return plistValue{}, false
	}
	for i := 0; i < len(v.Children); i += 2 {
		if v.Children[i].Text == key {
			return v.Children[i+1], true
		}
	}
	return plistValue{}, false
}

func settingsEntry(data []byte, hash string) (string, bool, error) {
	var root plistValue
	if err := xml.Unmarshal(data, &root); err != nil || root.XMLName.Local != "plist" || len(root.Children) != 1 {
		return "", false, errors.New("invalid trust-settings XML")
	}
	version, ok := root.Children[0].member("trustVersion")
	if !ok || version.XMLName.Local != "integer" || strings.TrimSpace(version.Text) != "1" {
		return "", false, errors.New("unsupported trust-settings version")
	}
	list, ok := root.Children[0].member("trustList")
	if !ok || !list.dictionary() {
		return "", false, errors.New("missing trust-settings list")
	}
	entry, ok := list.member(strings.ToUpper(hash))
	if _, lower := list.member(strings.ToLower(hash)); ok && lower && strings.ToUpper(hash) != strings.ToLower(hash) {
		return "", false, errors.New("ambiguous trust-settings certificate key")
	}
	if !ok {
		entry, ok = list.member(strings.ToLower(hash))
	}
	if !ok {
		return "", false, nil
	}
	normalizePlist(&entry)
	canonical, err := xml.Marshal(entry)
	return string(canonical), true, err
}

func normalizePlist(v *plistValue) {
	if len(v.Children) != 0 {
		v.Text = ""
	}
	if v.XMLName.Local == "data" {
		v.Text = strings.Join(strings.Fields(v.Text), "")
	}
	for i := range v.Children {
		normalizePlist(&v.Children[i])
	}
}

func exactSSLSettings(entry string) bool {
	var root plistValue
	if xml.Unmarshal([]byte(entry), &root) != nil {
		return false
	}
	settings, ok := root.member("trustSettings")
	if !ok || settings.XMLName.Local != "array" || len(settings.Children) != 1 {
		return false
	}
	item := settings.Children[0]
	if !item.dictionary() || (len(item.Children) != 4 && len(item.Children) != 6) {
		return false
	}
	policy, ok := item.member("kSecTrustSettingsPolicy")
	if !ok || policy.XMLName.Local != "data" || len(policy.Children) != 0 || !bytes.Equal([]byte(policy.Text), []byte("KoZIhvdjZAED")) {
		return false
	}
	name, ok := item.member("kSecTrustSettingsPolicyName")
	if !ok || name.XMLName.Local != "string" || len(name.Children) != 0 || name.Text != "sslServer" {
		return false
	}
	result, ok := item.member("kSecTrustSettingsResult")
	// Stock security omits the default trustRoot result; no other policy members are allowed.
	if !ok {
		return len(item.Children) == 4
	}
	return result.XMLName.Local == "integer" && len(result.Children) == 0 && result.Text == "1"
}
