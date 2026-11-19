package pushsend

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// secret is in every test address, so that a test can see that no error
// or outcome repeats one.
const secret = "upS3cretT0pic"

func rules(t *testing.T, list string, onlyListed bool) Rules {
	t.Helper()

	hosts, bad := ParseHostList(list)
	require.Empty(t, bad)

	return Rules{Listed: hosts, OnlyListed: onlyListed}
}

func TestAPushAddressThatPasses(t *testing.T) {
	r := rules(t, "localhost:7788\npush.lan\n192.168.1.20:8080", false)

	for raw, host := range map[string]string{
		"https://ntfy.sh/" + secret + "?up=1":            "ntfy.sh",
		"https://NTFY.sh:443/" + secret:                  "ntfy.sh",
		"https://push.example.com:8443/" + secret:        "push.example.com:8443",
		"http://localhost:7788/push/" + secret:           "localhost:7788",
		"http://push.lan/" + secret:                      "push.lan",
		"https://push.lan/" + secret:                     "push.lan",
		"http://192.168.1.20:8080/" + secret:             "192.168.1.20:8080",
		"https://[2606:4700:4700::1111]/" + secret:       "[2606:4700:4700::1111]",
		"https://[2606:4700:4700::1111]:8443/" + secret:  "[2606:4700:4700::1111]:8443",
		"https://8.8.8.8/" + secret:                      "8.8.8.8",
		"https://push.example.com/" + secret + "?a=b&c=": "push.example.com",
	} {
		endpoint, err := ValidateEndpoint(raw, r)
		require.NoError(t, err, raw)
		require.Equal(t, raw, endpoint.URL)
		require.Equal(t, host, endpoint.Host, raw)
		require.Equal(t, host, HostOf(raw), raw)
	}

	endpoint, err := ValidateEndpoint("http://localhost:7788/x", r)
	require.NoError(t, err)
	require.True(t, endpoint.Listed)
	require.Equal(t, "http", endpoint.Scheme)

	endpoint, err = ValidateEndpoint("https://ntfy.sh/x", r)
	require.NoError(t, err)
	require.False(t, endpoint.Listed)
	require.Equal(t, "https", endpoint.Scheme)
}

func TestAPushAddressThatIsRefused(t *testing.T) {
	r := rules(t, "localhost:7788\npush.lan\n127.0.0.1\n192.168.1.20:8080", false)

	for raw, want := range map[string]error{
		"": ErrEndpointNotURL,
		"https://ntfy.sh/" + strings.Repeat("a", MaxEndpointBytes): ErrEndpointTooLong,
		"https://ntfy.sh/" + secret + " x":                         ErrEndpointCharacters,
		"https://ntfy.sh/" + secret + "\n":                         ErrEndpointCharacters,
		"https://ntfy.sh/" + secret + "é":                          ErrEndpointCharacters,
		"https://ntfy.sh/" + secret + "#frag":                      ErrEndpointFragment,
		"/" + secret:                                               ErrEndpointNotURL,
		"https:" + secret:                                          ErrEndpointNotURL,
		"https:///" + secret:                                       ErrEndpointNotURL,
		"ntfy.sh/" + secret:                                        ErrEndpointNotURL,
		"https://ntfy.sh:99999/" + secret:                          ErrEndpointNotURL,
		"https://ntfy..sh/" + secret:                               ErrEndpointNotURL,
		"https://user@ntfy.sh/" + secret:                           ErrEndpointUserinfo,
		"https://user:pw@ntfy.sh/" + secret:                        ErrEndpointUserinfo,
		"ftp://ntfy.sh/" + secret:                                  ErrEndpointScheme,
		"wss://ntfy.sh/" + secret:                                  ErrEndpointScheme,
		"http://ntfy.sh/" + secret:                                 ErrEndpointPlainHTTP,
		"http://localhost:7789/" + secret:                          ErrEndpointPlainHTTP,
		"http://push.lan:8080/" + secret:                           ErrEndpointPlainHTTP,
		"https://127.0.0.1:7788/" + secret:                         ErrEndpointAddress,
		"https://10.0.0.1/" + secret:                               ErrEndpointAddress,
		"https://[::1]/" + secret:                                  ErrEndpointAddress,
		"https://[::ffff:127.0.0.1]:7788/" + secret:                ErrEndpointAddress,
		"https://[::7f00:1]/" + secret:                             ErrEndpointAddress,
		"https://[fe80::1]/" + secret:                              ErrEndpointAddress,
		"https://169.254.169.254/" + secret:                        ErrEndpointAddress,
		"https://[64:ff9b::a9fe:a9fe]/" + secret:                   ErrEndpointAddress,
		"https://192.168.1.20/" + secret:                           ErrEndpointAddress,
	} {
		_, err := ValidateEndpoint(raw, r)
		require.ErrorIs(t, err, want, "%q", raw)
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, err.Error(), "ntfy")
	}
}

func TestOnlyListedPushServersWhenTheAdminSaysSo(t *testing.T) {
	r := rules(t, "push.lan\nlocalhost:7788", true)

	_, err := ValidateEndpoint("https://ntfy.sh/"+secret, r)
	require.ErrorIs(t, err, ErrEndpointNotListed)

	_, err = ValidateEndpoint("https://push.lan:8443/"+secret, r)
	require.ErrorIs(t, err, ErrEndpointNotListed)

	_, err = ValidateEndpoint("https://push.lan/"+secret, r)
	require.NoError(t, err)

	_, err = ValidateEndpoint("http://localhost:7788/"+secret, r)
	require.NoError(t, err)

	// A listed loopback entry allows this machine; a listed private address
	// is allowed; a listed link-local one still is not.
	r = rules(t, "127.0.0.1:7788\n192.168.1.20\n169.254.169.254", false)
	_, err = ValidateEndpoint("http://127.0.0.1:7788/"+secret, r)
	require.NoError(t, err)
	_, err = ValidateEndpoint("https://192.168.1.20/"+secret, r)
	require.NoError(t, err)
	_, err = ValidateEndpoint("https://169.254.169.254/"+secret, r)
	require.ErrorIs(t, err, ErrEndpointAddress)
}

func TestHostOfAnAddressThatDoesNotPass(t *testing.T) {
	require.Equal(t, "ntfy.sh", HostOf("http://ntfy.sh/"+secret))
	require.Equal(t, "10.0.0.1:8080", HostOf("https://10.0.0.1:8080/"+secret))
	require.Equal(t, "", HostOf("not a url"))
	require.Equal(t, "", HostOf("/"+secret))
}
