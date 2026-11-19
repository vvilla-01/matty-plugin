package appcheck

import (
	"crypto/sha256"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"time"
)

// What the check is made against, built in rather than fetched, so that the
// plugin contacts nobody for it. data/README.md says where each file came
// from and when; a release made later can carry newer copies.
//
//go:embed data/google-rsa-root-2022-2042.pem data/google-ecdsa-p384-key-attestation-ca1-2025-2035.pem data/revocation-status.json data/grapheneos-attestation.json
var data embed.FS

// The SubjectPublicKeyInfo SHA-256 of Google's two hardware attestation
// roots: the RSA 4096 key, under which Google has issued its root four times
// (2016, 2019, 2021, 2022) and every one of them is this key, and the ECDSA
// P-384 "Key Attestation CA1" that signs chains since February 2026. A test
// holds the embedded certificates to these.
const (
	GoogleRSARootKey   = "feb2ea7551ee316ed4bb443c8293b884dbfdea40b603ee3e4f4a897e4580fbae"
	GoogleECDSARootKey = "3ee44512a1af2beb39c889490c60ea3f82e43f5d5a5532f5ab9419f676cd07ec"
)

// The copies built in, as of when they were taken.
const (
	// RevocationListDate is when Google last changed the list built in
	// (its Last-Modified), and RevocationListCount how many entries it has.
	RevocationListDate  = "2026-11-16"
	RevocationListCount = 1785

	// GrapheneOSListDate is the timestamp inside GrapheneOS's signed list of
	// its verified-boot keys.
	GrapheneOSListDate = "2026-04-20"
)

var loaded = sync.OnceValues(load)

type builtIn struct {
	roots    map[string]bool
	revoked  map[string]bool
	bootKeys map[string]bool
}

// GoogleRoots are the root keys a real phone's chain ends in (Policy.Roots).
func GoogleRoots() (map[string]bool, error) {
	b, err := loaded()
	if err != nil {
		return nil, err
	}

	return copyOf(b.roots), nil
}

// RevokedSerials are the serials on Google's list of revoked attestation
// certificates as built in (Policy.Revoked): each as the list writes it,
// lower case. Revoked and suspended alike.
func RevokedSerials() (map[string]bool, error) {
	b, err := loaded()
	if err != nil {
		return nil, err
	}

	return b.revoked, nil
}

// GrapheneOSBootKeys are GrapheneOS's verified-boot key digests, one per
// supported device, as built in (Policy.BootKeys).
func GrapheneOSBootKeys() (map[string]bool, error) {
	b, err := loaded()
	if err != nil {
		return nil, err
	}

	return copyOf(b.bootKeys), nil
}

func copyOf(set map[string]bool) map[string]bool {
	out := make(map[string]bool, len(set))
	for key := range set {
		out[key] = true
	}

	return out
}

func load() (*builtIn, error) {
	b := &builtIn{roots: map[string]bool{}, revoked: map[string]bool{}, bootKeys: map[string]bool{}}

	for name, want := range map[string]string{
		"data/google-rsa-root-2022-2042.pem":                       GoogleRSARootKey,
		"data/google-ecdsa-p384-key-attestation-ca1-2025-2035.pem": GoogleECDSARootKey,
	} {
		raw, err := data.ReadFile(name)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("%s is not a PEM certificate", name)
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		if got := hex.EncodeToString(sum[:]); got != want {
			return nil, fmt.Errorf("%s has the key %s, not Google's %s", name, got, want)
		}
		b.roots[want] = true
	}

	raw, err := data.ReadFile("data/revocation-status.json")
	if err != nil {
		return nil, err
	}
	var status struct {
		Entries map[string]struct {
			Status string `json:"status"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("the revocation list: %w", err)
	}
	for serial, entry := range status.Entries {
		if entry.Status == "REVOKED" || entry.Status == "SUSPENDED" {
			b.revoked[strings.ToLower(serial)] = true
		}
	}

	raw, err = data.ReadFile("data/grapheneos-attestation.json")
	if err != nil {
		return nil, err
	}
	var graphene struct {
		Keys      []string `json:"verifiedBootKeys"`
		Timestamp int64    `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &graphene); err != nil {
		return nil, fmt.Errorf("the GrapheneOS list: %w", err)
	}
	if got := time.Unix(graphene.Timestamp, 0).UTC().Format(time.DateOnly); got != GrapheneOSListDate {
		return nil, fmt.Errorf("the GrapheneOS list is dated %s, not %s", got, GrapheneOSListDate)
	}
	for _, key := range graphene.Keys {
		digest, ok := ParseDigest(key)
		if !ok {
			return nil, fmt.Errorf("the GrapheneOS list holds %q, which is not a digest", key)
		}
		b.bootKeys[digest] = true
	}

	return b, nil
}
