package namespace

import (
	"strings"
	"testing"
)

func assertHTMLParsing(t *testing.T, body, name, matchString string) {
	matchEntries := mustEntries(matchString)

	entries, err := parseHTMLHead(strings.NewReader(body), name)
	if err != nil {
		t.Fatalf("Error resolving name %q: %v", name, err)
	}
	if len(entries.entries) != len(matchEntries.entries) {
		t.Fatalf("Unexpected number of entries for %q: %d, expected %d", name, len(entries.entries), len(matchEntries.entries))
	}

	for i := range entries.entries {
		assertEntryEqual(t, entries.entries[i], matchEntries.entries[i])
	}
}

func TestParseHtmlHeadOk(t *testing.T) {
	okBody := `
<meta name="docker-scope" content="example.com"><!-- comment -->
<meta name="docker-registry-push" content="https://registry.example.com/v2/ version=2.0 trim">
<meta name="docker-registry" content="https://registry.example.com/v1/          version=1.0">
<meta name="docker-registry-pull" content="https://registry.mirror.com/v2/ version=2.0">
<meta name="docker-registry-pull" content="http://registry.mirror.com/v2/ version=2.0">
<meta name="docker-index" content="https://search.mirror.com/v1/ version=1.0">
`

	assertHTMLParsing(t, okBody, "example.com/my/app", `
example.com        push         https://registry.example.com/v2/	version=2.0 trim
example.com        push         https://registry.example.com/v1/	version=1.0
example.com        pull         https://registry.example.com/v1/	version=1.0
example.com        pull			https://registry.mirror.com/v2/		version=2.0
example.com        pull			http://registry.mirror.com/v2/		version=2.0
example.com        index        https://search.mirror.com/v1/		version=1.0
`)

	okBodyWithHead := `
<head>
<meta name="docker-scope" content="example.com/other"><!-- Applies to all metadata --></meta>
<meta name="docker-namespace" content="example.com"></meta>
<meta name="docker-registry" content="https://other.example.com/v1/ version=1.0"></meta>
</head>
`

	assertHTMLParsing(t, okBodyWithHead, "example.com/other/with/head", `
example.com/other  namespace    example.com
example.com/other  pull         https://other.example.com/v1/ version=1.0
example.com/other  push         https://other.example.com/v1/ version=1.0
`)

	okMissingScope := `
<meta name="docker-registry-push" content="https://registry.example.com/v1/ version=1.0">
<meta name="docker-registry-pull" content="http://mirror.example.com/v2/ version=2.0">
<meta name="docker-index" content="https://index.mirror.com/v1/ version=1.0">
`

	assertHTMLParsing(t, okMissingScope, "example.com/missing/scope", `
example.com/missing/scope		push	https://registry.example.com/v1/ version=1.0
example.com/missing/scope		pull	http://mirror.example.com/v2/ version=2.0
example.com/missing/scope		index	https://index.mirror.com/v1/ version=1.0
`)

	okDuplicateEntries := `
<head>
<meta name="docker-scope" content="example.com">
<meta name="docker-namespace" content="example.com/other"></meta>
<meta name="docker-registry" content="https://registry.example.com/v1/ version=1.0"></meta>
<meta name="docker-registry-pull" content="https://registry.mirror.com/v2/ version=2.0"></meta>
<!-- comment -->
<meta name="docker-registry-pull" content="http://registry.mirror.com/v2/ version=2.0"></meta>
<meta name="docker-index" content="https://search.mirror.com/v1/ version=1.0"></meta>
<meta name="docker-registry" content="https://registry.example.com/v1/ version=1.0"></meta>
<meta name="docker-index" content="https://search.mirror.com/v1/ version=1.0"></meta>
</head>
`

	assertHTMLParsing(t, okDuplicateEntries, "example.com/duplicate/entries", `
example.com			namespace	example.com/other
example.com			index		https://search.mirror.com/v1/ version=1.0
example.com			pull		http://registry.mirror.com/v2/ version=2.0
example.com			pull		https://registry.example.com/v1/ version=1.0
example.com			pull		https://registry.mirror.com/v2/ version=2.0
example.com			push		https://registry.example.com/v1/ version=1.0
`)
}

func TestParseHtmlHeadBad(t *testing.T) {
	badDoubleScope := `
<meta name="docker-scope" content="example.com">
<meta name="docker-scope" content="example.com/other">
<meta name="docker-namespace" content="example.com">
<meta name="docker-registry" content="https://registry.example.com/v1/ version=1.0">
<meta name="docker-index" content="https://search.mirror.com/v1/ version=1.0">
`

	_, err := parseHTMLHead(strings.NewReader("example.com/double/scope"), badDoubleScope)
	if err == nil {
		t.Errorf("Parsing of body with double scope tags should have failed.")
	}

	// Should no entries be really considered as parsing error?
	for _, body := range []string{
		"<head></head>",
		"",
		// scope without any entry meaningless
		`<meta name="docker-scope" content="example.com">`,
	} {
		_, err := parseHTMLHead(strings.NewReader(body), "example.com/no/entries")
		if err == nil {
			t.Errorf("Parsing of body without any meta tags should have failed.")
		}
	}
}

