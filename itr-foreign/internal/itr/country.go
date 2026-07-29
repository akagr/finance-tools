// Package itr holds lookups defined by the ITR forms themselves, shared by every
// schedule this tool builds (FA, FSI, TR).
package itr

import "strings"

// countries maps an ISO-3166 alpha-2 (or common 3-letter) code to the ITR
// (display name, country code).
//
// The ITR list is ISD-derived but is NOT the ISD list. The United States and
// Canada share ISD +1, so the ITD breaks the tie: Canada is 1 and the
// **United States is 2**. This is machine-enforced — the CountryCodeExcludingIndia
// enum in the ITD's published ITR-2 JSON schema, which the offline utility
// validates against, rejects 1 for a US entry. The same enum backs Schedules FA,
// FSI and TR, so this table is the single source of truth.
//
// Only common countries are mapped; anything else must be filled in manually
// (via --entities), which the report flags.
var countries = map[string][2]string{
	"US":  {"United States of America", "2"},
	"USA": {"United States of America", "2"},
	"IE":  {"Ireland", "353"},
	"GB":  {"United Kingdom", "44"},
	"UK":  {"United Kingdom", "44"},
	"CA":  {"Canada", "1"},
	"AU":  {"Australia", "61"},
	"DE":  {"Germany", "49"},
	"NL":  {"Netherlands", "31"},
	"FR":  {"France", "33"},
	"CH":  {"Switzerland", "41"},
	"SG":  {"Singapore", "65"},
	"JP":  {"Japan", "81"},
	"HK":  {"Hong Kong", "852"},
	"LU":  {"Luxembourg", "352"},
}

// Country returns the ITR display name and country code for an ISO country
// code. An unmapped country keeps its input as the name and returns an empty
// code, which callers flag for manual entry.
func Country(iso string) (name, code string) {
	if iso == "" {
		return "", ""
	}
	if v, ok := countries[strings.ToUpper(iso)]; ok {
		return v[0], v[1]
	}
	return iso, ""
}

// NameForCode returns the display name for an ITR country code, for when the
// code came from user-supplied metadata rather than from an ISO country.
func NameForCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	for _, v := range countries {
		if v[1] == code {
			return v[0]
		}
	}
	return ""
}

// Institution returns the address, ZIP, country name and ITR country code of an
// IBKR legal entity — the custodian details Table A2 asks for, and the source
// country for broker-paid cash interest.
func Institution(ibEntity string) (address, zip, countryName, countryCode string) {
	switch strings.ToUpper(strings.TrimSpace(ibEntity)) {
	case "", "IBLLC-US", "IBLLC":
		return "One Pickwick Plaza, Greenwich, CT", "06830", "United States of America", "2"
	default:
		return "", "", "", ""
	}
}
