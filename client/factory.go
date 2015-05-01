package client

import (
	"net/http"
	"os"
	"strings"

	"golang.org/x/net/context"

	"github.com/docker/distribution"
	"github.com/docker/distribution/namespace"
	rclient "github.com/docker/distribution/registry/client"
)

// RepositoryClientConfig is used to create new clients from endpoints
type RepositoryClientConfig struct {
	TrimHostname bool
	AllowMirrors bool
	Header       http.Header

	NamespaceFile string
	// Discovery method

	Credentials rclient.CredentialStore
}

// Resolver returns a new namespace resolver using this repository
// client configuration.  If there is an error loading the configuration,
// an error will be returned with a nil resolver.
func (f *RepositoryClientConfig) Resolver() (namespace.Resolver, error) {
	// Read base entries from f.NamespaceFile
	nsf, err := os.Open(f.NamespaceFile)
	if err != nil {
		return nil, err
	}

	entries, err := namespace.ReadEntries(nsf)
	if err != nil {
		return nil, err
	}

	resolver := namespace.NewNamespaceResolver(entries, namespace.NopDiscoverer{}, f.newRepository)

	return resolver, nil
}

// type RepositoryClientFactory func(version string, registries, mirrors []string) (distribution.Repository, error)
func (f *RepositoryClientConfig) newRepository(ctx context.Context, namespace string, endpoints []*namespace.RemoteEndpoint) (distribution.Repository, error) {
	if f.TrimHostname {
		i := strings.IndexRune(namespace, '/')
		if i > -1 && i < len(namespace)-1 {
			// TODO(dmcgowan): Check if first element is actually hostname
			namespace = namespace[i+1:]
		}

	}

	// Currently only single endpoint repository used
	endpoint := &rclient.RepositoryEndpoint{
		Header:      f.Header,
		Credentials: f.Credentials,
	}

	// TODO Loop through and find endpoint
	endpoint.Endpoint = endpoints[0].BaseURL.String()

	//if f.AllowMirrors && len(mirrors) > 0 {
	//	endpoint.Endpoint = mirrors[0]
	//	endpoint.Mirror = true
	//}
	//if endpoint.Endpoint == "" && len(registries) > 0 {
	//	endpoint.Endpoint = registries[0]
	//}

	//if endpoint.Endpoint == "" {
	//	return nil, errors.New("No valid endpoints")
	//}

	return rclient.NewRepositoryClient(context.Background(), namespace, endpoint)
}
