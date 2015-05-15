package namespace

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
)

type testScopeAttr struct {
	Name htmlMetaTagEnum
	Args []string
}

type byStrLen []string

func (a byStrLen) Len() int           { return len(a) }
func (a byStrLen) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byStrLen) Less(i, j int) bool { return len(a[i]) > len(a[j]) }

var (
	exampleServer    *httptest.Server
	otherServer      *httptest.Server
	testRepositories = map[string][]string{
		"example.com": {
			"foo/app",
			"library/bar",
			"short",
			"project/alice/app",
			"project/bob/app",
			"project/bob/foo",
			"project/main/app",
			"project/main",
		},
		"other.com": {
			"my/app",
			"other/app",
			"big/foo/test",
			"big/foo/app",
			"big/user/bar",
			"bad/bar",
		},
	}

	testScopes = map[string][]testScopeAttr{
		"example.com": {
			{htmlMetaTagScope, []string{"example.com"}},
			{htmlMetaTagIndex, []string{"https://search.example.com/"}},
			{htmlMetaTagRegistry, []string{"https://registry.example.com/v1/", "version=1.0", "trim"}},
		},
		"example.com/foo": {
			{htmlMetaTagScope, []string{"example.com/foo"}},
			{htmlMetaTagIndex, []string{"https://search.foo.com/"}},
			{htmlMetaTagRegistryPull, []string{"https://mirror.foo.com/v1/", "version=1.0"}},
			{htmlMetaTagRegistryPush, []string{"https://registry.foo.com/v1/", "version=1.0"}},
			{htmlMetaTagNamespace, []string{"example.com"}},
		},
		"example.com/project": {
			{htmlMetaTagScope, []string{"example.com/project"}},
			{htmlMetaTagIndex, []string{"https://search.company.ltd/"}},
			{htmlMetaTagRegistry, []string{"https://registry.company.ltd/v2/", "version=2.0", "trim"}},
			{htmlMetaTagNamespace, []string{}},
		},
		"example.com/project/main": {
			// missing scope entry - it should apply just to this namespace
			{htmlMetaTagIndex, []string{"https://search.project.com/"}},
			{htmlMetaTagRegistryPull, []string{"https://mirror.project.com/v2/", "version=2.0.1"}},
			{htmlMetaTagRegistryPush, []string{"https://registry-1.project.com/v2/", "version=2.0.1"}},
			{htmlMetaTagNamespace, []string{"example.com/project"}},
		},
		"other.com": {
			{htmlMetaTagScope, []string{"other.com"}},
			{htmlMetaTagIndex, []string{"https://other.com/v1/"}},
			{htmlMetaTagRegistryPull, []string{"https://mirror.other.com/v2/", "version=2.0"}},
			{htmlMetaTagRegistryPush, []string{"https://registry.other.com/v1/", "version=1.0"}},
		},
		"other.com/big/foo": {
			// just inherit parent namespace
			{htmlMetaTagScope, []string{"other.com/big/foo"}},
			{htmlMetaTagNamespace, []string{"other.com/big"}},
		},
		// refer to totally different namespace
		"other.com/big/foo/app": {
			{htmlMetaTagScope, []string{"other.com/big/foo/app"}},
			{htmlMetaTagIndex, []string{"https://index.big.com/v1/"}},
			{htmlMetaTagRegistry, []string{"https://registry.other.com/v2/", "version=2.0"}},
			{htmlMetaTagNamespace, []string{"example.com/project", "other.com"}},
		},
		"other.com/bad": {
			{htmlMetaTagScope, []string{"other.com/bad"}},
			{htmlMetaTagIndex, []string{"https://index.bad.com/v1/"}},
			{htmlMetaTagRegistry, []string{"https://registry.bad.com/v2/", "version=2.0.1"}},
			{htmlMetaTagNamespace, []string{"other.com/not/found", "not.reachable.server", "example.com"}},
		},
	}

	// sorted list of scopes (from the longest to shortest")
	testScopeList []string

	// maps <address>:<port> to corresponding server name (e.g. example.com)
	testServerAddrToName = map[string]string{
		"41:41:41:41": "not.reachable.server",
	}

	// contains netries <serverName> : <timeOfLastAccess>
	testServerLastAccessed map[string]time.Time
)

