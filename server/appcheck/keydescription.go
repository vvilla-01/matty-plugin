package appcheck

import (
	"encoding/asn1"
	"errors"
	"fmt"
)

// The attestation extension, as Android's Keystore writes it into the leaf
// certificate of a key it was asked to attest (source.android.com,
// "Key and ID attestation", schema versions 1 to 500; KeyCreationResult.aidl):
//
//	KeyDescription ::= SEQUENCE {
//	    attestationVersion INTEGER, attestationSecurityLevel SecurityLevel,
//	    keyMintVersion INTEGER, keyMintSecurityLevel SecurityLevel,
//	    attestationChallenge OCTET STRING, uniqueId OCTET STRING,
//	    softwareEnforced AuthorizationList, hardwareEnforced AuthorizationList }
//
// The eight fields are the same in every version. The two lists are
// SEQUENCEs of optional, EXPLICITly tagged fields, which every version adds
// to; only the ones this package decides by are read, strictly, and every
// other is skipped whatever it holds.

// The AuthorizationList fields read here.
const (
	tagAllApplications = 600
	tagOrigin          = 702
	tagRootOfTrust     = 704
	tagOSPatchLevel    = 706
	tagApplicationID   = 709
)

// originGenerated is KeyOrigin GENERATED: the key was made inside the
// secure hardware (or, for a software key, inside Keystore), not imported.
const originGenerated = 0

// keyDescription is what is read of the extension.
type keyDescription struct {
	attestationVersion int64
	attestationLevel   Level
	keyMintVersion     int64
	keyMintLevel       Level
	challenge          []byte
	software           authorizationList
	hardware           authorizationList
}

// authorizationList is what is read of one of the two lists.
type authorizationList struct {
	allApplications bool
	origin          *int64
	rootOfTrust     *rootOfTrust
	osPatchLevel    int64
	applicationID   []byte
}

type rootOfTrust struct {
	bootKey      []byte
	deviceLocked bool
	bootState    BootState
}

// applicationID is the attestationApplicationId: which packages run under
// the uid that asked for the key, and the SHA-256 of each signing
// certificate Android lists for the first of them.
//
//	AttestationApplicationId ::= SEQUENCE {
//	    packageInfoRecords SET OF PackageInfoRecord,
//	    signatureDigests SET OF OCTET STRING }
//	PackageInfoRecord ::= SEQUENCE { packageName OCTET STRING, version INTEGER }
type applicationID struct {
	packages []packageRecord
	digests  [][]byte
}

type packageRecord struct {
	name    string
	version int64
}

var errTrailing = errors.New("it has bytes after its end")

// elements is the content of a universal SEQUENCE or SET, as raw values,
// with nothing after it.
func elements(der []byte, tag int) ([]asn1.RawValue, error) {
	var outer asn1.RawValue

	rest, err := asn1.Unmarshal(der, &outer)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, errTrailing
	}

	return contents(outer, tag)
}

// contents is the elements of a raw value already read, which must be a
// constructed universal value with the tag given.
func contents(outer asn1.RawValue, tag int) ([]asn1.RawValue, error) {
	if outer.Class != asn1.ClassUniversal || outer.Tag != tag || !outer.IsCompound {
		return nil, fmt.Errorf("expected a universal constructed tag %d, found class %d tag %d", tag, outer.Class, outer.Tag)
	}

	var items []asn1.RawValue
	for rest := outer.Bytes; len(rest) > 0; {
		var item asn1.RawValue

		next, err := asn1.Unmarshal(rest, &item)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		rest = next
	}

	return items, nil
}

// exactly decodes one whole value into out: the type of out says which
// universal tag it must carry, and nothing may follow it.
func exactly(der []byte, out any) error {
	rest, err := asn1.Unmarshal(der, out)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errTrailing
	}

	return nil
}

func parseKeyDescription(der []byte) (*keyDescription, error) {
	items, err := elements(der, asn1.TagSequence)
	if err != nil {
		return nil, fmt.Errorf("the attestation is not a sequence: %w", err)
	}
	if len(items) < 8 {
		return nil, fmt.Errorf("the attestation has %d fields, not 8", len(items))
	}

	var kd keyDescription
	var attestationLevel, keyMintLevel asn1.Enumerated

	fields := []struct {
		name string
		out  any
	}{
		{"attestationVersion", &kd.attestationVersion},
		{"attestationSecurityLevel", &attestationLevel},
		{"keyMintVersion", &kd.keyMintVersion},
		{"keyMintSecurityLevel", &keyMintLevel},
		{"attestationChallenge", &kd.challenge},
	}
	for i, field := range fields {
		if err := exactly(items[i].FullBytes, field.out); err != nil {
			return nil, fmt.Errorf("its %s does not read: %w", field.name, err)
		}
	}
	kd.attestationLevel = Level(attestationLevel)
	kd.keyMintLevel = Level(keyMintLevel)

	// uniqueId is not read, only required to be what it is.
	var uniqueID []byte
	if err := exactly(items[5].FullBytes, &uniqueID); err != nil {
		return nil, fmt.Errorf("its uniqueId does not read: %w", err)
	}

	if kd.software, err = parseAuthorizationList(items[6]); err != nil {
		return nil, fmt.Errorf("its software-enforced list does not read: %w", err)
	}
	if kd.hardware, err = parseAuthorizationList(items[7]); err != nil {
		return nil, fmt.Errorf("its hardware-enforced list does not read: %w", err)
	}

	return &kd, nil
}

