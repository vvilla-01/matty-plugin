//go:build !villanet_standin

package appcheck

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A release is not the development build, and none of the development
// build's allowances can be had in it, whatever a policy asks for.
// standin_test.go is the other side.
func TestAReleaseIsNotTheDevelopmentBuild(t *testing.T) {
	require.False(t, developmentBuild)
}

func TestTheDevelopmentAllowancesAreNotInARelease(t *testing.T) {
	// Development asked for, by a policy, and nothing changes.
	a := goodAttestation()
	a.attestationLevel, a.keyMintLevel = Software, Software
	policy := testPolicy(t)
	policy.Development = true
	require.Equal(t, "This phone made its attestation in software, not in its secure hardware.", verifyWith(t, a, chainShape{}, policy).Reason)

	policy.Roots = map[string]bool{}
	require.Contains(t, verifyWith(t, goodAttestation(), chainShape{}, policy).Reason, "Google's hardware attestation roots")

	unlocked := goodAttestation()
	unlocked.hardware[2] = fieldRootOfTrust(make([]byte, 32), false, BootUnverified, true)
	policy = testPolicy(t)
	policy.Development = true
	require.Equal(t, "This phone's bootloader is unlocked.", verifyWith(t, unlocked, chainShape{}, policy).Reason)

	// The origin and the root of trust only in the list Android fills, where
	// an emulator puts them: the secure hardware's word is still the one
	// asked for. goodAttestation's hardware list is the key size, the
	// origin, the root of trust, the OS version and its patch level.
	moved := goodAttestation()
	hardware := moved.hardware
	moved.software = append(moved.software, hardware[1], hardware[2])
	moved.hardware = [][]byte{hardware[0], hardware[3], hardware[4]}
	require.Equal(t, "The phone's secure hardware did not say where the attested key was made.", verifyWith(t, moved, chainShape{}, policy).Reason)

	moved = goodAttestation()
	hardware = moved.hardware
	moved.software = append(moved.software, hardware[2])
	moved.hardware = [][]byte{hardware[0], hardware[1], hardware[3], hardware[4]}
	require.Equal(t, "This phone's secure hardware did not say how the phone started.", verifyWith(t, moved, chainShape{}, policy).Reason)
}

// A release refuses the emulator, whatever its policy asks: by its root, and,
// were the emulator's root pinned, by its software security level.
func TestAReleaseBuildRefusesTheEmulator(t *testing.T) {
	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(0xa0 + i)
	}
	const rsa = "ff2b7e6348b7dc1e97882e96fe6f8f4ccf6613f25255d364dd210e129cf0072c"

	for _, file := range []string{"emulator/classic.pem", "emulator/hybrid-clean.pem", "emulator/hybrid-rotate.pem"} {
		policy := googlePolicy(t, "dev.probe.attest", rsa)
		policy.Development = true

		result := Verify(readChain(t, file), challenge, policy, day("2026-11-18"))
		require.False(t, result.Verified)
		require.Equal(t, "The app's proof does not come from Google's hardware attestation roots, so no phone's secure hardware vouches for it.", result.Reason)

		policy.Roots["7a8cea957d0c4970fdc9b3d5cacb9ef68780600c76d37881b7df6f6436c0055f"] = true
		result = Verify(readChain(t, file), challenge, policy, day("2026-11-18"))
		require.Equal(t, "This phone made its attestation in software, not in its secure hardware.", result.Reason)
	}
}