func init() {
	r := mux.NewRouter()

	r.HandleFunc("/", discoveryHandler).Methods("GET").Queries("docker-discovery", "1")
	r.HandleFunc("/{path:.+}", discoveryHandler).Methods("GET").Queries("docker-discovery", "1")
	for domain, _ := range testRepositories {
		r.Host(domain)
	}

	exampleServer = httptest.NewTLSServer(handlerAccessLog(r))
	otherServer = httptest.NewTLSServer(handlerAccessLog(r))

	testServerLastAccessed = make(map[string]time.Time, 2)
	for _, tup := range []struct{ name, url string }{
		{"example.com", exampleServer.URL},
		{"other.com", otherServer.URL},
	} {
		addr := strings.TrimPrefix(tup.url, "https://")
		testServerAddrToName[addr] = tup.name
		testServerLastAccessed[tup.name] = time.Now()
	}

	testScopeList = make([]string, 0, len(testScopes))
	for scp, _ := range testScopes {
		testScopeList = append(testScopeList, scp)
	}
	sort.Sort(byStrLen(testScopeList))
}

type mockHTTPClient struct {
	http.Client
}

func (c *mockHTTPClient) Get(url string) (*http.Response, error) {
	for addr, name := range testServerAddrToName {
		newURL := strings.Replace(url, "https://"+name, "https://"+addr, 1)
		if newURL != url {
			return c.Client.Get(newURL)
		}
	}
	panic(fmt.Sprintf("trying to reach external domain %q", url))
}

func newMockHTTPClient() *mockHTTPClient {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &mockHTTPClient{http.Client{Transport: tr}}
}

func getScopeAttrs(host, path string) []testScopeAttr {
	repos, exists := testRepositories[host]
	if !exists {
		return nil
	}
	name := filepath.Join(host, path)
	if attrs, exists := testScopes[name]; exists {
		return attrs
	}
	found := false
	for _, repo := range repos {
		if repo == path || strings.HasPrefix(repo, path+"/") {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	for _, scp := range testScopeList {
		if strings.HasPrefix(name, scp) {
			return testScopes[scp]
		}
	}
	return nil
}

// TODO: are these something that would docker-distribution endpoint return
func writeHeaders(w http.ResponseWriter, contentType string) {
	h := w.Header()
	h.Add("Server", "docker-tests/mock")
	h.Add("Expires", "-1")
	h.Add("Content-Type", contentType)
	h.Add("Pragma", "no-cache")
	h.Add("Cache-Control", "no-cache")
	h.Add("X-Docker-Registry-Version", "0.0.0")
	h.Add("X-Docker-Registry-Config", "mock")
}

func writeResponse(w http.ResponseWriter, message interface{}, code int) {
	writeHeaders(w, "application/json")
	w.WriteHeader(code)
	body, err := json.Marshal(message)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}
	w.Write(body)
}

func writeAttributes(w http.ResponseWriter, attrs []testScopeAttr) {
	writeHeaders(w, "text/html")
	// TODO: does <head> tag belong here?
	w.Write([]byte("<head>\n"))
	for _, attr := range attrs {
		metaTagLine := fmt.Sprintf("<meta name=\"%s\" content=\"%s\">", attr.Name, strings.Join(attr.Args, " "))
		w.Write([]byte(metaTagLine + "\n"))
		logrus.Debugf(metaTagLine)
	}
	w.Write([]byte("</head>\n"))
}

// TODO: do we need to write any error messages in response's body?
// TODO: and if so, does it have to be json?
func apiError(w http.ResponseWriter, code int, message string, args ...interface{}) {
	body := map[string]string{
		"error": fmt.Sprintf(message, args...),
	}
	writeResponse(w, body, code)
}

func handlerAccessLog(handler http.Handler) http.Handler {
	logHandler := func(w http.ResponseWriter, r *http.Request) {
		logrus.Debugf("[host=%s (%s)]: %s \"%s %s\"", testServerAddrToName[r.Host], r.Host, r.RemoteAddr, r.Method, r.URL)
		testServerLastAccessed[testServerAddrToName[r.Host]] = time.Now()
		handler.ServeHTTP(w, r)
	}
	return http.HandlerFunc(logHandler)
}

func discoveryHandler(w http.ResponseWriter, r *http.Request) {
	hostname := testServerAddrToName[r.Host]
	_, exists := testRepositories[hostname]
	if !exists {
		apiError(w, 400, "Unexpected host %q", r.Host)
	}

	path := mux.Vars(r)["path"]
	attrs := getScopeAttrs(hostname, path)
	if attrs == nil {
		apiError(w, 404, "Path not found")
	}
	writeAttributes(w, attrs)
}

func TestMain(t *testing.M) {
	flag.Parse()
	if testing.Verbose() {
		logrus.SetLevel(logrus.DebugLevel)
	}
	os.Exit(t.Run())
}