func assertHTTPResolve(t *testing.T, r Resolver, name, matchString string, shouldContactServer bool) {
	testServerName := strings.SplitN(name, "/", 2)[0]
	lastAccess := testServerLastAccessed[testServerName]
	matchEntries := mustEntries(matchString)

	entries, err := r.Resolve(name)
	if err != nil {
		t.Fatalf("Failed to resolve name %q: %v", name, err)
	}
	if len(entries.entries) != len(matchEntries.entries) {
		t.Errorf("Unexpected number of entries for %q: %d, expected %d", name, len(entries.entries), len(matchEntries.entries))
	}

	for i := range entries.entries {
		assertEntryEqual(t, entries.entries[i], matchEntries.entries[i])
	}

	if shouldContactServer {
		if !testServerLastAccessed[testServerName].After(lastAccess) {
			t.Errorf("Resolver should have accessed test server %s when querying %s", testServerName, name)
		}
	} else {
		if testServerLastAccessed[testServerName].After(lastAccess) {
			t.Errorf("Resolver should have hit cache when querying %s", name)
		}
	}

}

func TestHTTPResolver(t *testing.T) {
	r := NewHTTPResolver(&HTTPResolverConfig{Client: newMockHTTPClient()})

	assertHTTPResolve(t, r, "example.com/library/bar", `
example.com			index		https://search.example.com/
example.com			pull		https://registry.example.com/v1/ version=1.0 trim
example.com			push		https://registry.example.com/v1/ version=1.0 trim
`, true)

	assertHTTPResolve(t, r, "example.com/foo/app", `
example.com/foo		index		https://search.foo.com/
example.com/foo		pull		https://mirror.foo.com/v1/ version=1.0
example.com/foo		push		https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace	example.com
`, true)

	assertHTTPResolve(t, r, "example.com/project/main", `
example.com/project/main index		https://search.project.com/
example.com/project/main pull		https://mirror.project.com/v2/ version=2.0.1
example.com/project/main push		https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main namespace	example.com/project
`, true)

	name := "example.com/foo/not/existent"
	entries, err := r.Resolve(name)
	if err == nil {
		t.Errorf("Resolving of %q should have failed but returned entries: %v", name, entries)
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("Expected 404 error, got: %v", err)
	}
}

func TestHTTPRecursiveResolver(t *testing.T) {
	const recursionLimit = 5
	resolvers := make([]Resolver, recursionLimit)
	hr := NewHTTPResolver(&HTTPResolverConfig{Client: newMockHTTPClient()})
	for i := 0; i < recursionLimit; i++ {
		resolvers[i] = hr
	}
	r := NewMultiResolver(resolvers...)

	assertHTTPResolve(t, r, "example.com/foo/app", `
example.com/foo		index	  https://search.foo.com/
example.com/foo		pull	  https://mirror.foo.com/v1/ version=1.0
example.com/foo		push	  https://registry.foo.com/v1/ version=1.0
example.com/foo     namespace example.com
example.com			index	  https://search.example.com/
example.com			pull	  https://registry.example.com/v1/ version=1.0 trim
example.com			push	  https://registry.example.com/v1/ version=1.0 trim
`, true)

	assertHTTPResolve(t, r, "other.com/big/foo", `
other.com/big/foo	namespace	other.com/big
other.com			index	https://other.com/v1/
other.com           pull	https://mirror.other.com/v2/   version=2.0
other.com           push	https://registry.other.com/v1/ version=1.0
`, true)

	assertHTTPResolve(t, r, "example.com/project/main", `
example.com/project/main	index		https://search.project.com/
example.com/project/main	pull		https://mirror.project.com/v2/ version=2.0.1
example.com/project/main	push		https://registry-1.project.com/v2/ version=2.0.1
example.com/project/main	namespace	example.com/project
example.com/project			index		https://search.company.ltd/
example.com/project			pull		https://registry.company.ltd/v2/ version=2.0 trim
example.com/project			push		https://registry.company.ltd/v2/ version=2.0 trim
example.com/project         namespace
`, true)

	name := "other.com/big/foo/app"
	entries, err := r.Resolve(name)
	if err == nil {
		t.Errorf("Resolving of %q should have failed but returned entries: %v", name, entries)
	} else if !strings.Contains(err.Error(), "invalid extension") {
		t.Errorf("Expected \"invalid extension\" error, got: %v", err)
	}
}

func TestHTTPRecursiveResolverAcceptArbitraryExtensions(t *testing.T) {
	const recursionLimit = 5
	resolvers := make([]Resolver, recursionLimit)
	config := HTTPResolverConfig{
		Client:          newMockHTTPClient(),
		ResolverFactory: PassEntriesResolverFactory,
	}
	hr := NewHTTPResolver(&config)
	for i := 0; i < recursionLimit; i++ {
		resolvers[i] = hr
	}
	r := NewMultiResolver(resolvers...)

	assertHTTPResolve(t, r, "other.com/big/foo", `
other.com/big/foo	namespace	other.com/big
other.com			index	https://other.com/v1/
other.com           pull	https://mirror.other.com/v2/   version=2.0
other.com           push	https://registry.other.com/v1/ version=1.0
`, true)

	assertHTTPResolve(t, r, "other.com/big/foo/app", `
other.com/big/foo/app		index		https://index.big.com/v1/
other.com/big/foo/app		pull		https://registry.other.com/v2/ version=2.0
other.com/big/foo/app		push		https://registry.other.com/v2/ version=2.0
other.com/big/foo/app       namespace   example.com/project other.com
example.com/project			index		https://search.company.ltd/
example.com/project			pull		https://registry.company.ltd/v2/ version=2.0 trim
example.com/project			push		https://registry.company.ltd/v2/ version=2.0 trim
example.com/project         namespace
other.com					index		https://other.com/v1/
other.com					pull		https://mirror.other.com/v2/   version=2.0
other.com					push		https://registry.other.com/v1/ version=1.0
`, true)

	// unreachable namespace extension still results in failure
	name := "other.com/bad"
	entries, err := r.Resolve(name)
	if err == nil {
		t.Errorf("Resolving of %q should have failed but returned entries: %v", name, entries)
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("Expected \"404\" error, got: %v", err)
	}
}