func parseAuthorizationList(raw asn1.RawValue) (authorizationList, error) {
	var list authorizationList

	items, err := contents(raw, asn1.TagSequence)
	if err != nil {
		return list, err
	}

	seen := map[int]bool{}
	for _, item := range items {
		if item.Class != asn1.ClassContextSpecific || !item.IsCompound {
			return list, fmt.Errorf("field [%d] is not an explicitly tagged field", item.Tag)
		}

		switch item.Tag {
		case tagAllApplications, tagOrigin, tagRootOfTrust, tagApplicationID:
		default:
			// Not decided by: read where it is useful to say, and never
			// a reason to refuse.
			if item.Tag == tagOSPatchLevel {
				var level int64
				if exactly(item.Bytes, &level) == nil {
					list.osPatchLevel = level
				}
			}

			continue
		}

		if seen[item.Tag] {
			return list, fmt.Errorf("field [%d] appears twice", item.Tag)
		}
		seen[item.Tag] = true

		switch item.Tag {
		case tagAllApplications:
			var null asn1.RawValue
			if err := exactly(item.Bytes, &null); err != nil || null.Class != asn1.ClassUniversal || null.Tag != asn1.TagNull || len(null.Bytes) != 0 {
				return list, errors.New("allApplications is not a NULL")
			}
			list.allApplications = true

		case tagOrigin:
			var origin int64
			if err := exactly(item.Bytes, &origin); err != nil {
				return list, fmt.Errorf("origin does not read: %w", err)
			}
			list.origin = &origin

		case tagRootOfTrust:
			rot, err := parseRootOfTrust(item.Bytes)
			if err != nil {
				return list, fmt.Errorf("rootOfTrust does not read: %w", err)
			}
			list.rootOfTrust = rot

		case tagApplicationID:
			var id []byte
			if err := exactly(item.Bytes, &id); err != nil {
				return list, fmt.Errorf("attestationApplicationId does not read: %w", err)
			}
			list.applicationID = id
		}
	}

	return list, nil
}

//	RootOfTrust ::= SEQUENCE { verifiedBootKey OCTET STRING,
//	    deviceLocked BOOLEAN, verifiedBootState VerifiedBootState,
//	    verifiedBootHash OCTET STRING }
//
// verifiedBootHash came with attestation version 3; version 2 has the first
// three alone.
func parseRootOfTrust(der []byte) (*rootOfTrust, error) {
	items, err := elements(der, asn1.TagSequence)
	if err != nil {
		return nil, err
	}
	if len(items) != 3 && len(items) != 4 {
		return nil, fmt.Errorf("it has %d fields, not 3 or 4", len(items))
	}

	var rot rootOfTrust
	var state asn1.Enumerated

	if err := exactly(items[0].FullBytes, &rot.bootKey); err != nil {
		return nil, fmt.Errorf("verifiedBootKey: %w", err)
	}
	if err := exactly(items[1].FullBytes, &rot.deviceLocked); err != nil {
		return nil, fmt.Errorf("deviceLocked: %w", err)
	}
	if err := exactly(items[2].FullBytes, &state); err != nil {
		return nil, fmt.Errorf("verifiedBootState: %w", err)
	}
	rot.bootState = BootState(state)

	if len(items) == 4 {
		var hash []byte
		if err := exactly(items[3].FullBytes, &hash); err != nil {
			return nil, fmt.Errorf("verifiedBootHash: %w", err)
		}
	}

	return &rot, nil
}

func parseApplicationID(der []byte) (*applicationID, error) {
	items, err := elements(der, asn1.TagSequence)
	if err != nil {
		return nil, err
	}
	if len(items) != 2 {
		return nil, fmt.Errorf("it has %d fields, not 2", len(items))
	}

	var id applicationID

	records, err := contents(items[0], asn1.TagSet)
	if err != nil {
		return nil, fmt.Errorf("its packages: %w", err)
	}
	for _, record := range records {
		fields, err := contents(record, asn1.TagSequence)
		if err != nil || len(fields) != 2 {
			return nil, errors.New("a package record is not a name and a version")
		}

		var name []byte
		var version int64
		if exactly(fields[0].FullBytes, &name) != nil || exactly(fields[1].FullBytes, &version) != nil {
			return nil, errors.New("a package record is not a name and a version")
		}
		id.packages = append(id.packages, packageRecord{name: string(name), version: version})
	}

	digests, err := contents(items[1], asn1.TagSet)
	if err != nil {
		return nil, fmt.Errorf("its signature digests: %w", err)
	}
	for _, raw := range digests {
		var digest []byte
		if err := exactly(raw.FullBytes, &digest); err != nil {
			return nil, fmt.Errorf("a signature digest: %w", err)
		}
		id.digests = append(id.digests, digest)
	}

	return &id, nil
}
