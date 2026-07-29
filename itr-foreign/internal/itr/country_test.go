package itr

import "testing"

// The ITR country-code list is ISD-derived but is NOT the ISD list: the US and
// Canada share ISD +1, so the ITD assigns Canada 1 and the United States 2.
// Filing 1 for a US entry fails the offline utility's schema validation, so this
// is the one entry in the table that must never be "fixed" back to the ISD code.
func TestUSIsTwoAndCanadaIsOne(t *testing.T) {
	name, code := Country("US")
	if code != "2" {
		t.Errorf("US country code = %q, want %q (1 is Canada)", code, "2")
	}
	if name != "United States of America" {
		t.Errorf("US country name = %q", name)
	}
	if _, code := Country("USA"); code != "2" {
		t.Errorf("USA country code = %q, want %q", code, "2")
	}
	if _, code := Country("CA"); code != "1" {
		t.Errorf("Canada country code = %q, want %q", code, "1")
	}
}

// The rest of the table really is the ISD code.
func TestCommonCountries(t *testing.T) {
	for iso, want := range map[string]string{
		"IE": "353", "GB": "44", "UK": "44", "SG": "65", "DE": "49",
		"NL": "31", "CH": "41", "JP": "81", "HK": "852", "LU": "352", "AU": "61", "FR": "33",
	} {
		if _, code := Country(iso); code != want {
			t.Errorf("Country(%s) = %q, want %q", iso, code, want)
		}
	}
}

// An unmapped country must surface as a blank code so the caller flags it for
// manual entry, rather than inventing one.
func TestUnknownCountryHasNoCode(t *testing.T) {
	name, code := Country("ZZ")
	if code != "" {
		t.Errorf("unknown country code = %q, want empty", code)
	}
	if name != "ZZ" {
		t.Errorf("unknown country name = %q, want the input back", name)
	}
	if n, c := Country(""); n != "" || c != "" {
		t.Errorf("empty ISO gave (%q, %q), want empty", n, c)
	}
}

func TestNameForCode(t *testing.T) {
	if got, want := NameForCode("2"), "United States of America"; got != want {
		t.Errorf("NameForCode(2) = %q, want %q", got, want)
	}
	if got, want := NameForCode("353"), "Ireland"; got != want {
		t.Errorf("NameForCode(353) = %q, want %q", got, want)
	}
	if got := NameForCode("9999"); got != "" {
		t.Errorf("NameForCode(9999) = %q, want empty", got)
	}
}

func TestInstitution(t *testing.T) {
	addr, zip, name, code := Institution("IBLLC-US")
	if code != "2" {
		t.Errorf("IBKR LLC country code = %q, want %q", code, "2")
	}
	if addr == "" || zip == "" || name == "" {
		t.Errorf("incomplete institution metadata: %q %q %q", addr, zip, name)
	}
	// An unknown entity must not silently inherit the US details.
	if _, _, _, code := Institution("IBIE"); code != "" {
		t.Errorf("unknown IB entity country code = %q, want empty", code)
	}
}
