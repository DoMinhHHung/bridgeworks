package organizationonboarding

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxLegalNameRunes   = 200
	maxWebsiteBytes     = 2048
	maxCompanyTypeRunes = 64
)

var ErrInvalidProfile = errors.New("invalid organization profile")

type StringPatch struct {
	Set   bool
	Value *string
}

type ProfilePatch struct {
	LegalName   StringPatch
	Website     StringPatch
	Country     StringPatch
	CompanyType StringPatch
}

func NormalizeProfilePatch(patch ProfilePatch) (ProfilePatch, error) {
	if !patch.LegalName.Set && !patch.Website.Set && !patch.Country.Set && !patch.CompanyType.Set {
		return ProfilePatch{}, ErrInvalidProfile
	}

	var err error
	if patch.LegalName, err = normalizeOptional(patch.LegalName, normalizeLegalName); err != nil {
		return ProfilePatch{}, err
	}
	if patch.Website, err = normalizeOptional(patch.Website, normalizeWebsite); err != nil {
		return ProfilePatch{}, err
	}
	if patch.Country, err = normalizeOptional(patch.Country, normalizeCountry); err != nil {
		return ProfilePatch{}, err
	}
	if patch.CompanyType, err = normalizeOptional(patch.CompanyType, normalizeCompanyType); err != nil {
		return ProfilePatch{}, err
	}
	return patch, nil
}

func normalizeOptional(field StringPatch, normalize func(string) (string, error)) (StringPatch, error) {
	if !field.Set || field.Value == nil {
		return field, nil
	}
	value, err := normalize(*field.Value)
	if err != nil {
		return StringPatch{}, ErrInvalidProfile
	}
	field.Value = &value
	return field, nil
}

func normalizeLegalName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxLegalNameRunes {
		return "", ErrInvalidProfile
	}
	return value, nil
}

func normalizeWebsite(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxWebsiteBytes {
		return "", ErrInvalidProfile
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return "", ErrInvalidProfile
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrInvalidProfile
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", ErrInvalidProfile
	}

	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}

	normalized := parsed.String()
	if len(normalized) > maxWebsiteBytes {
		return "", ErrInvalidProfile
	}
	return normalized, nil
}

func normalizeCountry(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 2 || !strings.Contains(iso3166Alpha2, " "+value+" ") {
		return "", ErrInvalidProfile
	}
	return value, nil
}

func normalizeCompanyType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || utf8.RuneCountInString(value) > maxCompanyTypeRunes {
		return "", ErrInvalidProfile
	}
	for index, character := range value {
		alphaNumeric := character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		separator := character == '_' || character == '-'
		if !alphaNumeric && !separator {
			return "", ErrInvalidProfile
		}
		if (index == 0 || index == len(value)-1) && separator {
			return "", ErrInvalidProfile
		}
	}
	return value, nil
}

const iso3166Alpha2 = " AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW "
