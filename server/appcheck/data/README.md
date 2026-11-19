# What the app check is made against

Built into the plugin (data.go embeds the four files marked *embedded*); nothing is fetched while it runs.
A later release can carry newer copies: replace the file, change the date and count in data.go, and the tests
say whether anything else has to follow.

## Google's hardware attestation roots (embedded)

From https://android.googleapis.com/attestation/root, a JSON array of two PEM certificates, fetched
2026-11-18 (the same two are in github.com/android/keyattestation, roots.json, and on
developer.android.com/privacy-and-security/security-key-attestation under "Root certificates").

| file | subject | valid | SubjectPublicKeyInfo SHA-256 |
|---|---|---|---|
| google-rsa-root-2022-2042.pem | serialNumber=f92009e853b6b045 (RSA 4096) | 2022-03-20 to 2042-03-15 | feb2ea7551ee316ed4bb443c8293b884dbfdea40b603ee3e4f4a897e4580fbae |
| google-ecdsa-p384-key-attestation-ca1-2025-2035.pem | CN=Key Attestation CA1, OU=Android, O=Google LLC, C=US (EC P-384) | 2025-07-17 to 2035-07-15 | 3ee44512a1af2beb39c889490c60ea3f82e43f5d5a5532f5ab9419f676cd07ec |

A chain is accepted by its root's key, not by the root certificate: Google issued the RSA root in 2016, 2019,
2021 and 2022, all over the same key, and phones send whichever their chain was made under.

## Google's list of revoked attestation certificates (embedded)

revocation-status.json is https://android.googleapis.com/attestation/status as fetched on 2026-11-18
(Last-Modified: Mon, 16 Nov 2026 20:48:00 GMT): 1785 entries, every one REVOKED (1759 KEY_COMPROMISE,
26 SOFTWARE_FLAW). Keys are certificate serial numbers; most are lower-case hex, some are decimal, and both
forms are looked up. Google refreshes the list daily; this copy is as old as the release that carries it, so
a key leaked since then is not on it.

## GrapheneOS's verified-boot keys (embedded)

grapheneos-attestation.json is https://grapheneos.org/attestation.json: 21 keys, one per supported Pixel,
timestamp 1776709772 (2026-04-20). It is signed: grapheneos-attestation.json.sig beside it, checked with
grapheneos-allowed_signers (from https://grapheneos.org/allowed_signers) by

    ssh-keygen -Y verify -f grapheneos-allowed_signers -I contact@grapheneos.org \
      -n "attestation keys" -s grapheneos-attestation.json.sig < grapheneos-attestation.json

which printed `Good "attestation keys" signature` for the ED25519 key
SHA256:AhgHif0mei+9aNyKLfMZBh2yptHdw/aN7Tlh/j2eFwM. The key comes from the same site as the list, so this
shows the two agree, not where they came from. The signature and the signers file are kept for that check and
are not embedded.
