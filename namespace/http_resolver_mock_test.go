package namespace

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
)

type testScopeAttr struct {
	Name htmlMetaTagEnum
	URL  string
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
		},
	}

	testScopes = map[string][]testScopeAttr{
		"example.com": {
			{htmlMetaTagScope, "example.com", []string{}},
			{htmlMetaTagIndex, "https://search.example.com/", []string{}},
			{htmlMetaTagRegistry, "https://registry.example.com/v1/", []string{"version=1.0", "trim"}},
		},
		"example.com/foo": {
			{htmlMetaTagScope, "example.com/foo", []string{}},
			{htmlMetaTagIndex, "https://search.foo.com/", []string{}},
			{htmlMetaTagRegistryPull, "https://mirror.foo.com/v1/", []string{"version=1.0"}},
			{htmlMetaTagRegistryPush, "https://registry.foo.com/v1/", []string{"version=1.0"}},
			{htmlMetaTagNamespace, "example.com", []string{}},
		},
		"example.com/project": {
			{htmlMetaTagScope, "example.com/project", []string{}},
			{htmlMetaTagIndex, "https://search.company.ltd/", []string{}},
			{htmlMetaTagRegistry, "https://registry.company.ltd/v2/", []string{"version=2.0", "trim"}},
		},
		"example.com/project/main": {
			// missing scope entry - it should apply just to this namespace
			{htmlMetaTagIndex, "https://search.project.com/", []string{}},
			{htmlMetaTagRegistryPull, "https://mirror.project.com/v2/", []string{"version=2.0.1"}},
			{htmlMetaTagRegistryPush, "https://registry-1.project.com/v2/", []string{"version=2.0.1"}},
		},
		"other.com": {
			{htmlMetaTagScope, "other.com", []string{}},
			{htmlMetaTagIndex, "https://other.com/v1/", []string{}},
			{htmlMetaTagRegistryPull, "https://mirror.other.com/v2/", []string{"version=2.0"}},
			{htmlMetaTagRegistryPush, "https://registry.other.com/v1/", []string{"version=1.0"}},
		},
		"other.com/big/foo": {
			// just inherit parent namespace
			{htmlMetaTagScope, "other.com/big/foo", []string{}},
			{htmlMetaTagNamespace, "other.com/big", []string{"version=2.0"}},
		},
		"other.com/big/foo/app": {
			{htmlMetaTagScope, "other.com/big/foo/app", []string{}},
			{htmlMetaTagIndex, "https://index.big.com/v1/", []string{}},
			{htmlMetaTagRegistry, "https://mirror.other.com/v2/", []string{"version=2.0"}},
		},
	}

	// sorted list of scopes (from the longest to shortest")
	testScopeList []string

	testServerNameToAddr map[string]string
	testServerAddrToName map[string]string
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
	testServerNameToAddr = map[string]string{
		"example.com": strings.TrimPrefix(exampleServer.URL, "https://"),
		"other.com":   strings.TrimPrefix(otherServer.URL, "https://"),
	}
	testServerAddrToName = map[string]string{
		testServerNameToAddr["example.com"]: "example.com",
		testServerNameToAddr["other.com"]:   "other.com",
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
	newURL := strings.Replace(url, "https://example.com", exampleServer.URL, 1)
	if newURL == url {
		newURL = strings.Replace(url, "https://other.com", otherServer.URL, 1)
	}
	return c.Client.Get(newURL)
}

func newMockHTTPClient() *mockHTTPClient {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &mockHTTPClient{http.Client{Transport: tr}}
}

func getScopeAttrs(host, path string) []testScopeAttr {
	_, exists := testRepositories[host]
	if !exists {
		return nil
	}
	name := filepath.Join(host, path)
	if attrs, exists := testScopes[name]; exists {
		return attrs
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
		content := attr.URL
		if len(attr.Args) > 0 {
			content += " " + strings.Join(attr.Args, " ")
		}
		metaTagLine := fmt.Sprintf("<meta name=\"%s\" content=\"%s\">\n", attr.Name, content)
		w.Write([]byte(metaTagLine))
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
		logrus.Debugf("%s \"%s %s\"", r.RemoteAddr, r.Method, r.URL)
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
