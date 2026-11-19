//go:build villanet_standin

package appcheck

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The development build takes what the development emulator makes: a
// software attestation under the emulator's own test root, from an unlocked
// phone, and nothing else is relaxed: the challenge, the package and the
// signing certificate are held as in a release.
func TestStandInTheDevelopmentBuildAcceptsTheEmulator(t *testing.T) {
	require.True(t, developmentBuild)

	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(0xa0 + i)
	}
	const rsa = "ff2b7e6348b7dc1e97882e96fe6f8f4ccf6613f25255d364dd210e129cf0072c"
	at := day("2026-11-18")

	for _, file := range []string{"emulator/classic.pem", "emulator/hybrid-clean.pem"} {
		policy := googlePolicy(t, "dev.probe.attest", rsa)
		policy.Development = true

		result := Verify(readChain(t, file), challenge, policy, at)
		require.True(t, result.Verified, "%s: %s", file, result.Reason)
		require.Equal(t, Facts{
			AttestationVersion: 400, KeyMintVersion: 400, Level: Software,
			Package: "dev.probe.attest", VersionCode: 7, Digest: rsa,
			BootState: BootUnverified, DeviceLocked: false, OSPatchLevel: 0,
			Development: true,
		}, result.Facts, file)

		// Without the policy asking, the development build is a release.
		policy.Development = false
		require.Contains(t, Verify(readChain(t, file), challenge, policy, at).Reason, "Google's hardware attestation roots")
	}

	policy := googlePolicy(t, "dev.probe.attest", rsa)
	policy.Development = true

	// A history that starts with the debug key attests the debug key, which
	// has to be listed like any other certificate.
	rotate := Verify(readChain(t, "emulator/hybrid-rotate.pem"), challenge, policy, at)
	require.Equal(t, "This copy of Matty was signed with a certificate this server does not accept (70:89:65:94:E5:82:2D:14…).", rotate.Reason)

	wrong := append([]byte{}, challenge...)
	wrong[0] ^= 1
	require.Equal(t, "The app's attestation was made for another challenge, so it is not an answer to this one.",
		Verify(readChain(t, "emulator/classic.pem"), wrong, policy, at).Reason)

	matty := googlePolicy(t, "ceo.villa.matty.debug", rsa)
	matty.Development = true
	require.Equal(t, "The app that asked is dev.probe.attest, not Matty.", Verify(readChain(t, "emulator/classic.pem"), challenge, matty, at).Reason)
}
