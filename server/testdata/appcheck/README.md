# Attestation chains for the app check's tests

## keyattestation/

Certificate chains that real phones made, with the JSON decoding beside each, from Google's reference verifier
for Android key attestation: https://github.com/android/keyattestation, commit
c391708adccdeff0ac12f51e81cebfe5de42a017 (2026-11-16), directory `testdata/`, fetched 2026-11-18. File names
are the repository's paths with `/` turned into `-` or `_` (`caiman/sdk36/TEE_EC_RKP.pem` is
`caiman-sdk36_TEE_EC_RKP.pem`, `invalid/...` is `invalid_...`). The files are unchanged.

They are Copyright Google LLC and licensed under the Apache License, Version 2.0; LICENSE beside them is the
repository's licence file, unchanged. They are used only as test data and are not part of the plugin's build.

What they cover: remotely provisioned chains under Google's RSA root (caiman, akita, tokay) and its ECDSA root
(frankel, tegu), factory-provisioned ones (blueline, walleye, sony-xperia10-iii, tokay), TEE and StrongBox,
attestation versions 2, 3, 300, 400 and 500, an ML-DSA key (tokay), locked phones with verified systems and
unlocked ones, Android's software attestation (marlin), and two chains Google made wrong on purpose (invalid_*).

## emulator/

What Matty's development emulator (Android 17, sdk_gphone64_x86_64 CE2A.260420.019, software KeyMint 4) made
on 2026-11-18 for a probe app, `dev.probe.attest` version 7, asked to attest an EC P-256 key with the challenge
A0 A1 ... BF, the app signed three ways with throwaway keys: hybrid RSA 4096 + ML-DSA-87 (`hybrid-clean`),
classical RSA only (`classic`), and hybrid with a debug key first in its signing history (`hybrid-rotate`). The
chain ends in the emulator's own test root (CN=Droid Unregistered Device CA, O=Google Test LLC, valid until
2027-01-19; its intermediates until 2026-11-24, so tests read them at a fixed time). The attested digest is the
throwaway RSA certificate's (FF:2B:7E:63...) for the first two and the debug key's (70:89:65:94...) for the
third; the ML-DSA certificate's appears in none.
