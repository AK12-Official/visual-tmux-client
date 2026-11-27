package host

// Programs differ in whether they read the lower- or the upper-case name, so
// every variable is set in both forms.
var (
	proxyVariableNames  = []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"}
	proxyExclusionNames = []string{"no_proxy", "NO_PROXY"}
)

// proxyExclusions lists what must not be sent through the proxy. A domain is
// written with a leading dot, the form curl, Git, Python and Go all read as
// "any name under this domain".
const proxyExclusions = "localhost,127.0.0.1,::1,.local"

// ProxyEnvironment returns the variables that let commands in a terminal use
// proxyAddress, the stored host:port form. The scheme is socks5h so that names
// are resolved by the proxy, as the application's own connections do. An empty
// address adds nothing.
func ProxyEnvironment(proxyAddress string) map[string]string {
	if proxyAddress == "" {
		return nil
	}
	environment := make(map[string]string, len(proxyVariableNames)+len(proxyExclusionNames))
	for _, name := range proxyVariableNames {
		environment[name] = "socks5h://" + proxyAddress
	}
	for _, name := range proxyExclusionNames {
		environment[name] = proxyExclusions
	}
	return environment
}
